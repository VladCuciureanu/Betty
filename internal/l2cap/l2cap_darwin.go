//go:build darwin

package l2cap

/*
#cgo LDFLAGS: -framework Foundation -framework IOBluetooth
#include <stdlib.h>
#include "l2cap_darwin.h"
*/
import "C"

import (
	"context"
	"fmt"
	"strings"
	"unsafe"

	"github.com/vladcuciureanu/betty/internal/domain"
	"github.com/vladcuciureanu/betty/internal/driver"
)

type darwinHost struct{}

func New() driver.Host { return &darwinHost{} }
func (*darwinHost) OpenL2CAP(ctx context.Context, deviceID string, psm uint16) (string, error) {
	address, ok := strings.CutPrefix(deviceID, "macos:")
	if !ok {
		return "", domain.NewError(domain.CodeUnsupported, "device %q has no macOS Classic address", deviceID)
	}
	cAddress := C.CString(address)
	defer C.free(unsafe.Pointer(cAddress))
	type result struct {
		id   int64
		code int
	}
	done := make(chan result, 1)
	go func() {
		var code C.int
		id := C.betty_l2cap_open(cAddress, C.ushort(psm), &code)
		done <- result{int64(id), int(code)}
	}()
	select {
	case <-ctx.Done():
		go func() {
			r := <-done
			if r.id > 0 {
				C.betty_l2cap_close(C.longlong(r.id))
			}
		}()
		return "", ctx.Err()
	case r := <-done:
		if r.code != 0 || r.id <= 0 {
			// Apple's public IOBluetooth API reaches the paired device but rejects
			// the reserved Apple Accessory Protocol channel used by AirPods and Beats.
			if psm == 0x1001 && uint32(r.code) == ioReturnError {
				return "", domain.NewError(domain.CodeUnsupported, "macOS does not permit third-party apps to open the Apple audio control channel (PSM 0x1001)")
			}
			return "", nativeError("open", r.code)
		}
		return fmt.Sprintf("darwin-%d", r.id), nil
	}
}
func (*darwinHost) ReadL2CAP(ctx context.Context, channelID string, max int) ([]byte, error) {
	id, err := parseChannel(channelID)
	if err != nil {
		return nil, err
	}
	buffer := make([]byte, max)
	for {
		var code C.int
		n := int(C.betty_l2cap_read(C.longlong(id), unsafe.Pointer(&buffer[0]), C.int(max), C.int(200), &code))
		if code != 0 {
			return nil, nativeError("read", int(code))
		}
		if n > 0 {
			return buffer[:n], nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}
}
func (*darwinHost) WriteL2CAP(_ context.Context, channelID string, data []byte) (int, error) {
	id, err := parseChannel(channelID)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, nil
	}
	code := int(C.betty_l2cap_write(C.longlong(id), unsafe.Pointer(&data[0]), C.int(len(data))))
	if code != 0 {
		return 0, nativeError("write", code)
	}
	return len(data), nil
}
func (*darwinHost) CloseL2CAP(_ context.Context, channelID string) error {
	id, err := parseChannel(channelID)
	if err != nil {
		return err
	}
	code := int(C.betty_l2cap_close(C.longlong(id)))
	if code != 0 {
		return nativeError("close", code)
	}
	return nil
}
func parseChannel(value string) (int64, error) {
	var id int64
	if _, err := fmt.Sscanf(value, "darwin-%d", &id); err != nil || id < 1 {
		return 0, domain.NewError(domain.CodeUsage, "invalid L2CAP channel %q", value)
	}
	return id, nil
}
func nativeError(operation string, code int) error {
	value := uint32(code)
	name := ioReturnNames[value]
	if name == "" {
		name = "unknown IOReturn"
	}
	return fmt.Errorf("macOS L2CAP %s failed: %s (0x%08x)", operation, name, value)
}

const ioReturnError uint32 = 0xe00002bc

var ioReturnNames = map[uint32]string{
	ioReturnError: "kIOReturnError",
	0xe00002c0:    "kIOReturnNoDevice",
	0xe00002c1:    "kIOReturnNotPrivileged",
	0xe00002c2:    "kIOReturnBadArgument",
	0xe00002c5:    "kIOReturnExclusiveAccess",
	0xe00002c7:    "kIOReturnUnsupported",
	0xe00002d5:    "kIOReturnBusy",
	0xe00002d6:    "kIOReturnTimeout",
	0xe00002d7:    "kIOReturnOffline",
}
