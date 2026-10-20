//go:build linux

package l2cap

import (
	"context"
	"errors"
	"fmt"

	"github.com/vladcuciureanu/betty/internal/btsock"
	"github.com/vladcuciureanu/betty/internal/domain"
	"github.com/vladcuciureanu/betty/internal/driver"
	"golang.org/x/sys/unix"
)

type linuxHost struct {
	channels *btsock.Channels
}

func New() driver.Host { return &linuxHost{channels: btsock.NewChannels("L2CAP", "linux-")} }

func (h *linuxHost) OpenL2CAP(ctx context.Context, deviceID string, psm uint16) (string, error) {
	address, err := btsock.ParseAddress(deviceID, "bluez:")
	if err != nil {
		return "", err
	}
	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_SEQPACKET|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, unix.BTPROTO_L2CAP)
	if err != nil {
		if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
			return "", domain.NewError(domain.CodePermission, "opening a Bluetooth L2CAP connection was denied by the system security policy")
		}
		return "", fmt.Errorf("open Linux L2CAP socket: %w", err)
	}
	// unix.SockaddrL2 takes the address in display order.
	if err := btsock.Connect(ctx, fd, &unix.SockaddrL2{PSM: psm, Addr: address}); err != nil {
		_ = unix.Close(fd)
		if ctx.Err() != nil {
			return "", err
		}
		return "", fmt.Errorf("connect Linux L2CAP PSM 0x%04x: %w", psm, err)
	}
	return h.channels.Add(fd), nil
}

func (h *linuxHost) ReadL2CAP(ctx context.Context, channelID string, max int) ([]byte, error) {
	var data []byte
	err := h.channels.Use(channelID, func(fd int) error {
		var err error
		data, err = btsock.Read(ctx, fd, max)
		// SOCK_SEQPACKET never delivers empty packets, so zero bytes means
		// the peer closed the channel.
		if errors.Is(err, btsock.ErrPeerClosed) {
			return domain.NewError(domain.CodeDisconnected, "L2CAP channel closed by the device")
		}
		return err
	})
	return data, err
}

func (h *linuxHost) WriteL2CAP(ctx context.Context, channelID string, data []byte) (int, error) {
	var written int
	err := h.channels.Use(channelID, func(fd int) error {
		var err error
		written, err = btsock.Write(ctx, fd, data)
		return err
	})
	return written, err
}

func (h *linuxHost) CloseL2CAP(_ context.Context, channelID string) error {
	return h.channels.Close(channelID)
}
