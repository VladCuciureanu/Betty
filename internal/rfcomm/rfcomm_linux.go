//go:build linux

package rfcomm

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vladcuciureanu/betty/internal/domain"
	"github.com/vladcuciureanu/betty/internal/driver"
	"golang.org/x/sys/unix"
)

type host struct {
	mu       sync.Mutex
	channels map[string]int
	next     atomic.Uint64
}

func New() driver.RFCOMMHost { return &host{channels: map[string]int{}} }

func (h *host) OpenRFCOMM(ctx context.Context, deviceID, serviceUUID string) (string, error) {
	address, err := bluetoothAddress(deviceID)
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
	ok := false
	defer func() {
		if !ok {
			_ = unix.Close(fd)
		}
	}()
	if err = connect(ctx, fd, &unix.SockaddrRFCOMM{Addr: address, Channel: channel}); err != nil {
		return "", fmt.Errorf("connect RFCOMM service %s: %w", serviceUUID, err)
	}
	id := "rfcomm-linux-" + strconv.FormatUint(h.next.Add(1), 10)
	h.mu.Lock()
	h.channels[id] = fd
	h.mu.Unlock()
	ok = true
	return id, nil
}

func (h *host) ReadRFCOMM(ctx context.Context, channelID string, max int) ([]byte, error) {
	fd, err := h.fd(channelID)
	if err != nil {
		return nil, err
	}
	for {
		buffer := make([]byte, max)
		n, err := unix.Read(fd, buffer)
		if err == nil {
			if n == 0 {
				return nil, fmt.Errorf("RFCOMM channel closed")
			}
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

func (h *host) WriteRFCOMM(ctx context.Context, channelID string, data []byte) (int, error) {
	fd, err := h.fd(channelID)
	if err != nil {
		return 0, err
	}
	written := 0
	for written < len(data) {
		n, writeErr := unix.Write(fd, data[written:])
		if writeErr == nil {
			written += n
			continue
		}
		if !errors.Is(writeErr, unix.EAGAIN) {
			return written, writeErr
		}
		if err := waitFD(ctx, fd, unix.POLLOUT); err != nil {
			return written, err
		}
	}
	return written, nil
}

func (h *host) CloseRFCOMM(_ context.Context, channelID string) error {
	h.mu.Lock()
	fd, ok := h.channels[channelID]
	if ok {
		delete(h.channels, channelID)
	}
	h.mu.Unlock()
	if !ok {
		return domain.NewError(domain.CodeNotFound, "RFCOMM channel %q is not open", channelID)
	}
	return unix.Close(fd)
}

func (h *host) fd(id string) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fd, ok := h.channels[id]
	if !ok {
		return 0, domain.NewError(domain.CodeNotFound, "RFCOMM channel %q is not open", id)
	}
	return fd, nil
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
	if err := connect(ctx, fd, &unix.SockaddrL2{PSM: 0x0001, Addr: address}); err != nil {
		return 0, fmt.Errorf("connect Bluetooth SDP: %w", err)
	}
	transaction := uint16(1)
	var continuation []byte
	var attributes []byte
	for pages := 0; pages < 8; pages++ {
		request := serviceAttributeRequest(transaction, uuid, continuation)
		if _, err := writeAll(ctx, fd, request); err != nil {
			return 0, fmt.Errorf("write Bluetooth SDP request: %w", err)
		}
		response, err := readPacket(ctx, fd, 65535)
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

func bluetoothAddress(deviceID string) ([6]byte, error) {
	var out [6]byte
	value := strings.TrimPrefix(deviceID, "bluez:")
	parts := strings.Split(strings.ReplaceAll(value, "-", ":"), ":")
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

func connect(ctx context.Context, fd int, address unix.Sockaddr) error {
	err := unix.Connect(fd, address)
	if err == nil {
		return nil
	}
	if !errors.Is(err, unix.EINPROGRESS) {
		return err
	}
	if err := waitFD(ctx, fd, unix.POLLOUT); err != nil {
		return err
	}
	socketErr, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_ERROR)
	if err != nil {
		return err
	}
	if socketErr != 0 {
		return unix.Errno(socketErr)
	}
	return nil
}

func readPacket(ctx context.Context, fd, max int) ([]byte, error) {
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

func writeAll(ctx context.Context, fd int, data []byte) (int, error) {
	written := 0
	for written < len(data) {
		n, err := unix.Write(fd, data[written:])
		if err == nil {
			written += n
			continue
		}
		if !errors.Is(err, unix.EAGAIN) {
			return written, err
		}
		if err := waitFD(ctx, fd, unix.POLLOUT); err != nil {
			return written, err
		}
	}
	return written, nil
}

func waitFD(ctx context.Context, fd int, events int16) error {
	for {
		timeout := 200
		if deadline, ok := ctx.Deadline(); ok {
			remaining := int(time.Until(deadline).Milliseconds())
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

func translateSocketError(operation string, err error) error {
	if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
		return domain.NewError(domain.CodePermission, "%s requires Bluetooth socket permission", operation)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
