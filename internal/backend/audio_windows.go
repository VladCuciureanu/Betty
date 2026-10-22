//go:build windows

package backend

import (
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/vladcuciureanu/betty/internal/domain"
)

// Windows connects and disconnects Bluetooth audio devices the way the Sound
// control panel does (#20): each Bluetooth audio driver's KS filter
// implements the documented KSPROPERTY_ONESHOT_RECONNECT and
// KSPROPERTY_ONESHOT_DISCONNECT, which Betty reaches from user mode through
// Core Audio's device topology and IKsControl.

var (
	ole32            = windows.NewLazySystemDLL("ole32.dll")
	coCreateInstance = ole32.NewProc("CoCreateInstance")

	clsidMMDeviceEnumerator = windows.GUID{Data1: 0xbcde0395, Data2: 0xe52f, Data3: 0x467c, Data4: [8]byte{0x8e, 0x3d, 0xc4, 0x57, 0x92, 0x91, 0x69, 0x2e}}
	iidIMMDeviceEnumerator  = windows.GUID{Data1: 0xa95664d2, Data2: 0x9614, Data3: 0x4f35, Data4: [8]byte{0xa7, 0x46, 0xde, 0x8d, 0xb6, 0x36, 0x17, 0xe6}}
	iidIDeviceTopology      = windows.GUID{Data1: 0x2a07407e, Data2: 0x6497, Data3: 0x4a18, Data4: [8]byte{0x97, 0x87, 0x32, 0xf7, 0x9b, 0xd0, 0xd9, 0x8f}}
	iidIKsControl           = windows.GUID{Data1: 0x28f54685, Data2: 0x06fd, Data3: 0x11d2, Data4: [8]byte{0xb2, 0x7a, 0x00, 0xa0, 0xc9, 0x22, 0x31, 0x96}}
	ksPropSetIDBtAudio      = windows.GUID{Data1: 0x7fa06c40, Data2: 0xb8f6, Data3: 0x4c7e, Data4: [8]byte{0x85, 0x56, 0xe8, 0xc3, 0x3a, 0x12, 0xe5, 0x4d}}
)

const (
	clsctxAll          = 0x17
	eAll               = 2
	deviceStateMaskAll = 0xf
	ksPropertyTypeGet  = 1

	ksPropertyOneshotReconnect  = 0
	ksPropertyOneshotDisconnect = 1

	// Vtable slots, after IUnknown's QueryInterface, AddRef and Release.
	enumeratorEnumAudioEndpoints = 3
	enumeratorGetDevice          = 5
	collectionGetCount           = 3
	collectionItem               = 4
	deviceActivate               = 3
	topologyGetConnector         = 4
	connectorGetDeviceIDTo       = 10
	ksControlKsProperty          = 3
)

// comObject is a COM interface pointer: its first word points to the vtable.
type comObject struct{ p unsafe.Pointer }

func (o comObject) call(slot int, args ...uintptr) uint32 {
	vtable := *(*unsafe.Pointer)(o.p)
	method := *(*uintptr)(unsafe.Add(vtable, slot*int(unsafe.Sizeof(uintptr(0)))))
	result, _, _ := syscall.SyscallN(method, append([]uintptr{uintptr(o.p)}, args...)...)
	return uint32(result)
}

func (o comObject) release() {
	if o.p != nil {
		o.call(2)
	}
}

type ksProperty struct {
	Set   windows.GUID
	ID    uint32
	Flags uint32
}

// btAudioFilterPath finds the Bluetooth address in a Bluetooth audio KS
// filter's device ID, such as
// `{2}.\\?\bthhfenum#...#8&1a2b3c&0&D8373BC1331A_C00000000#{...}`.
var btAudioFilterPath = regexp.MustCompile(`(?i)^\{2\}\.\\\\\?\\(?:bthhfenum|bthenum)#.*&([0-9a-f]{12})_c[0-9a-f]+#`)

// audioFilterAddress returns the "AA:BB:CC:DD:EE:FF" address of a Bluetooth
// audio KS filter's device ID, and "" for any other filter.
func audioFilterAddress(id string) string {
	match := btAudioFilterPath.FindStringSubmatch(id)
	if match == nil {
		return ""
	}
	hex := strings.ToUpper(match[1])
	parts := make([]string, 6)
	for i := range parts {
		parts[i] = hex[2*i : 2*i+2]
	}
	return strings.Join(parts, ":")
}

