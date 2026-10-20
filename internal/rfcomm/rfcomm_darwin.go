//go:build darwin

package rfcomm

/*
#cgo LDFLAGS: -framework Foundation -framework IOBluetooth
#include <stdlib.h>
#include "rfcomm_darwin.h"
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

func New() driver.RFCOMMHost { return &darwinHost{} }

func (*darwinHost) OpenRFCOMM(ctx context.Context, deviceID, serviceUUID string) (string, error) {
	address, ok := strings.CutPrefix(deviceID, "macos:")
	if !ok {
		return "", domain.NewError(domain.CodeUnsupported, "device %q has no macOS Classic address", deviceID)
	}
	type result struct {
		id   int64
		code int
	}
	done := make(chan result, 1)
	go func() {
		// The goroutine owns the C strings: on cancel the caller returns while
		// betty_rfcomm_open may still be reading them.
		cAddress, cUUID := C.CString(address), C.CString(serviceUUID)
		defer C.free(unsafe.Pointer(cAddress))
		defer C.free(unsafe.Pointer(cUUID))
		var code C.int
		id := C.betty_rfcomm_open(cAddress, cUUID, &code)
		done <- result{int64(id), int(code)}
	}()
	select {
	case <-ctx.Done():
		go func() {
			r := <-done
			if r.id > 0 {
				C.betty_rfcomm_close(C.longlong(r.id))
			}
		}()
		return "", ctx.Err()
	case r := <-done:
		if r.code != 0 || r.id <= 0 {
			return "", nativeError("open", r.code)
		}
		return fmt.Sprintf("rfcomm-darwin-%d", r.id), nil
	}
}

func (*darwinHost) ReadRFCOMM(ctx context.Context, channelID string, max int) ([]byte, error) {
	id, err := parseChannel(channelID)
	if err != nil {
		return nil, err
	}
	buffer := make([]byte, max)
	for {
		var code C.int
		n := int(C.betty_rfcomm_read(C.longlong(id), unsafe.Pointer(&buffer[0]), C.int(max), 200, &code))
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

func (*darwinHost) WriteRFCOMM(_ context.Context, channelID string, data []byte) (int, error) {
	id, err := parseChannel(channelID)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, nil
	}
	if code := int(C.betty_rfcomm_write(C.longlong(id), unsafe.Pointer(&data[0]), C.int(len(data)))); code != 0 {
		return 0, nativeError("write", code)
	}
	return len(data), nil
}

func (*darwinHost) CloseRFCOMM(_ context.Context, channelID string) error {
	id, err := parseChannel(channelID)
	if err != nil {
		return err
	}
	if code := int(C.betty_rfcomm_close(C.longlong(id))); code != 0 {
		return nativeError("close", code)
	}
	return nil
}

func parseChannel(value string) (int64, error) {
	var id int64
	if _, err := fmt.Sscanf(value, "rfcomm-darwin-%d", &id); err != nil || id < 1 {
		return 0, domain.NewError(domain.CodeUsage, "invalid RFCOMM channel %q", value)
	}
	return id, nil
}

func nativeError(operation string, code int) error {
	value := uint32(code)
	return domain.NewError(ioReturnCode(value), "macOS RFCOMM %s failed with IOReturn 0x%08x", operation, value)
}

func ioReturnCode(value uint32) domain.ErrorCode {
	switch value {
	case 0xe00002c0, 0xe00002f0: // kIOReturnNoDevice, kIOReturnNotFound
		return domain.CodeNotFound
	case 0xe00002c1, 0xe00002e2: // kIOReturnNotPrivileged, kIOReturnNotPermitted
		return domain.CodePermission
	case 0xe00002c7: // kIOReturnUnsupported
		return domain.CodeUnsupported
	case 0xe00002d6: // kIOReturnTimeout
		return domain.CodeTimeout
	case 0xe00002cd, 0xe00002d7: // kIOReturnNotOpen, kIOReturnOffline
		return domain.CodeDisconnected
	case 0xe00002c2: // kIOReturnBadArgument
		return domain.CodeUsage
	}
	return domain.CodeUnavailable
}
