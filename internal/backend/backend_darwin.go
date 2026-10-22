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

func (*darwinBackend) Capabilities() domain.Capabilities {
	return domain.Capabilities{Scan: domain.Supported, Pair: domain.Supported, Unpair: domain.Supported, Connect: domain.Supported, Disconnect: domain.Supported, GATT: domain.Supported}
}

// Scan runs a Classic device inquiry until ctx ends and adds the paired
// devices. Nearby BLE devices come from the BLE client.
func (b *darwinBackend) Scan(ctx context.Context) ([]domain.Device, error) {
	timeout := timeoutMillis(ctx)
	if timeout == 0 {
		timeout = 10000 // an inquiry's usual length
	}
	cancel := new(int32)
	type result struct {
		devices []domain.Device
		status  int
	}
	done := make(chan result, 1)
	go func() {
		var count, status C.int
		devices := C.betty_inquiry(C.int(timeout), (*C.int)(unsafe.Pointer(cancel)), &count, &status)
		defer C.betty_free_devices(devices, count)
		done <- result{darwinDevices(devices, count), int(status)}
	}()
	var found result
	select {
	case <-ctx.Done():
		// The inquiry stops within one run-loop pass; what it found counts.
		atomic.StoreInt32(cancel, 1)
		found = <-done
	case found = <-done:
	}
	if found.status != 0 && len(found.devices) == 0 {
		return nil, darwinError("scan", uint32(found.status))
	}
	paired, err := b.List(ctx)
	if err != nil {
		return found.devices, nil
	}
	seen := map[string]bool{}
	for _, d := range found.devices {
		seen[d.ID] = true
	}
	for _, d := range paired {
		if !seen[d.ID] {
			found.devices = append(found.devices, d)
		}
	}
	return found.devices, nil
}

func (*darwinBackend) List(context.Context) ([]domain.Device, error) {
	var count C.int
	devices := C.betty_copy_paired_devices(&count)
	defer C.betty_free_devices(devices, count)
	return darwinDevices(devices, count), nil
}

func darwinDevices(devices *C.betty_device, count C.int) []domain.Device {
	if devices == nil || count == 0 {
		return nil
	}
	out := make([]domain.Device, 0, int(count))
	for _, d := range unsafe.Slice(devices, int(count)) {
		address := C.GoString(d.address)
		out = append(out, domain.Device{ID: "macos:" + address, Name: C.GoString(d.name), Paired: d.paired != 0, Connected: d.connected != 0, Endpoints: []domain.Endpoint{{ID: address, Address: address, Transport: domain.TransportClassic}}})
	}
	return out
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

// Unpair uses a private IOBluetooth API, because macOS has no public one
// (#14).
func (*darwinBackend) Unpair(ctx context.Context, d domain.Device) error {
	return darwinCall(ctx, d, "unpair", nil)
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
		case "unpair":
			code = int(C.betty_unpair(c))
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
	switch operation {
	case "pair":
		state = func() bool { return C.betty_is_paired(c) != 0 }
	case "unpair":
		state, desired = func() bool { return C.betty_is_paired(c) != 0 }, false
	case "disconnect":
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