// setAudioConnection sends the one-shot reconnect or disconnect to every
// Bluetooth audio filter of the device at address. It fails with
// unsupported when the device has no Bluetooth audio endpoint.
func setAudioConnection(address string, connect bool) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err != nil {
		return domain.NewError(domain.CodeUnavailable, "Windows audio is unavailable: %v", err)
	}
	defer windows.CoUninitialize()

	var enumerator comObject
	if hr, _, _ := coCreateInstance.Call(uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, clsctxAll, uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)), uintptr(unsafe.Pointer(&enumerator.p))); uint32(hr) != 0 {
		return audioError("open the audio device list", uint32(hr))
	}
	defer enumerator.release()
	filters, err := audioFilters(enumerator)
	if err != nil {
		return err
	}
	id := uint32(ksPropertyOneshotDisconnect)
	operation := "disconnect"
	if connect {
		id, operation = ksPropertyOneshotReconnect, "connect"
	}
	sent, failure := 0, uint32(0)
	for _, filter := range filters {
		if audioFilterAddress(filter) != address {
			continue
		}
		if hr := oneshot(enumerator, filter, id); hr != 0 {
			failure = hr
			continue
		}
		sent++
	}
	if sent > 0 {
		return nil
	}
	if failure != 0 {
		return audioError(operation, failure)
	}
	return domain.NewError(domain.CodeUnsupported, "Windows can %s only Bluetooth audio devices, and this device has no audio endpoint; use Windows Settings", operation)
}

// audioFilters lists the distinct KS filter device IDs behind every audio
// endpoint, present or not.
func audioFilters(enumerator comObject) ([]string, error) {
	var collection comObject
	if hr := enumerator.call(enumeratorEnumAudioEndpoints, eAll, deviceStateMaskAll, uintptr(unsafe.Pointer(&collection.p))); hr != 0 {
		return nil, audioError("list audio endpoints", hr)
	}
	defer collection.release()
	var count uint32
	if hr := collection.call(collectionGetCount, uintptr(unsafe.Pointer(&count))); hr != 0 {
		return nil, audioError("count audio endpoints", hr)
	}
	seen := map[string]bool{}
	var filters []string
	for i := uint32(0); i < count; i++ {
		var device comObject
		if collection.call(collectionItem, uintptr(i), uintptr(unsafe.Pointer(&device.p))) != 0 {
			continue
		}
		filter := endpointFilter(device)
		device.release()
		if filter != "" && !seen[filter] {
			seen[filter] = true
			filters = append(filters, filter)
		}
	}
	return filters, nil
}

// endpointFilter returns the device ID of the KS filter an endpoint's
// connector is attached to, or "" when it has none.
func endpointFilter(device comObject) string {
	var topology comObject
	if device.call(deviceActivate, uintptr(unsafe.Pointer(&iidIDeviceTopology)), clsctxAll, 0, uintptr(unsafe.Pointer(&topology.p))) != 0 {
		return ""
	}
	defer topology.release()
	var connector comObject
	if topology.call(topologyGetConnector, 0, uintptr(unsafe.Pointer(&connector.p))) != 0 {
		return ""
	}
	defer connector.release()
	var id *uint16
	if connector.call(connectorGetDeviceIDTo, uintptr(unsafe.Pointer(&id))) != 0 || id == nil {
		return ""
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(id))
	return windows.UTF16PtrToString(id)
}

// oneshot sends a KSPROPSETID_BtAudio one-shot property to a filter and
// returns its HRESULT.
func oneshot(enumerator comObject, filter string, id uint32) uint32 {
	name, err := windows.UTF16PtrFromString(filter)
	if err != nil {
		return uint32(windows.E_INVALIDARG)
	}
	var device comObject
	if hr := enumerator.call(enumeratorGetDevice, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&device.p))); hr != 0 {
		return hr
	}
	defer device.release()
	var control comObject
	if hr := device.call(deviceActivate, uintptr(unsafe.Pointer(&iidIKsControl)), clsctxAll, 0, uintptr(unsafe.Pointer(&control.p))); hr != 0 {
		return hr
	}
	defer control.release()
	property := ksProperty{Set: ksPropSetIDBtAudio, ID: id, Flags: ksPropertyTypeGet}
	var returned uint32
	return control.call(ksControlKsProperty, uintptr(unsafe.Pointer(&property)), unsafe.Sizeof(property), 0, 0, uintptr(unsafe.Pointer(&returned)))
}

func audioError(operation string, hr uint32) error {
	if hr == uint32(windows.E_ACCESSDENIED) {
		return domain.NewError(domain.CodePermission, "Windows denied the audio %s (HRESULT 0x%08x)", operation, hr)
	}
	return domain.NewError(domain.CodeUnavailable, "Windows audio %s failed (HRESULT 0x%08x)", operation, hr)
}
