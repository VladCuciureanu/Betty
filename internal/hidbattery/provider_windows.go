//go:build windows

package hidbattery

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows publishes no battery property for Bluetooth HID devices, so Betty
// reads the standard battery usages from each Bluetooth HID collection, as
// macOS's IOHID path does.

var (
	hid                   = windows.NewLazySystemDLL("hid.dll")
	hidDGetPreparsedData  = hid.NewProc("HidD_GetPreparsedData")
	hidDFreePreparsedData = hid.NewProc("HidD_FreePreparsedData")
	hidDGetInputReport    = hid.NewProc("HidD_GetInputReport")
	hidDGetFeature        = hid.NewProc("HidD_GetFeature")
	hidPGetCaps           = hid.NewProc("HidP_GetCaps")
	hidPGetValueCaps      = hid.NewProc("HidP_GetValueCaps")
	hidPGetUsageValue     = hid.NewProc("HidP_GetUsageValue")

	guidDevinterfaceHID = windows.GUID{Data1: 0x4d1e55b2, Data2: 0xf16f, Data3: 0x11cf, Data4: [8]byte{0x88, 0xcb, 0x00, 0x11, 0x11, 0x00, 0x00, 0x30}}
	devpkeyDeviceParent = windows.DEVPROPKEY{FmtID: windows.DEVPROPGUID{Data1: 0x4340a6c5, Data2: 0x93fa, Data3: 0x4706, Data4: [8]byte{0x97, 0x2c, 0x7b, 0x64, 0x80, 0x08, 0xa5, 0xa7}}, PID: 8}
)

const (
	hidpInput         = 0
	hidpFeature       = 2
	hidpStatusSuccess = 0x00110000

	usagePageGenericDeviceControls = 0x06
	usageBatteryStrength           = 0x20
	usagePageBatterySystem         = 0x85
	usageAbsoluteStateOfCharge     = 0x65
)

// batteryUsages are the usages Betty reads, in order. Battery Strength is
// scaled over its logical range. AbsoluteStateOfCharge is a percentage by
// definition, and taken as one: Apple's Magic Keyboard reports it in input
// report 0x90 with a logical range of 0-255 but values of 0-100, which
// Linux's hid-apple corrects in the descriptor for the same reason.
var batteryUsages = []struct {
	page, usage uint16
	percent     bool
}{
	{usagePageGenericDeviceControls, usageBatteryStrength, false},
	{usagePageBatterySystem, usageAbsoluteStateOfCharge, true},
}

