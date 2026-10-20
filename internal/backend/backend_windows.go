//go:build windows

package backend

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/vladcuciureanu/betty/internal/domain"
)

var (
	bthprops             = syscall.NewLazyDLL("bthprops.cpl")
	findFirstDevice      = bthprops.NewProc("BluetoothFindFirstDevice")
	findNextDevice       = bthprops.NewProc("BluetoothFindNextDevice")
	findDeviceClose      = bthprops.NewProc("BluetoothFindDeviceClose")
	authenticateDeviceEx = bthprops.NewProc("BluetoothAuthenticateDeviceEx")
	removeDevice         = bthprops.NewProc("BluetoothRemoveDevice")
	bthpropsOnce         sync.Once
	bthpropsErr          error
)

const (
	errorNoMoreItems        syscall.Errno = 259
	errorDeviceNotConnected syscall.Errno = 1167
	errorNotFound           syscall.Errno = 1168
	errorCancelled          syscall.Errno = 1223
	errorNotAuthenticated   syscall.Errno = 1244
	errorTimeout            syscall.Errno = 1460
	errorNotSupported       syscall.Errno = 50
	errorInvalidParameter   syscall.Errno = 87
	errorServiceNotActive   syscall.Errno = 1062
)

// loadBthprops resolves every bthprops.cpl entry point up front. A LazyProc
// panics on Call when its DLL or symbol is missing, for example on Windows
// editions without the Bluetooth stack.
func loadBthprops() error {
	bthpropsOnce.Do(func() {
		for _, proc := range []*syscall.LazyProc{findFirstDevice, findNextDevice, findDeviceClose, authenticateDeviceEx, removeDevice} {
			if err := proc.Find(); err != nil {
				bthpropsErr = domain.NewError(domain.CodeUnavailable, "Windows Bluetooth API is unavailable: %v", err)
				return
			}
		}
	})
	return bthpropsErr
}

type systemTime struct{ Year, Month, DayOfWeek, Day, Hour, Minute, Second, Milliseconds uint16 }
type bluetoothDeviceInfo struct {
	Size                                 uint32
	Address                              uint64
	ClassOfDevice                        uint32
	Connected, Remembered, Authenticated int32
	LastSeen, LastUsed                   systemTime
	Name                                 [248]uint16
}
type deviceSearchParams struct {
	Size                                                                                uint32
	ReturnAuthenticated, ReturnRemembered, ReturnUnknown, ReturnConnected, IssueInquiry int32
	TimeoutMultiplier                                                                   uint8
	// Natural alignment pads before RadioHandle exactly as the C struct
	// does: 3 bytes on 32-bit Windows, 7 on 64-bit.
	RadioHandle uintptr
}
type windowsBackend struct{}

func newPlatformBackend(PairingIO) Backend { return &windowsBackend{} }
func (*windowsBackend) Capabilities() domain.Capabilities {
	return domain.Capabilities{Scan: domain.Supported, Pair: domain.Supported, Unpair: domain.Supported, Connect: domain.Unsupported, Disconnect: domain.Unsupported, GATT: domain.Supported}
}
func (b *windowsBackend) Scan(context.Context) ([]domain.Device, error) { return b.enumerate(true) }
func (b *windowsBackend) List(context.Context) ([]domain.Device, error) { return b.enumerate(false) }
func (b *windowsBackend) Pair(ctx context.Context, d domain.Device) error {
	if err := loadBthprops(); err != nil {
		return err
	}
	info, err := windowsInfo(d)
	if err != nil {
		return err
	}
	// BluetoothAuthenticateDeviceEx returns its Win32 error code directly;
	// GetLastError is not meaningful afterwards.
	result, _, _ := authenticateDeviceEx.Call(0, 0, uintptr(unsafe.Pointer(&info)), 0, 2)
	if result != 0 {
		return windowsError("pair", syscall.Errno(result))
	}
	return b.waitPaired(ctx, info.Address, true)
}
func (b *windowsBackend) Unpair(ctx context.Context, d domain.Device) error {
	if err := loadBthprops(); err != nil {
		return err
	}
	info, err := windowsInfo(d)
	if err != nil {
		return err
	}
	result, _, _ := removeDevice.Call(uintptr(unsafe.Pointer(&info.Address)))
	if result != 0 {
		return windowsError("unpair", syscall.Errno(result))
	}
	return b.waitPaired(ctx, info.Address, false)
}

