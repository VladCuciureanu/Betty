//go:build windows

package l2cap

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/vladcuciureanu/betty/internal/domain"
	"github.com/vladcuciureanu/betty/internal/driver"
)

// Windows does not expose arbitrary Bluetooth Classic L2CAP channels to a
// desktop process. This host speaks the small DeviceIoControl ABI exposed by
// the optional Apple Audio Bluetooth profile driver. Battery parsing and AAP
// remain entirely in Betty's portable Apple Audio driver.
var airPodsInterfaceGUID = guid{0xb377a1a0, 0x8cb0, 0x4a5c, [8]byte{0x9b, 0xa2, 0x53, 0xbd, 0xcc, 0x4c, 0x5e, 0x21}}

// IOCTL codes from packaging/windows/driver/BettyAAP.h:
// CTL_CODE(0x8000, function, METHOD_BUFFERED, access).
const (
	ioctlConnect    = uint32(0x8000e000) // 0x800, FILE_READ_DATA|FILE_WRITE_DATA
	ioctlDisconnect = uint32(0x8000e004) // 0x801, FILE_READ_DATA|FILE_WRITE_DATA
	ioctlSend       = uint32(0x8000a008) // 0x802, FILE_WRITE_DATA
	ioctlReceive    = uint32(0x8000600c) // 0x803, FILE_READ_DATA
	ioctlGetAddress = uint32(0x80006010) // 0x804, FILE_READ_DATA

	digcfPresent         = 0x00000002
	digcfDeviceInterface = 0x00000010
	genericRead          = 0x80000000
	genericWrite         = 0x40000000
	fileShareRead        = 0x00000001
	fileShareWrite       = 0x00000002
	openExisting         = 3
)

var (
	setupAPI                         = syscall.NewLazyDLL("setupapi.dll")
	kernel32                         = syscall.NewLazyDLL("kernel32.dll")
	setupDiGetClassDevsW             = setupAPI.NewProc("SetupDiGetClassDevsW")
	setupDiEnumDeviceInterfaces      = setupAPI.NewProc("SetupDiEnumDeviceInterfaces")
	setupDiGetDeviceInterfaceDetailW = setupAPI.NewProc("SetupDiGetDeviceInterfaceDetailW")
	setupDiDestroyDeviceInfoList     = setupAPI.NewProc("SetupDiDestroyDeviceInfoList")
	createFileW                      = kernel32.NewProc("CreateFileW")
	deviceIoControl                  = kernel32.NewProc("DeviceIoControl")
	closeHandle                      = kernel32.NewProc("CloseHandle")
)

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

type deviceInterfaceData struct {
	Size      uint32
	ClassGUID guid
	Flags     uint32
	Reserved  uintptr
}

type deviceInterfaceDetailHeader struct {
	Size       uint32
	DevicePath [1]uint16
}

type windowsChannel struct {
	handle syscall.Handle
	mu     sync.Mutex
}

type windowsHost struct {
	mu       sync.Mutex
	channels map[string]*windowsChannel
	next     atomic.Uint64
}

func New() driver.Host { return &windowsHost{channels: map[string]*windowsChannel{}} }

func (h *windowsHost) OpenL2CAP(ctx context.Context, deviceID string, psm uint16) (string, error) {
	address, err := windowsAddress(deviceID)
	if err != nil {
		return "", err
	}
	paths, err := airPodsDriverPaths()
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", domain.NewError(domain.CodeUnsupported, "the Windows Apple Audio Bluetooth profile driver is not installed, or no paired Apple audio device is using it")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	handle, err := openBoundInstance(paths, address, probeDriver, closeWindowsHandle)
	if err != nil {
		return "", err
	}
	input := make([]byte, 10)
	binary.LittleEndian.PutUint64(input[:8], address)
	binary.LittleEndian.PutUint16(input[8:], psm)
	output := make([]byte, 8)
	_, err = ioctl(handle, ioctlConnect, input, output)
	if err == nil && binary.LittleEndian.Uint32(output[:4]) != 0 {
		id := "windows-" + strconv.FormatUint(h.next.Add(1), 10)
		h.mu.Lock()
		h.channels[id] = &windowsChannel{handle: handle}
		h.mu.Unlock()
		return id, nil
	}
	_ = closeWindowsHandle(handle)
	if err == nil {
		err = fmt.Errorf("profile driver rejected L2CAP connection (NTSTATUS 0x%08x)", binary.LittleEndian.Uint32(output[4:]))
	}
	return "", fmt.Errorf("connect Windows Apple Audio L2CAP PSM 0x%04x: %w", psm, err)
}

