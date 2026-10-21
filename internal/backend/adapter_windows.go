//go:build windows

package backend

import (
	"context"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	"github.com/vladcuciureanu/betty/internal/domain"
)

var (
	findFirstRadio  = bthprops.NewProc("BluetoothFindFirstRadio")
	findNextRadio   = bthprops.NewProc("BluetoothFindNextRadio")
	findRadioClose  = bthprops.NewProc("BluetoothFindRadioClose")
	getRadioInfo    = bthprops.NewProc("BluetoothGetRadioInfo")
	isDiscoverable  = bthprops.NewProc("BluetoothIsDiscoverable")
	isConnectable   = bthprops.NewProc("BluetoothIsConnectable")
	enableDiscovery = bthprops.NewProc("BluetoothEnableDiscovery")
	closeHandle     = syscall.NewLazyDLL("kernel32.dll").NewProc("CloseHandle")
	radioProcsOnce  sync.Once
	radioProcsErr   error
)

// bluetoothRadioInfo mirrors BLUETOOTH_RADIO_INFO. Like BLUETOOTH_DEVICE_INFO,
// its address is an 8-byte-aligned ULONGLONG at offset 8 on every
// architecture.
type bluetoothRadioInfo struct {
	Size          uint32
	_             uint32
	Address       uint64
	Name          [248]uint16
	ClassOfDevice uint32
	LMPSubversion uint16
	Manufacturer  uint16
}

type findRadioParams struct{ Size uint32 }

func loadRadioProcs() error {
	if err := loadBthprops(); err != nil {
		return err
	}
	radioProcsOnce.Do(func() {
		for _, proc := range []*syscall.LazyProc{findFirstRadio, findNextRadio, findRadioClose, getRadioInfo, isDiscoverable, isConnectable, enableDiscovery, closeHandle} {
			if err := proc.Find(); err != nil {
				radioProcsErr = domain.NewError(domain.CodeUnavailable, "Windows Bluetooth radio API is unavailable: %v", err)
				return
			}
		}
	})
	return radioProcsErr
}

// radios calls f with each local radio's handle. Windows uses only the first
// for Bluetooth, so that one is the default.
func radios(f func(handle uintptr, index int) error) error {
	if err := loadRadioProcs(); err != nil {
		return err
	}
	params := findRadioParams{Size: uint32(unsafe.Sizeof(findRadioParams{}))}
	var handle uintptr
	find, _, callErr := findFirstRadio.Call(uintptr(unsafe.Pointer(&params)), uintptr(unsafe.Pointer(&handle)))
	if find == 0 {
		if errno, ok := callErr.(syscall.Errno); ok && errno == errorNoMoreItems {
			return domain.NewError(domain.CodeUnavailable, "no Bluetooth radio found")
		}
		return windowsError("find radio", callErr)
	}
	defer findRadioClose.Call(find)
	for index := 0; ; index++ {
		err := f(handle, index)
		closeHandle.Call(handle)
		if err != nil {
			return err
		}
		if ok, _, _ := findNextRadio.Call(find, uintptr(unsafe.Pointer(&handle))); ok == 0 {
			return nil
		}
	}
}

func radioAdapter(handle uintptr, index int) (domain.Adapter, error) {
	info := bluetoothRadioInfo{Size: uint32(unsafe.Sizeof(bluetoothRadioInfo{}))}
	// BluetoothGetRadioInfo returns its Win32 error code directly.
	if result, _, _ := getRadioInfo.Call(handle, uintptr(unsafe.Pointer(&info))); result != 0 {
		return domain.Adapter{}, windowsError("read radio", syscall.Errno(result))
	}
	discoverable, _, _ := isDiscoverable.Call(handle)
	connectable, _, _ := isConnectable.Call(handle)
	isDiscoverableNow, isPairable := discoverable != 0, connectable != 0
	return domain.Adapter{
		ID:           "radio" + strconv.Itoa(index),
		Name:         syscall.UTF16ToString(info.Name[:]),
		Address:      formatWindowsAddress(info.Address),
		Discoverable: &isDiscoverableNow,
		Pairable:     &isPairable,
		Default:      index == 0,
	}, nil
}

func (*windowsBackend) Adapters(context.Context) ([]domain.Adapter, error) {
	var adapters []domain.Adapter
	err := radios(func(handle uintptr, index int) error {
		adapter, err := radioAdapter(handle, index)
		if err == nil {
			adapters = append(adapters, adapter)
		}
		return err
	})
	return adapters, err
}

func (*windowsBackend) SetAdapter(_ context.Context, change domain.AdapterChange) (domain.Adapter, error) {
	if change.Powered != nil {
		return domain.Adapter{}, domain.NewError(domain.CodeUnsupported, "turning the Bluetooth radio on or off isn't implemented on Windows yet")
	}
	if change.Name != nil {
		return domain.Adapter{}, domain.NewError(domain.CodeUnsupported, "the Windows Bluetooth name is the computer's name; rename the computer to change it")
	}
	var adapter domain.Adapter
	err := radios(func(handle uintptr, index int) error {
		if index != 0 {
			return nil
		}
		if change.Discoverable != nil {
			// The radio handle is ignored for a single radio; it must also be
			// connectable to be made discoverable.
			if ok, _, callErr := enableDiscovery.Call(handle, uintptr(bool32(*change.Discoverable))); ok == 0 {
				return windowsError("change discoverability", callErr)
			}
		}
		var err error
		adapter, err = radioAdapter(handle, index)
		return err
	})
	return adapter, err
}
