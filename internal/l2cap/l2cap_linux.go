//go:build linux

package l2cap

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/vladcuciureanu/betty/internal/domain"
	"github.com/vladcuciureanu/betty/internal/driver"
	"golang.org/x/sys/unix"
)

type linuxHost struct {
	mu       sync.Mutex
	channels map[string]int
	next     atomic.Uint64
}

func New() driver.Host { return &linuxHost{channels: map[string]int{}} }

func (h *linuxHost) OpenL2CAP(ctx context.Context, deviceID string, psm uint16) (string, error) {
	address, err := linuxAddress(deviceID)
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
	ok := false
	defer func() {
		if !ok {
			_ = unix.Close(fd)
		}
	}()
	err = unix.Connect(fd, &unix.SockaddrL2{PSM: psm, Addr: address})
	if err != nil && !errors.Is(err, unix.EINPROGRESS) {
		return "", fmt.Errorf("connect Linux L2CAP PSM 0x%04x: %w", psm, err)
	}
	if err != nil {
		if err = waitFD(ctx, fd, unix.POLLOUT); err != nil {
			return "", err
		}
		socketErr, getErr := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_ERROR)
		if getErr != nil {
			return "", getErr
		}
		if socketErr != 0 {
			return "", fmt.Errorf("connect Linux L2CAP PSM 0x%04x: %w", psm, unix.Errno(socketErr))
		}
	}
	id := "linux-" + strconv.FormatUint(h.next.Add(1), 10)
	h.mu.Lock()
	h.channels[id] = fd
	h.mu.Unlock()
	ok = true
	return id, nil
}

func (h *linuxHost) ReadL2CAP(ctx context.Context, channelID string, max int) ([]byte, error) {
	fd, err := h.fd(channelID)
	if err != nil {
		return nil, err
	}
	for {
		buffer := make([]byte, max)
		n, err := unix.Read(fd, buffer)
		if err == nil {
			return buffer[:n], nil
		}
		if !errors.Is(err, unix.EAGAIN) {
			return nil, err
		}
		if err := waitFD(ctx, fd, unix.POLLIN); err != nil {
			return nil, err
		}
	}
}

func (h *linuxHost) WriteL2CAP(ctx context.Context, channelID string, data []byte) (int, error) {
	fd, err := h.fd(channelID)
	if err != nil {
		return 0, err
	}
	for {
		n, err := unix.Write(fd, data)
		if err == nil {
			return n, nil
		}
		if !errors.Is(err, unix.EAGAIN) {
			return 0, err
		}
		if err := waitFD(ctx, fd, unix.POLLOUT); err != nil {
			return 0, err
		}
	}
}

func (h *linuxHost) CloseL2CAP(_ context.Context, channelID string) error {
	h.mu.Lock()
	fd, ok := h.channels[channelID]
	if ok {
		delete(h.channels, channelID)
	}
	h.mu.Unlock()
	if !ok {
		return domain.NewError(domain.CodeNotFound, "L2CAP channel %q is not open", channelID)
	}
	return unix.Close(fd)
}

func (h *linuxHost) fd(id string) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fd, ok := h.channels[id]
	if !ok {
		return 0, domain.NewError(domain.CodeNotFound, "L2CAP channel %q is not open", id)
	}
	return fd, nil
}

func linuxAddress(deviceID string) ([6]byte, error) {
	var out [6]byte
	value := strings.TrimPrefix(deviceID, "bluez:")
	value = strings.ReplaceAll(value, "-", ":")
	parts := strings.Split(value, ":")
	if len(parts) != 6 {
		return out, domain.NewError(domain.CodeUsage, "device %q has no Linux Classic address", deviceID)
	}
	for i, part := range parts {
		n, err := strconv.ParseUint(part, 16, 8)
		if err != nil {
			return out, domain.NewError(domain.CodeUsage, "device %q has an invalid Bluetooth address", deviceID)
		}
		out[i] = byte(n)
	}
	return out, nil
}

func waitFD(ctx context.Context, fd int, events int16) error {
	for {
		timeout := 200
		if deadline, ok := ctx.Deadline(); ok {
			remaining := int(timeUntil(deadline).Milliseconds())
			if remaining <= 0 {
				return ctx.Err()
			}
			if remaining < timeout {
				timeout = remaining
			}
		}
		n, err := unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: events}}, timeout)
		if err != nil && !errors.Is(err, unix.EINTR) {
			return err
		}
		if n > 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}
