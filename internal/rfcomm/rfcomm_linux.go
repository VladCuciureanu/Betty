//go:build linux

package rfcomm

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/vladcuciureanu/betty/internal/btsock"
	"github.com/vladcuciureanu/betty/internal/domain"
	"github.com/vladcuciureanu/betty/internal/driver"
	"golang.org/x/sys/unix"
)

type host struct {
	channels *btsock.Channels
}

func New() driver.RFCOMMHost { return &host{channels: btsock.NewChannels("RFCOMM", "rfcomm-linux-")} }

func (h *host) OpenRFCOMM(ctx context.Context, deviceID, serviceUUID string) (string, error) {
	address, err := btsock.ParseAddress(deviceID, "bluez:")
	if err != nil {
		return "", err
	}
	channel, err := resolveChannel(ctx, address, serviceUUID)
	if err != nil {
		return "", err
	}
	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_STREAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, unix.BTPROTO_RFCOMM)
	if err != nil {
		return "", translateSocketError("open RFCOMM socket", err)
	}
	if err = btsock.Connect(ctx, fd, &unix.SockaddrRFCOMM{Addr: rfcommAddress(address), Channel: channel}); err != nil {
		_ = unix.Close(fd)
		return "", fmt.Errorf("connect RFCOMM service %s: %w", serviceUUID, err)
	}
	return h.channels.Add(fd), nil
}

func (h *host) ReadRFCOMM(ctx context.Context, channelID string, max int) ([]byte, error) {
	var data []byte
	err := h.channels.Use(channelID, func(fd int) error {
		var err error
		data, err = btsock.Read(ctx, fd, max)
		if errors.Is(err, btsock.ErrPeerClosed) {
			return domain.NewError(domain.CodeDisconnected, "RFCOMM channel closed by the device")
		}
		return err
	})
	return data, err
}

func (h *host) WriteRFCOMM(ctx context.Context, channelID string, data []byte) (int, error) {
	var written int
	err := h.channels.Use(channelID, func(fd int) error {
		var err error
		written, err = btsock.WriteAll(ctx, fd, data)
		return err
	})
	return written, err
}

func (h *host) CloseRFCOMM(_ context.Context, channelID string) error {
	return h.channels.Close(channelID)
}

func resolveChannel(ctx context.Context, address [6]byte, serviceUUID string) (uint8, error) {
	uuid, err := parseUUID(serviceUUID)
	if err != nil {
		return 0, err
	}
	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_SEQPACKET|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, unix.BTPROTO_L2CAP)
	if err != nil {
		return 0, translateSocketError("open Bluetooth SDP socket", err)
	}
	defer unix.Close(fd)
	if err := btsock.Connect(ctx, fd, &unix.SockaddrL2{PSM: 0x0001, Addr: address}); err != nil {
		return 0, fmt.Errorf("connect Bluetooth SDP: %w", err)
	}
	transaction := uint16(1)
	var continuation []byte
	var attributes []byte
	for pages := 0; pages < 8; pages++ {
		request := serviceAttributeRequest(transaction, uuid, continuation)
		if _, err := btsock.Write(ctx, fd, request); err != nil {
			return 0, fmt.Errorf("write Bluetooth SDP request: %w", err)
		}
		response, err := btsock.Read(ctx, fd, 65535)
		if err != nil {
			return 0, fmt.Errorf("read Bluetooth SDP response: %w", err)
		}
		page, next, err := parseServiceAttributeResponse(response, transaction)
		if err != nil {
			return 0, err
		}
		attributes = append(attributes, page...)
		if len(next) == 0 {
			if channel, ok := findRFCOMMChannel(attributes); ok {
				return channel, nil
			}
			return 0, domain.NewError(domain.CodeUnsupported, "Bluetooth service %s has no RFCOMM channel", serviceUUID)
		}
		continuation = next
		transaction++
	}
	return 0, fmt.Errorf("Bluetooth SDP response exceeded continuation limit")
}

func serviceAttributeRequest(transaction uint16, uuid [16]byte, continuation []byte) []byte {
	params := []byte{0x35, 0x11, 0x1c}
	params = append(params, uuid[:]...)
	params = append(params, 0xff, 0xff, 0x35, 0x03, 0x09, 0x00, 0x04, byte(len(continuation)))
	params = append(params, continuation...)
	packet := make([]byte, 5, 5+len(params))
	packet[0] = 0x06
	binary.BigEndian.PutUint16(packet[1:3], transaction)
	binary.BigEndian.PutUint16(packet[3:5], uint16(len(params)))
	return append(packet, params...)
}

func parseServiceAttributeResponse(packet []byte, transaction uint16) ([]byte, []byte, error) {
	if len(packet) < 8 || packet[0] != 0x07 {
		return nil, nil, fmt.Errorf("invalid Bluetooth SDP service-attribute response")
	}
	if binary.BigEndian.Uint16(packet[1:3]) != transaction {
		return nil, nil, fmt.Errorf("Bluetooth SDP transaction mismatch")
	}
	parameterLength := int(binary.BigEndian.Uint16(packet[3:5]))
	if len(packet) != 5+parameterLength {
		return nil, nil, fmt.Errorf("invalid Bluetooth SDP response length")
	}
	attributeLength := int(binary.BigEndian.Uint16(packet[5:7]))
	if 7+attributeLength >= len(packet) {
		return nil, nil, fmt.Errorf("truncated Bluetooth SDP attributes")
	}
	offset := 7 + attributeLength
	continuationLength := int(packet[offset])
	offset++
	if offset+continuationLength != len(packet) {
		return nil, nil, fmt.Errorf("invalid Bluetooth SDP continuation state")
	}
	return append([]byte(nil), packet[7:7+attributeLength]...), append([]byte(nil), packet[offset:]...), nil
}

func findRFCOMMChannel(attributes []byte) (uint8, bool) {
	for i := 0; i+4 < len(attributes); i++ {
		if attributes[i] != 0x19 || attributes[i+1] != 0x00 || attributes[i+2] != 0x03 {
			continue
		}
		switch attributes[i+3] {
		case 0x08:
			channel := attributes[i+4]
			return channel, channel >= 1 && channel <= 30
		case 0x09:
			if i+5 < len(attributes) {
				channel := binary.BigEndian.Uint16(attributes[i+4 : i+6])
				return uint8(channel), channel >= 1 && channel <= 30
			}
		}
	}
	return 0, false
}

func parseUUID(value string) ([16]byte, error) {
	var out [16]byte
	compact := strings.ReplaceAll(strings.TrimSpace(value), "-", "")
	decoded, err := hex.DecodeString(compact)
	if err != nil || len(decoded) != len(out) {
		return out, domain.NewError(domain.CodeUsage, "invalid 128-bit service UUID %q", value)
	}
	copy(out[:], decoded)
	return out, nil
}

// rfcommAddress converts an address in display order to the little-endian
// order that unix.SockaddrRFCOMM copies into the socket address unchanged.
// unix.SockaddrL2, by contrast, takes display order and reverses it itself.
func rfcommAddress(address [6]byte) [6]byte {
	var out [6]byte
	for i, b := range address {
		out[len(address)-1-i] = b
	}
	return out
}

func translateSocketError(operation string, err error) error {
	if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
		return domain.NewError(domain.CodePermission, "%s requires Bluetooth socket permission", operation)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
