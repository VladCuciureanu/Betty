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
	"fmt"
	"unsafe"

	"github.com/vladcuciureanu/betty/internal/domain"
)

type darwinBackend struct{}

func newPlatformBackend() Backend { return &darwinBackend{} }
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
func (*darwinBackend) Pair(_ context.Context, d domain.Device) error {
	return darwinCall(d, "pair")
}
func (*darwinBackend) Connect(_ context.Context, d domain.Device) error {
	return darwinCall(d, "connect")
}
func (*darwinBackend) Disconnect(_ context.Context, d domain.Device) error {
	return darwinCall(d, "disconnect")
}
func (*darwinBackend) Unpair(context.Context, domain.Device) error {
	return domain.NewError(domain.CodeUnsupported, "macOS does not expose a supported public API for programmatic unpairing; remove the device in System Settings")
}

func darwinCall(d domain.Device, operation string) error {
	address := classicAddress(d)
	if address == "" {
		return domain.NewError(domain.CodeUnsupported, "device has no macOS Classic endpoint")
	}
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
	if code == 0 {
		return nil
	}
	return fmt.Errorf("macOS Bluetooth operation failed (IOReturn %d)", code)
}
func classicAddress(d domain.Device) string {
	for _, e := range d.Endpoints {
		if e.Transport == domain.TransportClassic || e.Transport == domain.TransportOS {
			return e.Address
		}
	}
	return ""
}
