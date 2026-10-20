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
	"runtime/cgo"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/vladcuciureanu/betty/internal/domain"
)

const (
	ioReturnNoDevice      uint32 = 0xe00002c0
	ioReturnNotPrivileged uint32 = 0xe00002c1
	ioReturnUnsupported   uint32 = 0xe00002c7
	ioReturnBusy          uint32 = 0xe00002d5
	ioReturnTimeout       uint32 = 0xe00002d6
	ioReturnNotPermitted  uint32 = 0xe00002e2
	ioReturnAborted       uint32 = 0xe00002eb
)

type darwinBackend struct{ pairing PairingIO }

func newPlatformBackend(pairing PairingIO) Backend { return &darwinBackend{pairing: pairing} }

// Capabilities reports Scan as unsupported: IOBluetooth discovery of
// unpaired Classic devices is not implemented, so Scan only lists paired
// devices. Nearby BLE devices still come from the BLE client.
func (*darwinBackend) Capabilities() domain.Capabilities {
	return domain.Capabilities{Scan: domain.Unsupported, Pair: domain.Supported, Unpair: domain.Unsupported, Connect: domain.Supported, Disconnect: domain.Supported, GATT: domain.Supported}
}
func (b *darwinBackend) Scan(ctx context.Context) ([]domain.Device, error) { return b.List(ctx) }
func (*darwinBackend) List(context.Context) ([]domain.Device, error) {
	var count C.int
	devices := C.betty_copy_paired_devices(&count)
	defer C.betty_free_devices(devices, count)
	if devices == nil || count == 0 {
		return nil, nil
	}
	out := make([]domain.Device, 0, int(count))
	for _, d := range unsafe.Slice(devices, int(count)) {
		address := C.GoString(d.address)
		out = append(out, domain.Device{ID: "macos:" + address, Name: C.GoString(d.name), Paired: d.paired != 0, Connected: d.connected != 0, Endpoints: []domain.Endpoint{{ID: address, Address: address, Transport: domain.TransportClassic}}})
	}
	return out, nil
}
func (b *darwinBackend) Pair(ctx context.Context, d domain.Device) error {
	agent := newPairingAgent(b.pairing)
	err := darwinCall(ctx, d, "pair", agent)
	if err != nil && agent.needsInteraction() {
		return domain.NewError(domain.CodeInteraction, "pairing requires interactive input")
	}
	return err
}
func (*darwinBackend) Connect(ctx context.Context, d domain.Device) error {
	return darwinCall(ctx, d, "connect", nil)
}
func (*darwinBackend) Disconnect(ctx context.Context, d domain.Device) error {
	return darwinCall(ctx, d, "disconnect", nil)
}
func (*darwinBackend) Unpair(context.Context, domain.Device) error {
	return domain.NewError(domain.CodeUnsupported, "macOS does not expose a supported public API for programmatic unpairing; remove the device in System Settings")
}

func darwinCall(ctx context.Context, d domain.Device, operation string, agent *pairingAgent) error {
	address := classicAddress(d)
	if address == "" {
		return domain.NewError(domain.CodeUnsupported, "device has no macOS Classic endpoint")
	}
	// The goroutine owns the C string, so a cancelled caller never frees
	// memory the native call is still reading. cancel is Go memory that the
	// native pairing loop polls only while betty_pair runs.
	cancel := new(int32)
	result := make(chan int, 1)
	go func() {
		c := C.CString(address)
		defer C.free(unsafe.Pointer(c))
		var code int
		switch operation {
		case "pair":
			handle := cgo.NewHandle(agent)
			defer handle.Delete()
			code = int(C.betty_pair(c, C.int(timeoutMillis(ctx)), (*C.int)(unsafe.Pointer(cancel)), C.uintptr_t(handle)))
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
		atomic.StoreInt32(cancel, 1)
		return ctx.Err()
	case code = <-result:
	}
	if code != 0 {
		return darwinError(operation, uint32(code))
	}
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

func darwinError(operation string, code uint32) error {
	switch code {
	case ioReturnNoDevice:
		return domain.NewError(domain.CodeNotFound, "macOS has no Bluetooth device with that address")
	case ioReturnNotPrivileged, ioReturnNotPermitted:
		return domain.NewError(domain.CodePermission, "macOS denied Bluetooth %s (IOReturn 0x%08x)", operation, code)
	case ioReturnUnsupported:
		return domain.NewError(domain.CodeUnsupported, "macOS does not support Bluetooth %s for this device", operation)
	case ioReturnTimeout:
		return domain.NewError(domain.CodeTimeout, "macOS Bluetooth %s timed out", operation)
	case ioReturnAborted:
		return domain.NewError(domain.CodeTimeout, "macOS Bluetooth %s was cancelled", operation)
	case ioReturnBusy:
		return domain.NewError(domain.CodeUnavailable, "macOS Bluetooth is busy (IOReturn 0x%08x)", code)
	}
	if operation == "pair" {
		return domain.NewError(domain.CodeAuthentication, "macOS pairing failed (IOReturn 0x%08x)", code)
	}
	return domain.NewError(domain.CodeUnavailable, "macOS Bluetooth %s failed (IOReturn 0x%08x)", operation, code)
}

// timeoutMillis is the time left before ctx's deadline, rounded up so a
// sub-millisecond remainder is not mistaken for "no limit" (0).
func timeoutMillis(ctx context.Context) int {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 1
	}
	return int((remaining + time.Millisecond - 1) / time.Millisecond)
}

//export bettyPairPIN
func bettyPairPIN(handle C.uintptr_t, out *C.char, max C.int) C.int {
	agent := cgo.Handle(handle).Value().(*pairingAgent)
	if agent == nil {
		return 0
	}
	value, err := agent.prompt("Bluetooth PIN: ")
	if err != nil || value == "" || len(value) > int(max) {
		return 0
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(out)), int(max)), value)
	return C.int(len(value))
}

//export bettyPairConfirm
func bettyPairConfirm(handle C.uintptr_t, passkey C.uint) C.int {
	agent := cgo.Handle(handle).Value().(*pairingAgent)
	if agent == nil {
		return 0
	}
	if ok, err := agent.confirmed(fmt.Sprintf("Confirm passkey %06d? [y/N] ", uint32(passkey))); err != nil || !ok {
		return 0
	}
	return 1
}

//export bettyPairNotify
func bettyPairNotify(handle C.uintptr_t, passkey C.uint) {
	if agent := cgo.Handle(handle).Value().(*pairingAgent); agent != nil {
		agent.notice("Passkey: %06d (type it on the device)\n", uint32(passkey))
	}
}

func classicAddress(d domain.Device) string {
	for _, e := range d.Endpoints {
		if e.Transport == domain.TransportClassic || e.Transport == domain.TransportOS {
			return e.Address
		}
	}
	return ""
}
