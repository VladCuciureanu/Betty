//go:build windows

package backend

import (
	"context"
	"fmt"
	"syscall"
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
)

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
	_                                                                                   [7]byte
	RadioHandle                                                                         uintptr
}
type windowsBackend struct{}

func newPlatformBackend(PairingIO) Backend { return &windowsBackend{} }
func (*windowsBackend) Capabilities() domain.Capabilities {
	return domain.Capabilities{Scan: domain.Supported, Pair: domain.Supported, Unpair: domain.Supported, Connect: domain.Unsupported, Disconnect: domain.Unsupported, GATT: domain.Supported}
}
func (b *windowsBackend) Scan(context.Context) ([]domain.Device, error) { return b.enumerate(true) }
func (b *windowsBackend) List(context.Context) ([]domain.Device, error) { return b.enumerate(false) }
func (*windowsBackend) Pair(_ context.Context, d domain.Device) error {
	info, err := windowsInfo(d)
	if err != nil {
		return err
	}
	result, _, callErr := authenticateDeviceEx.Call(0, 0, uintptr(unsafe.Pointer(&info)), 0, 2)
	if result != 0 {
		return windowsError("pair", result, callErr)
	}
	return nil
}
func (*windowsBackend) Unpair(_ context.Context, d domain.Device) error {
	info, err := windowsInfo(d)
	if err != nil {
		return err
	}
	result, _, callErr := removeDevice.Call(uintptr(unsafe.Pointer(&info.Address)))
	if result != 0 {
		return windowsError("unpair", result, callErr)
	}
	return nil
}
func (*windowsBackend) Connect(context.Context, domain.Device) error {
	return domain.NewError(domain.CodeUnsupported, "Windows does not expose a reliable generic API for connecting every saved device profile; connect it from Windows Settings")
}
func (*windowsBackend) Disconnect(context.Context, domain.Device) error {
	return domain.NewError(domain.CodeUnsupported, "Windows does not expose a reliable generic API for disconnecting every saved device profile; disconnect it from Windows Settings")
}
func (*windowsBackend) enumerate(inquiry bool) ([]domain.Device, error) {
	params := deviceSearchParams{ReturnAuthenticated: 1, ReturnRemembered: 1, ReturnUnknown: bool32(inquiry), ReturnConnected: 1, IssueInquiry: bool32(inquiry), TimeoutMultiplier: 4}
	params.Size = uint32(unsafe.Sizeof(params))
	var info bluetoothDeviceInfo
	info.Size = uint32(unsafe.Sizeof(info))
	handle, _, callErr := findFirstDevice.Call(uintptr(unsafe.Pointer(&params)), uintptr(unsafe.Pointer(&info)))
	if handle == 0 {
		errno, ok := callErr.(syscall.Errno)
		if ok && errno == 259 {
			return nil, nil
		}
		return nil, windowsError("enumerate", handle, callErr)
	}
	defer findDeviceClose.Call(handle)
	var out []domain.Device
	for {
		address := formatWindowsAddress(info.Address)
		name := syscall.UTF16ToString(info.Name[:])
		if name == "" {
			name = address
		}
		out = append(out, domain.Device{ID: "windows:" + address, Name: name, Paired: info.Authenticated != 0 || info.Remembered != 0, Connected: info.Connected != 0, Endpoints: []domain.Endpoint{{ID: address, Address: address, Transport: domain.TransportOS}}})
		info.Size = uint32(unsafe.Sizeof(info))
		next, _, _ := findNextDevice.Call(handle, uintptr(unsafe.Pointer(&info)))
		if next == 0 {
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
func windowsError(op string, code uintptr, err error) error {
	return fmt.Errorf("Windows Bluetooth %s failed (code %d): %v", op, code, err)
}