// probeDriver opens one profile driver instance and returns the address of
// the remote device it is bound to. The caller owns the returned handle.
func probeDriver(path string) (syscall.Handle, uint64, error) {
	handle, err := openDriver(path)
	if err != nil {
		return syscall.InvalidHandle, 0, err
	}
	output := make([]byte, 8)
	n, err := ioctl(handle, ioctlGetAddress, nil, output)
	if err == nil && n != len(output) {
		err = fmt.Errorf("profile driver returned %d address bytes", n)
	}
	if err != nil {
		_ = closeWindowsHandle(handle)
		return syscall.InvalidHandle, 0, err
	}
	return handle, binary.LittleEndian.Uint64(output), nil
}

// openBoundInstance returns an open handle to the profile driver instance
// bound to address. Each instance is bound to one remote device, and the
// driver rejects CONNECT for any other address.
func openBoundInstance(paths []string, address uint64, probe func(string) (syscall.Handle, uint64, error), closeHandle func(syscall.Handle) error) (syscall.Handle, error) {
	var probeErrs []error
	for _, path := range paths {
		handle, bound, err := probe(path)
		if err != nil {
			probeErrs = append(probeErrs, err)
			continue
		}
		if bound == address {
			return handle, nil
		}
		_ = closeHandle(handle)
	}
	if len(probeErrs) > 0 {
		// The matching instance may be the one that could not be opened,
		// for example because another process holds its exclusive handle.
		return syscall.InvalidHandle, fmt.Errorf("open Windows Apple Audio profile driver for %s: %w", formatAddress(address), errors.Join(probeErrs...))
	}
	return syscall.InvalidHandle, domain.NewError(domain.CodeUnsupported, "the Windows Apple Audio Bluetooth profile driver is not bound to %s; pair the device with Windows and make sure the driver is installed for it", formatAddress(address))
}

func formatAddress(address uint64) string {
	var b strings.Builder
	for i := 5; i >= 0; i-- {
		fmt.Fprintf(&b, "%02X", byte(address>>(8*i)))
		if i > 0 {
			b.WriteByte(':')
		}
	}
	return b.String()
}

func (h *windowsHost) ReadL2CAP(ctx context.Context, channelID string, max int) ([]byte, error) {
	channel, err := h.channel(channelID)
	if err != nil {
		return nil, err
	}
	timeout := uint32(5000)
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, ctx.Err()
		}
		if remaining < 5*time.Second {
			timeout = uint32(maxDuration(remaining, time.Millisecond).Milliseconds())
		}
	}
	input := make([]byte, 4)
	binary.LittleEndian.PutUint32(input, timeout)
	output := make([]byte, max)
	channel.mu.Lock()
	n, err := ioctl(channel.handle, ioctlReceive, input, output)
	channel.mu.Unlock()
	if err != nil {
		if errors.Is(err, syscall.Errno(121)) || errors.Is(err, syscall.Errno(258)) || errors.Is(err, syscall.Errno(1460)) {
			return nil, domain.NewError(domain.CodeTimeout, "timed out waiting for Apple audio data")
		}
		return nil, fmt.Errorf("receive Windows Apple Audio L2CAP data: %w", err)
	}
	return output[:n], nil
}

func (h *windowsHost) WriteL2CAP(ctx context.Context, channelID string, data []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	channel, err := h.channel(channelID)
	if err != nil {
		return 0, err
	}
	channel.mu.Lock()
	_, err = ioctl(channel.handle, ioctlSend, data, nil)
	channel.mu.Unlock()
	if err != nil {
		return 0, fmt.Errorf("send Windows Apple Audio L2CAP data: %w", err)
	}
	return len(data), nil
}

func (h *windowsHost) CloseL2CAP(_ context.Context, channelID string) error {
	h.mu.Lock()
	channel, ok := h.channels[channelID]
	if ok {
		delete(h.channels, channelID)
	}
	h.mu.Unlock()
	if !ok {
		return domain.NewError(domain.CodeNotFound, "L2CAP channel %q is not open", channelID)
	}
	channel.mu.Lock()
	_, disconnectErr := ioctl(channel.handle, ioctlDisconnect, nil, nil)
	closeErr := closeWindowsHandle(channel.handle)
	channel.mu.Unlock()
	if disconnectErr != nil {
		return disconnectErr
	}
	if closeErr != nil {
		return closeErr
	}
	return nil
}

