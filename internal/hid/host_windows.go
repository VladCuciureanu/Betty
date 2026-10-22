//go:build windows

package hid

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	protocol "github.com/vladcuciureanu/betty/driver"
	"github.com/vladcuciureanu/betty/internal/domain"
)

// ioctlHIDGetFeature is IOCTL_HID_GET_FEATURE.
const ioctlHIDGetFeature = 0x000b0192

// Host serves drivers' hid host calls. Input reports are read with
// overlapped I/O, which Windows cancels at the deadline. Feature reports
// are requested synchronously: the HID driver neither cancels them nor lets
// a process exit while one is pending, so a device that doesn't answer
// holds the call, and bt, for about five seconds.
type Host struct {
	mu      sync.Mutex
	handles map[string]*openCollection
	next    int
}

type openCollection struct {
	handle        windows.Handle
	inputLength   int
	featureLength int
}

func New() *Host { return &Host{handles: map[string]*openCollection{}} }

func (h *Host) OpenHID(_ context.Context, address string, usagePage, usage uint16) (protocol.HIDCollection, error) {
	want := strings.ToLower(strings.NewReplacer(":", "", "-", "").Replace(address))
	collections, err := Collections()
	if err != nil {
		return protocol.HIDCollection{}, domain.NewError(domain.CodeUnavailable, "list HID collections: %v", err)
	}
	denied := false
	for _, collection := range collections {
		if collection.Address != want {
			continue
		}
		name, err := windows.UTF16PtrFromString(collection.Path)
		if err != nil {
			continue
		}
		handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
		if err != nil {
			// Windows keeps keyboard and mouse collections to itself.
			denied = denied || errors.Is(err, windows.ERROR_ACCESS_DENIED)
			continue
		}
		open, vendor, product, ok := describe(handle, usagePage, usage)
		if !ok {
			windows.CloseHandle(handle)
			continue
		}
		h.mu.Lock()
		h.next++
		id := "hid-" + strconv.Itoa(h.next)
		h.handles[id] = open
		h.mu.Unlock()
		return protocol.HIDCollection{HandleID: id, VendorID: vendor, ProductID: product}, nil
	}
	if denied {
		return protocol.HIDCollection{}, domain.NewError(domain.CodePermission, "Windows keeps this device's HID collection to itself")
	}
	return protocol.HIDCollection{}, domain.NewError(domain.CodeUnsupported, "the device has no matching HID collection; is it connected?")
}

// describe checks an open collection's top-level usage and reads its
// report lengths and IDs.
func describe(handle windows.Handle, usagePage, usage uint16) (*openCollection, uint16, uint16, bool) {
	preparsed, free, ok := PreparsedData(handle)
	if !ok {
		return nil, 0, 0, false
	}
	defer free()
	caps, ok := GetCaps(preparsed)
	if !ok || (usagePage != 0 && (caps.UsagePage != usagePage || caps.Usage != usage)) {
		return nil, 0, 0, false
	}
	attributes := attributes{Size: uint32(unsafe.Sizeof(attributes{}))}
	if ok, _, _ := hidDGetAttributes.Call(uintptr(handle), uintptr(unsafe.Pointer(&attributes))); ok == 0 {
		return nil, 0, 0, false
	}
	return &openCollection{handle: handle, inputLength: int(caps.InputReportByteLength), featureLength: int(caps.FeatureReportByteLength)}, attributes.VendorID, attributes.ProductID, true
}

func (h *Host) collection(id string) (*openCollection, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	open, ok := h.handles[id]
	if !ok {
		return nil, domain.NewError(domain.CodeUsage, "unknown HID handle %q", id)
	}
	return open, nil
}

func (h *Host) ReadHID(ctx context.Context, id string) ([]byte, error) {
	open, err := h.collection(id)
	if err != nil {
		return nil, err
	}
	if open.inputLength == 0 {
		return nil, domain.NewError(domain.CodeUnsupported, "the HID collection has no input reports")
	}
	report := make([]byte, open.inputLength)
	n, err := overlapped(ctx, open.handle, func(o *windows.Overlapped) error {
		var n uint32
		return windows.ReadFile(open.handle, report, &n, o)
	})
	if err != nil {
		return nil, err
	}
	return report[:n], nil
}

func (h *Host) GetHIDFeature(ctx context.Context, id string, reportID uint8) ([]byte, error) {
	open, err := h.collection(id)
	if err != nil {
		return nil, err
	}
	if open.featureLength == 0 {
		return nil, domain.NewError(domain.CodeUnsupported, "the HID collection has no feature reports")
	}
	report := make([]byte, open.featureLength)
	report[0] = reportID
	n, err := overlapped(ctx, open.handle, func(o *windows.Overlapped) error {
		var n uint32
		return windows.DeviceIoControl(open.handle, ioctlHIDGetFeature, nil, 0, &report[0], uint32(len(report)), &n, o)
	})
	if err != nil {
		return nil, err
	}
	return report[:n], nil
}

func (h *Host) CloseHID(_ context.Context, id string) error {
	h.mu.Lock()
	open, ok := h.handles[id]
	delete(h.handles, id)
	h.mu.Unlock()
	if !ok {
		return domain.NewError(domain.CodeUsage, "unknown HID handle %q", id)
	}
	return windows.CloseHandle(open.handle)
}

// overlapped starts an operation on an overlapped handle and waits for it
// until ctx ends, then cancels it.
func overlapped(ctx context.Context, handle windows.Handle, start func(*windows.Overlapped) error) (uint32, error) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, domain.NewError(domain.CodeUnavailable, "create event: %v", err)
	}
	defer windows.CloseHandle(event)
	o := windows.Overlapped{HEvent: event}
	if err := start(&o); err != nil && err != windows.ERROR_IO_PENDING {
		return 0, hidError(err)
	}
	for {
		if wait, _ := windows.WaitForSingleObject(event, 50); wait == windows.WAIT_OBJECT_0 {
			break
		}
		if ctx.Err() != nil {
			windows.CancelIoEx(handle, &o)
			var n uint32
			windows.GetOverlappedResult(handle, &o, &n, true)
			return 0, domain.NewError(domain.CodeTimeout, "the HID device sent no report in time")
		}
	}
	var n uint32
	if err := windows.GetOverlappedResult(handle, &o, &n, true); err != nil {
		return 0, hidError(err)
	}
	return n, nil
}

func hidError(err error) error {
	switch {
	case errors.Is(err, windows.ERROR_DEVICE_NOT_CONNECTED), errors.Is(err, windows.ERROR_GEN_FAILURE):
		return domain.NewError(domain.CodeDisconnected, "the HID device is not responding: %v", err)
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return domain.NewError(domain.CodePermission, "Windows denied HID access: %v", err)
	}
	return domain.NewError(domain.CodeUnavailable, "HID request failed: %v", err)
}
