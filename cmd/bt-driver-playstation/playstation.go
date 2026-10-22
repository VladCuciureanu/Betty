package main

import (
	"encoding/binary"
	"hash/crc32"

	protocol "github.com/vladcuciureanu/betty/driver"
)

// Over Bluetooth, PlayStation controllers start by sending a basic input
// report (ID 0x01) with only sticks, buttons and triggers. They send the
// extended report, which carries battery, only after a host requests a
// calibration feature report, and keep sending it until they are power
// cycled. Games that read the basic report through DirectInput stop seeing
// input once the controller switches, so the driver switches only when asked
// for the battery explicitly. Layouts follow Linux's hid-playstation.

const sonyVendorID = 0x054c

type family struct {
	// extendedReport is the Bluetooth report ID that carries battery, and
	// switchReport the feature report that makes the controller send it.
	extendedReport, switchReport uint8
	// statusOffset is the battery status byte's offset in the extended
	// report, counting the report ID.
	statusOffset int
	decode       func(status byte) (protocol.BatteryComponent, bool)
}

var (
	dualSense   = family{extendedReport: 0x31, switchReport: 0x05, statusOffset: 54, decode: dualSenseBattery}
	dualShock4  = family{extendedReport: 0x11, switchReport: 0x02, statusOffset: 32, decode: dualShock4Battery}
	controllers = map[uint16]struct {
		model  string
		family family
	}{
		0x0ce6: {"DualSense", dualSense},
		0x0df2: {"DualSense Edge", dualSense},
		0x05c4: {"DualShock 4", dualShock4},
		0x09cc: {"DualShock 4", dualShock4},
	}
)

// bluetoothReportLength is the length of both extended reports, which end in
// a CRC-32.
const bluetoothReportLength = 78

// extendedBattery decodes the battery in an extended report, and reports
// false for any other report, a report that fails its CRC, or an unknown
// battery state.
func (f family) extendedBattery(report []byte) (protocol.BatteryComponent, bool) {
	if len(report) < bluetoothReportLength || report[0] != f.extendedReport || !validCRC(report[:bluetoothReportLength]) {
		return protocol.BatteryComponent{}, false
	}
	return f.decode(report[f.statusOffset])
}

// validCRC checks a Bluetooth input report's trailing CRC-32, computed over
// the input-report seed byte 0xa1 and the rest of the report.
func validCRC(report []byte) bool {
	body := len(report) - 4
	crc := crc32.Update(crc32.ChecksumIEEE([]byte{0xa1}), crc32.IEEETable, report[:body])
	return crc == binary.LittleEndian.Uint32(report[body:])
}

// dualSenseBattery decodes a DualSense status byte: the low nibble is the
// level in tenths, the high nibble the charging state.
func dualSenseBattery(status byte) (protocol.BatteryComponent, bool) {
	level := min(int(status&0x0f)*10+5, 100)
	switch status >> 4 {
	case 0x0:
		return protocol.BatteryComponent{Level: level, Status: "discharging"}, true
	case 0x1:
		return protocol.BatteryComponent{Level: level, Charging: true, Status: "charging"}, true
	case 0x2:
		return protocol.BatteryComponent{Level: 100, Status: "full"}, true
	}
	// 0xa and 0xb mean it isn't charging because of a voltage or
	// temperature fault, and the level isn't reported then.
	return protocol.BatteryComponent{}, false
}

// dualShock4Battery decodes a DualShock 4 status byte: the low nibble is the
// level in tenths, 11 meaning full, and bit 4 is set while a cable is in.
func dualShock4Battery(status byte) (protocol.BatteryComponent, bool) {
	level := int(status & 0x0f)
	if status&0x10 == 0 {
		return protocol.BatteryComponent{Level: min(level*10+5, 100), Status: "discharging"}, true
	}
	switch {
	case level < 10:
		return protocol.BatteryComponent{Level: level*10 + 5, Charging: true, Status: "charging"}, true
	case level == 10:
		return protocol.BatteryComponent{Level: 100, Charging: true, Status: "charging"}, true
	case level == 11:
		return protocol.BatteryComponent{Level: 100, Status: "full"}, true
	}
	return protocol.BatteryComponent{}, false
}