func closeWindowsHandle(handle syscall.Handle) error {
	ok, _, callErr := closeHandle.Call(uintptr(handle))
	if ok == 0 {
		return callErr
	}
	return nil
}

func (h *windowsHost) channel(id string) (*windowsChannel, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	channel, ok := h.channels[id]
	if !ok {
		return nil, domain.NewError(domain.CodeNotFound, "L2CAP channel %q is not open", id)
	}
	return channel, nil
}

func windowsAddress(deviceID string) (uint64, error) {
	value := strings.TrimPrefix(deviceID, "windows:")
	value = strings.NewReplacer(":", "", "-", "").Replace(value)
	if len(value) != 12 {
		return 0, domain.NewError(domain.CodeUsage, "device %q has no Windows Classic address", deviceID)
	}
	address, err := strconv.ParseUint(value, 16, 48)
	if err != nil {
		return 0, domain.NewError(domain.CodeUsage, "device %q has an invalid Bluetooth address", deviceID)
	}
	return address, nil
}

func airPodsDriverPaths() ([]string, error) {
	set, _, callErr := setupDiGetClassDevsW.Call(uintptr(unsafe.Pointer(&airPodsInterfaceGUID)), 0, 0, digcfPresent|digcfDeviceInterface)
	if set == ^uintptr(0) {
		return nil, callErr
	}
	defer setupDiDestroyDeviceInfoList.Call(set)
	var paths []string
	for index := uint32(0); ; index++ {
		data := deviceInterfaceData{Size: uint32(unsafe.Sizeof(deviceInterfaceData{}))}
		ok, _, enumErr := setupDiEnumDeviceInterfaces.Call(set, 0, uintptr(unsafe.Pointer(&airPodsInterfaceGUID)), uintptr(index), uintptr(unsafe.Pointer(&data)))
		if ok == 0 {
			if enumErr == syscall.Errno(259) {
				break
			}
			return nil, enumErr
		}
		var required uint32
		setupDiGetDeviceInterfaceDetailW.Call(set, uintptr(unsafe.Pointer(&data)), 0, 0, uintptr(unsafe.Pointer(&required)), 0)
		if required == 0 {
			continue
		}
		buffer := make([]byte, required)
		header := (*deviceInterfaceDetailHeader)(unsafe.Pointer(&buffer[0]))
		header.Size = uint32(unsafe.Sizeof(deviceInterfaceDetailHeader{}))
		ok, _, detailErr := setupDiGetDeviceInterfaceDetailW.Call(set, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(required), uintptr(unsafe.Pointer(&required)), 0)
		if ok == 0 {
			return nil, detailErr
		}
		pathOffset := unsafe.Offsetof(deviceInterfaceDetailHeader{}.DevicePath)
		pathLength := (uintptr(len(buffer)) - pathOffset) / 2
		pathUnits := unsafe.Slice(&header.DevicePath[0], pathLength)
		path := syscall.UTF16ToString(pathUnits)
		paths = append(paths, path)
	}
	return paths, nil
}

func openDriver(path string) (syscall.Handle, error) {
	pointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return syscall.InvalidHandle, err
	}
	handle, _, callErr := createFileW.Call(uintptr(unsafe.Pointer(pointer)), genericRead|genericWrite, fileShareRead|fileShareWrite, 0, openExisting, 0, 0)
	if handle == ^uintptr(0) {
		return syscall.InvalidHandle, callErr
	}
	return syscall.Handle(handle), nil
}

func ioctl(handle syscall.Handle, code uint32, input, output []byte) (int, error) {
	var inputPointer, outputPointer uintptr
	if len(input) > 0 {
		inputPointer = uintptr(unsafe.Pointer(&input[0]))
	}
	if len(output) > 0 {
		outputPointer = uintptr(unsafe.Pointer(&output[0]))
	}
	var returned uint32
	ok, _, callErr := deviceIoControl.Call(uintptr(handle), uintptr(code), inputPointer, uintptr(len(input)), outputPointer, uintptr(len(output)), uintptr(unsafe.Pointer(&returned)), 0)
	if ok == 0 {
		return 0, callErr
	}
	if returned > uint32(len(output)) {
		return 0, fmt.Errorf("Windows profile driver returned invalid byte count %d", returned)
	}
	return int(returned), nil
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
