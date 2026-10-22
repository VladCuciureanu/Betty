//go:build windows

// Package hid finds the HID collections of Bluetooth devices and opens them
// for drivers' hid host calls.
package hid

import (
	"regexp"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	hidDLL                = windows.NewLazySystemDLL("hid.dll")
	hidDGetPreparsedData  = hidDLL.NewProc("HidD_GetPreparsedData")
	hidDFreePreparsedData = hidDLL.NewProc("HidD_FreePreparsedData")
	hidDGetAttributes     = hidDLL.NewProc("HidD_GetAttributes")
	hidPGetCaps           = hidDLL.NewProc("HidP_GetCaps")

	guidDevinterfaceHID = windows.GUID{Data1: 0x4d1e55b2, Data2: 0xf16f, Data3: 0x11cf, Data4: [8]byte{0x88, 0xcb, 0x00, 0x11, 0x11, 0x00, 0x00, 0x30}}
	devpkeyDeviceParent = windows.DEVPROPKEY{FmtID: windows.DEVPROPGUID{Data1: 0x4340a6c5, Data2: 0x93fa, Data3: 0x4706, Data4: [8]byte{0x97, 0x2c, 0x7b, 0x64, 0x80, 0x08, 0xa5, 0xa7}}, PID: 8}
)

// StatusSuccess is HIDP_STATUS_SUCCESS.
const StatusSuccess = 0x00110000

// Caps is HIDP_CAPS: a collection's top-level usage, report lengths and how
// many controls of each kind it has.
type Caps struct {
	Usage, UsagePage                                uint16
	InputReportByteLength, OutputReportByteLength   uint16
	FeatureReportByteLength                         uint16
	Reserved                                        [17]uint16
	NumberLinkCollectionNodes                       uint16
	NumberInputButtonCaps, NumberInputValueCaps     uint16
	NumberInputDataIndices                          uint16
	NumberOutputButtonCaps, NumberOutputValueCaps   uint16
	NumberOutputDataIndices                         uint16
	NumberFeatureButtonCaps, NumberFeatureValueCaps uint16
	NumberFeatureDataIndices                        uint16
}

// PreparsedData returns a collection's parsed report descriptor, for the
// HidP functions, and the function that frees it.
func PreparsedData(handle windows.Handle) (uintptr, func(), bool) {
	var preparsed uintptr
	if ok, _, _ := hidDGetPreparsedData.Call(uintptr(handle), uintptr(unsafe.Pointer(&preparsed))); ok == 0 {
		return 0, nil, false
	}
	return preparsed, func() { hidDFreePreparsedData.Call(preparsed) }, true
}

// GetCaps reads the capabilities of a collection from its preparsed data.
func GetCaps(preparsed uintptr) (Caps, bool) {
	var caps Caps
	status, _, _ := hidPGetCaps.Call(preparsed, uintptr(unsafe.Pointer(&caps)))
	return caps, uint32(status) == StatusSuccess
}

type attributes struct {
	Size                         uint32
	VendorID, ProductID, Version uint16
}

// Collection is one HID collection of a Bluetooth device.
type Collection struct {
	// Address is the device's address as 12 lowercase hex digits.
	Address string
	Path    string
}

// bluetoothParent finds the Bluetooth address in a HID collection's parent
// device instance ID, such as
// `BTHENUM\{00001124-0000-1000-8000-00805F9B34FB}_VID&0001004C_PID&029C\A&AE70F6&0&B0BE83F34CD4_C00000000`.
var bluetoothParent = regexp.MustCompile(`(?i)^(?:bthenum|bthledevice)\\.*&([0-9a-f]{12})(?:_c[0-9a-f]+)?$`)

func parentAddress(parent string) string {
	match := bluetoothParent.FindStringSubmatch(parent)
	if match == nil {
		return ""
	}
	return strings.ToLower(match[1])
}

// Collections lists the present HID collections of Bluetooth devices.
func Collections() ([]Collection, error) {
	devices, err := windows.SetupDiGetClassDevsEx(&guidDevinterfaceHID, "", 0, windows.DIGCF_PRESENT|windows.DIGCF_DEVICEINTERFACE, 0, "")
	if err != nil {
		return nil, err
	}
	defer devices.Close()
	var collections []Collection
	for i := 0; ; i++ {
		info, err := windows.SetupDiEnumDeviceInfo(devices, i)
		if err == windows.ERROR_NO_MORE_ITEMS {
			return collections, nil
		}
		if err != nil {
			continue
		}
		parent, err := windows.SetupDiGetDeviceProperty(devices, info, &devpkeyDeviceParent)
		if err != nil {
			continue
		}
		parentID, _ := parent.(string)
		address := parentAddress(parentID)
		if address == "" {
			continue
		}
		instance, err := windows.SetupDiGetDeviceInstanceId(devices, info)
		if err != nil {
			continue
		}
		paths, err := windows.CM_Get_Device_Interface_List(instance, &guidDevinterfaceHID, windows.CM_GET_DEVICE_INTERFACE_LIST_PRESENT)
		if err != nil {
			continue
		}
		for _, path := range paths {
			collections = append(collections, Collection{address, path})
		}
	}
}