func (b *windowsBackend) waitPaired(ctx context.Context, address uint64, desired bool) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		devices, err := b.enumerate(false)
		if err != nil {
			return err
		}
		paired := false
		formatted := formatWindowsAddress(address)
		for _, device := range devices {
			if device.ID == "windows:"+formatted {
				paired = device.Paired
				break
			}
		}
		if paired == desired {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func (*windowsBackend) Connect(context.Context, domain.Device) error {
	return domain.NewError(domain.CodeUnsupported, "Windows does not expose a reliable generic API for connecting every saved device profile; connect it from Windows Settings")
}
func (*windowsBackend) Disconnect(context.Context, domain.Device) error {
	return domain.NewError(domain.CodeUnsupported, "Windows does not expose a reliable generic API for disconnecting every saved device profile; disconnect it from Windows Settings")
}
func (*windowsBackend) enumerate(inquiry bool) ([]domain.Device, error) {
	if err := loadBthprops(); err != nil {
		return nil, err
	}
	params := deviceSearchParams{ReturnAuthenticated: 1, ReturnRemembered: 1, ReturnUnknown: bool32(inquiry), ReturnConnected: 1, IssueInquiry: bool32(inquiry), TimeoutMultiplier: 4}
	params.Size = uint32(unsafe.Sizeof(params))
	var info bluetoothDeviceInfo
	info.Size = uint32(unsafe.Sizeof(info))
	handle, _, callErr := findFirstDevice.Call(uintptr(unsafe.Pointer(&params)), uintptr(unsafe.Pointer(&info)))
	if handle == 0 {
		// BluetoothFindFirstDevice reports failure only through
		// GetLastError, which LazyProc.Call captures on the calling thread.
		if errors.Is(callErr, errorNoMoreItems) {
			return nil, nil
		}
		return nil, windowsError("enumerate", callErr)
	}
	defer findDeviceClose.Call(handle)
	var out []domain.Device
	for {
		address := formatWindowsAddress(info.Address)
		name := syscall.UTF16ToString(info.Name[:])
		if name == "" {
			name = address
		}
		out = append(out, domain.Device{ID: "windows:" + address, Name: name, Paired: info.Authenticated != 0, Connected: info.Connected != 0, Endpoints: []domain.Endpoint{{ID: address, Address: address, Transport: domain.TransportOS}}})
		info.Size = uint32(unsafe.Sizeof(info))
		next, _, nextErr := findNextDevice.Call(handle, uintptr(unsafe.Pointer(&info)))
		if next == 0 {
			if !errors.Is(nextErr, errorNoMoreItems) {
				return out, windowsError("enumerate", nextErr)
			}
			break
		}
	}
	return out, nil
}
func windowsInfo(d domain.Device) (bluetoothDeviceInfo, error) {
	var info bluetoothDeviceInfo
	info.Size = uint32(unsafe.Sizeof(info))
	var address string
	for _, e := range d.Endpoints {
		if e.Address != "" {
			address = e.Address
			break
		}
	}
	if address == "" {
		return info, domain.NewError(domain.CodeUnsupported, "device has no Windows Bluetooth address")
	}
	var raw uint64
	_, err := fmt.Sscanf(address, "%02X:%02X:%02X:%02X:%02X:%02X", bytePtr(&raw, 5), bytePtr(&raw, 4), bytePtr(&raw, 3), bytePtr(&raw, 2), bytePtr(&raw, 1), bytePtr(&raw, 0))
	if err != nil {
		return info, domain.NewError(domain.CodeUsage, "invalid Bluetooth address %q", address)
	}
	info.Address = raw
	return info, nil
}
func bytePtr(raw *uint64, offset uintptr) *byte {
	return (*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(raw)) + offset))
}
func formatWindowsAddress(a uint64) string {
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", byte(a>>40), byte(a>>32), byte(a>>24), byte(a>>16), byte(a>>8), byte(a))
}
func bool32(v bool) int32 {
	if v {
		return 1
	}
	return 0
}

// windowsError classifies a Win32 error code returned by a Bluetooth API.
func windowsError(op string, err error) error {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return domain.NewError(domain.CodeUnavailable, "Windows Bluetooth %s failed: %v", op, err)
	}
	code := windowsErrorCode(op, errno)
	return domain.NewError(code, "Windows Bluetooth %s failed: %v (code %d)", op, errno, uint32(errno))
}

func windowsErrorCode(op string, errno syscall.Errno) domain.ErrorCode {
	switch errno {
	case syscall.ERROR_ACCESS_DENIED:
		return domain.CodePermission
	case errorNotFound:
		return domain.CodeNotFound
	case errorCancelled, errorNotAuthenticated:
		return domain.CodeAuthentication
	case errorTimeout:
		return domain.CodeTimeout
	case errorNotSupported:
		return domain.CodeUnsupported
	case errorInvalidParameter:
		return domain.CodeUsage
	case errorDeviceNotConnected:
		return domain.CodeDisconnected
	case errorServiceNotActive:
		return domain.CodeUnavailable
	}
	if op == "pair" {
		return domain.CodeAuthentication
	}
	return domain.CodeUnavailable
}
