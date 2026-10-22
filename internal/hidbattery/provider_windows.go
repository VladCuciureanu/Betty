//go:build windows

package hidbattery

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/vladcuciureanu/betty/internal/hid"
)

// Windows publishes no battery property for Bluetooth HID devices, so Betty
// reads the standard battery usages from each Bluetooth HID collection, as
// macOS's IOHID path does.

var (
	hidDLL             = windows.NewLazySystemDLL("hid.dll")
	hidDGetInputReport = hidDLL.NewProc("HidD_GetInputReport")
	hidDGetFeature     = hidDLL.NewProc("HidD_GetFeature")
	hidPGetValueCaps   = hidDLL.NewProc("HidP_GetValueCaps")
	hidPGetUsageValue  = hidDLL.NewProc("HidP_GetUsageValue")
)

const (
	hidpInput   = 0
	hidpFeature = 2

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
	collections, err := hid.Collections()
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
	collections, err := hid.Collections()
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
			level, ok := batteryLevel(collection.Path)
			if !ok {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if !seen[collection.Address] {
				seen[collection.Address] = true
				encoder.Encode(Observation{Address: collection.Address, Percentage: level, Source: "windows-hid"})
			}
		}()
	}
	wg.Wait()
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
	preparsed, free, ok := hid.PreparsedData(handle)
	if !ok {
		return 0, false
	}
	defer free()
	caps, ok := hid.GetCaps(preparsed)
	if !ok {
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
		if status, _, _ := hidPGetValueCaps.Call(kind.reportType, uintptr(unsafe.Pointer(&values[0])), uintptr(unsafe.Pointer(&count)), preparsed); uint32(status) != hid.StatusSuccess {
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
			if status, _, _ := hidPGetUsageValue.Call(kind.reportType, uintptr(value.UsagePage), uintptr(value.LinkCollection), uintptr(value.Usage), uintptr(unsafe.Pointer(&raw)), preparsed, uintptr(unsafe.Pointer(&report[0])), uintptr(len(report))); uint32(status) != hid.StatusSuccess {
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
