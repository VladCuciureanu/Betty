package main

import (
	"encoding/binary"
	"hash/crc32"
	"testing"

	protocol "github.com/vladcuciureanu/betty/driver"
)

// bluetoothReport builds an extended report with its CRC-32.
func bluetoothReport(id uint8, statusOffset int, status byte) []byte {
	report := make([]byte, bluetoothReportLength)
	report[0] = id
	report[statusOffset] = status
	body := len(report) - 4
	crc := crc32.Update(crc32.ChecksumIEEE([]byte{0xa1}), crc32.IEEETable, report[:body])
	binary.LittleEndian.PutUint32(report[body:], crc)
	return report
}

func TestExtendedBatteryNeedsTheExtendedReportAndAValidCRC(t *testing.T) {
	report := bluetoothReport(0x31, 54, 0x18)
	if got, ok := dualSense.extendedBattery(report); !ok || got != (protocol.BatteryComponent{Level: 85, Charging: true, Status: "charging"}) {
		t.Fatalf("battery = %+v, %v", got, ok)
	}
	corrupt := append([]byte(nil), report...)
	corrupt[54] = 0x05
	if _, ok := dualSense.extendedBattery(corrupt); ok {
		t.Fatal("a report with a bad CRC was decoded")
	}
	if _, ok := dualSense.extendedBattery(bluetoothReport(0x01, 54, 0x18)); ok {
		t.Fatal("the basic report was decoded")
	}
	if _, ok := dualSense.extendedBattery(report[:10]); ok {
		t.Fatal("a short report was decoded")
	}
	if got, ok := dualShock4.extendedBattery(bluetoothReport(0x11, 32, 0x07)); !ok || got.Level != 75 || got.Charging {
		t.Fatalf("DualShock 4 battery = %+v, %v", got, ok)
	}
}

func TestDualSenseBattery(t *testing.T) {
	for _, c := range []struct {
		status byte
		want   protocol.BatteryComponent
		ok     bool
	}{
		{0x00, protocol.BatteryComponent{Level: 5, Status: "discharging"}, true},
		{0x08, protocol.BatteryComponent{Level: 85, Status: "discharging"}, true},
		{0x0a, protocol.BatteryComponent{Level: 100, Status: "discharging"}, true},
		{0x13, protocol.BatteryComponent{Level: 35, Charging: true, Status: "charging"}, true},
		{0x2a, protocol.BatteryComponent{Level: 100, Status: "full"}, true},
		{0xa5, protocol.BatteryComponent{}, false},
		{0xb5, protocol.BatteryComponent{}, false},
		{0xf0, protocol.BatteryComponent{}, false},
	} {
		if got, ok := dualSenseBattery(c.status); got != c.want || ok != c.ok {
			t.Errorf("dualSenseBattery(%#x) = %+v, %v", c.status, got, ok)
		}
	}
}

func TestDualShock4Battery(t *testing.T) {
	for _, c := range []struct {
		status byte
		want   protocol.BatteryComponent
		ok     bool
	}{
		{0x04, protocol.BatteryComponent{Level: 45, Status: "discharging"}, true},
		{0x0b, protocol.BatteryComponent{Level: 100, Status: "discharging"}, true},
		{0x14, protocol.BatteryComponent{Level: 45, Charging: true, Status: "charging"}, true},
		{0x1a, protocol.BatteryComponent{Level: 100, Charging: true, Status: "charging"}, true},
		{0x1b, protocol.BatteryComponent{Level: 100, Status: "full"}, true},
		{0x1e, protocol.BatteryComponent{}, false},
	} {
		if got, ok := dualShock4Battery(c.status); got != c.want || ok != c.ok {
			t.Errorf("dualShock4Battery(%#x) = %+v, %v", c.status, got, ok)
		}
	}
}

func TestEveryControllerHasAFamily(t *testing.T) {
	for product, known := range controllers {
		if known.model == "" || known.family.decode == nil || known.family.extendedReport == 0 {
			t.Errorf("product %#04x: %+v", product, known)
		}
	}
}