type hidpCaps struct {
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

type hidpValueCaps struct {
	UsagePage                                          uint16
	ReportID                                           uint8
	IsAlias                                            uint8
	BitField, LinkCollection, LinkUsage, LinkUsagePage uint16
	IsRange, IsStringRange, IsDesignatorRange          uint8
	IsAbsolute, HasNull, Reserved                      uint8
	BitSize, ReportCount                               uint16
	Reserved2                                          [5]uint16
	UnitsExp, Units                                    uint32
	LogicalMin, LogicalMax, PhysicalMin, PhysicalMax   int32
	// UsageMin, or Usage when IsRange is 0; then UsageMax, string,
	// designator and data indices.
	Usage, UsageMax uint16
	Rest            [6]uint16
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

type platformProvider struct{}

func newPlatformProvider() Provider { return platformProvider{} }

// Reading a report from a Bluetooth HID device that is asleep blocks for
// about five seconds before failing. Windows' HID driver ignores
// cancellation and completes the request synchronously even on an
// overlapped handle, and a process cannot exit while it is pending. So bt
// reads batteries in a helper copy of itself (ServeHelper), keeps what
// arrives within readBudget, and exits without waiting for the rest.
const readBudget = time.Second

func (platformProvider) Batteries(ctx context.Context) ([]Observation, error) {
	if !helperEnabled {
		return nil, nil
	}
	collections, err := bluetoothCollections()
	if err != nil || len(collections) == 0 {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, readBudget)
	defer cancel()
	helper := exec.Command(executable, helperArgument)
	// Without a console of its own, bt run from a GUI app would flash one.
	helper.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	stdout, err := helper.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := helper.Start(); err != nil {
		return nil, err
	}
	observations := make(chan Observation)
	go func() {
		defer close(observations)
		lines := bufio.NewScanner(stdout)
		for lines.Scan() {
			var observation Observation
			if json.Unmarshal(lines.Bytes(), &observation) != nil {
				continue
			}
			select {
			case observations <- observation:
			case <-ctx.Done():
			}
		}
		helper.Wait()
	}()
	var found []Observation
	for {
		select {
		case observation, ok := <-observations:
			if !ok {
				return found, nil
			}
			found = append(found, observation)
		case <-ctx.Done():
			return found, nil
		}
	}
}

// runHelper reads every Bluetooth HID collection at once and prints one
// JSON Observation per line as each device answers.
func runHelper(stdout io.Writer) {
	collections, err := bluetoothCollections()
	if err != nil {
		return
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	encoder := json.NewEncoder(stdout)
	seen := map[string]bool{}
	for _, collection := range collections {
		wg.Add(1)
		go func() {
			defer wg.Done()
			level, ok := batteryLevel(collection.path)
			if !ok {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if !seen[collection.address] {
				seen[collection.address] = true
				encoder.Encode(Observation{Address: collection.address, Percentage: level, Source: "windows-hid"})
			}
		}()
	}
	wg.Wait()
}

type collection struct{ address, path string }

// bluetoothCollections lists the present HID collections of Bluetooth
// devices, with each device's address.
func bluetoothCollections() ([]collection, error) {
	devices, err := windows.SetupDiGetClassDevsEx(&guidDevinterfaceHID, "", 0, windows.DIGCF_PRESENT|windows.DIGCF_DEVICEINTERFACE, 0, "")
	if err != nil {
		return nil, err
	}
	defer devices.Close()
	var collections []collection
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
			collections = append(collections, collection{address, path})
		}
	}
}

// batteryLevel reads the battery percentage of one HID collection. Windows
// keeps keyboards and mice open exclusively, but other collections of the
// same device can be read.
func batteryLevel(path string) (int, bool) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, false
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return 0, false
	}
	defer windows.CloseHandle(handle)
	var preparsed uintptr
	if ok, _, _ := hidDGetPreparsedData.Call(uintptr(handle), uintptr(unsafe.Pointer(&preparsed))); ok == 0 {
		return 0, false
	}
	defer hidDFreePreparsedData.Call(preparsed)
	var caps hidpCaps
	if status, _, _ := hidPGetCaps.Call(preparsed, uintptr(unsafe.Pointer(&caps))); uint32(status) != hidpStatusSuccess {
		return 0, false
	}
	for _, kind := range []struct {
		reportType uintptr
		count      uint16
		length     uint16
		read       *windows.LazyProc
	}{
		{hidpInput, caps.NumberInputValueCaps, caps.InputReportByteLength, hidDGetInputReport},
		{hidpFeature, caps.NumberFeatureValueCaps, caps.FeatureReportByteLength, hidDGetFeature},
	} {
		if kind.count == 0 || kind.length == 0 {
			continue
		}
		values := make([]hidpValueCaps, kind.count)
		count := kind.count
		if status, _, _ := hidPGetValueCaps.Call(kind.reportType, uintptr(unsafe.Pointer(&values[0])), uintptr(unsafe.Pointer(&count)), preparsed); uint32(status) != hidpStatusSuccess {
			continue
		}
		for _, value := range values[:count] {
			usage := -1
			for i, candidate := range batteryUsages {
				if value.IsRange == 0 && value.UsagePage == candidate.page && value.Usage == candidate.usage {
					usage = i
				}
			}
			if usage < 0 {
				continue
			}
			report := make([]byte, kind.length)
			report[0] = value.ReportID
			if ok, _, _ := kind.read.Call(uintptr(handle), uintptr(unsafe.Pointer(&report[0])), uintptr(len(report))); ok == 0 {
				continue
			}
			var raw uint32
			if status, _, _ := hidPGetUsageValue.Call(kind.reportType, uintptr(value.UsagePage), uintptr(value.LinkCollection), uintptr(value.Usage), uintptr(unsafe.Pointer(&raw)), preparsed, uintptr(unsafe.Pointer(&report[0])), uintptr(len(report))); uint32(status) != hidpStatusSuccess {
				continue
			}
			if batteryUsages[usage].percent {
				if raw <= 100 {
					return int(raw), true
				}
				continue
			}
			if level, ok := scaleBattery(int64(raw), int64(value.LogicalMin), int64(value.LogicalMax)); ok {
				return level, true
			}
		}
	}
	return 0, false
}

// scaleBattery maps a logical value to 0-100, as macOS's provider does.
func scaleBattery(value, minimum, maximum int64) (int, bool) {
	if maximum <= minimum || value < minimum || value > maximum {
		return 0, false
	}
	return int((value - minimum) * 100 / (maximum - minimum)), true
}
