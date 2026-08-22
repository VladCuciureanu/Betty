//go:build darwin

package backend

/*
#cgo LDFLAGS: -framework Foundation -framework IOBluetooth
#include <stdlib.h>
#include "bridge_darwin.h"
*/
import "C"

import (
	"context"
	"time"
	"unsafe"

	"github.com/vladcuciureanu/betty/internal/domain"
)

type darwinBackend struct{}

func newPlatformBackend(PairingIO) Backend { return &darwinBackend{} }
func (*darwinBackend) Capabilities() domain.Capabilities {
	return domain.Capabilities{Scan: domain.Supported, Pair: domain.Supported, Unpair: domain.Unsupported, Connect: domain.Supported, Disconnect: domain.Supported, GATT: domain.Supported}
}
func (b *darwinBackend) Scan(ctx context.Context) ([]domain.Device, error) { return b.List(ctx) }
func (*darwinBackend) List(context.Context) ([]domain.Device, error) {
	C.betty_refresh()
	return (&darwinBackend{}).devices(), nil
}
func (*darwinBackend) devices() []domain.Device {
	count := int(C.betty_paired_count())
	out := make([]domain.Device, 0, count)
	for i := 0; i < count; i++ {
		cname := C.betty_paired_name(C.int(i))
		name := C.GoString(cname)
		C.free(unsafe.Pointer(cname))
		caddress := C.betty_paired_address(C.int(i))
		address := C.GoString(caddress)
		C.free(unsafe.Pointer(caddress))
		connected := C.betty_paired_connected(C.int(i)) != 0
		paired := C.betty_device_paired(C.int(i)) != 0
		out = append(out, domain.Device{ID: "macos:" + address, Name: name, Paired: paired, Connected: connected, Endpoints: []domain.Endpoint{{ID: address, Address: address, Transport: domain.TransportClassic}}})
	}
	return out
}
func (*darwinBackend) Pair(ctx context.Context, d domain.Device) error {
	return darwinCall(ctx, d, "pair")
}
func (*darwinBackend) Connect(ctx context.Context, d domain.Device) error {
	return darwinCall(ctx, d, "connect")
}
func (*darwinBackend) Disconnect(ctx context.Context, d domain.Device) error {
	return darwinCall(ctx, d, "disconnect")
}
func (*darwinBackend) Unpair(context.Context, domain.Device) error {
	return domain.NewError(domain.CodeUnsupported, "macOS does not expose a supported public API for programmatic unpairing; remove the device in System Settings")
}

func darwinCall(ctx context.Context, d domain.Device, operation string) error {
	address := classicAddress(d)
	if address == "" {
		return domain.NewError(domain.CodeUnsupported, "device has no macOS Classic endpoint")
	}
	result := make(chan int, 1)
	go func() {
		c := C.CString(address)
		defer C.free(unsafe.Pointer(c))
		var code int
		switch operation {
		case "pair":
			code = int(C.betty_pair(c))
		case "connect":
			code = int(C.betty_connect(c))
		case "disconnect":
			code = int(C.betty_disconnect(c))
		}
		result <- code
	}()
	var code int
	select {
	case <-ctx.Done():
		return ctx.Err()
	case code = <-result:
	}
	if code == 0 {
		c := C.CString(address)
		defer C.free(unsafe.Pointer(c))
		desired := true
		state := func() bool { return C.betty_is_connected(c) != 0 }
		if operation == "pair" {
			state = func() bool { return C.betty_is_paired(c) != 0 }
		} else if operation == "disconnect" {
			desired = false
		}
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			if state() == desired {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
	}
	if operation == "pair" {
		return domain.NewError(domain.CodeAuthentication, "macOS pairing failed (IOReturn %d)", code)
	}
	return domain.NewError(domain.CodeUnavailable, "macOS Bluetooth %s failed (IOReturn %d)", operation, code)
}
func classicAddress(d domain.Device) string {
	for _, e := range d.Endpoints {
		if e.Transport == domain.TransportClassic || e.Transport == domain.TransportOS {
			return e.Address
		}
	}
	return ""
}
