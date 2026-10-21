//go:build linux

package ble

import (
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/vladcuciureanu/betty/internal/domain"
)

func TestFindDescriptorsInTheBlueZTree(t *testing.T) {
	device := "/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF"
	objects := bluezObjects{
		dbus.ObjectPath(device + "/service0010"): {"org.bluez.GattService1": {"UUID": dbus.MakeVariant("0000180d-0000-1000-8000-00805f9b34fb")}},
		dbus.ObjectPath(device + "/service0020"): {"org.bluez.GattService1": {"UUID": dbus.MakeVariant("0000180f-0000-1000-8000-00805f9b34fb")}},
		dbus.ObjectPath(device + "/service0010/char0011"): {"org.bluez.GattCharacteristic1": {
			"UUID": dbus.MakeVariant("00002a37-0000-1000-8000-00805f9b34fb"), "Service": dbus.MakeVariant(dbus.ObjectPath(device + "/service0010"))}},
		dbus.ObjectPath(device + "/service0020/char0021"): {"org.bluez.GattCharacteristic1": {
			"UUID": dbus.MakeVariant("00002a37-0000-1000-8000-00805f9b34fb"), "Service": dbus.MakeVariant(dbus.ObjectPath(device + "/service0020"))}},
		dbus.ObjectPath(device + "/service0010/char0011/desc0013"): {"org.bluez.GattDescriptor1": {
			"UUID": dbus.MakeVariant("00002902-0000-1000-8000-00805f9b34fb"), "Characteristic": dbus.MakeVariant(dbus.ObjectPath(device + "/service0010/char0011"))}},
	}
	if _, _, err := findCharacteristic(objects, "aa:bb:cc:dd:ee:ff", "", "2a37"); domain.AsError(err).Code != domain.CodeAmbiguous {
		t.Fatalf("a characteristic in two services must be ambiguous: %v", err)
	}
	path, service, err := findCharacteristic(objects, "AA:BB:CC:DD:EE:FF", "180d", "2A37")
	if err != nil || path != dbus.ObjectPath(device+"/service0010/char0011") || service != "0000180d-0000-1000-8000-00805f9b34fb" {
		t.Fatalf("path=%s service=%s err=%v", path, service, err)
	}
	descriptors := findDescriptors(objects, path)
	if len(descriptors) != 1 || descriptors["00002902-0000-1000-8000-00805f9b34fb"] == "" {
		t.Fatalf("descriptors=%v", descriptors)
	}
	if _, _, err := findCharacteristic(objects, "11:22:33:44:55:66", "", "2a37"); domain.AsError(err).Code != domain.CodeNotFound {
		t.Fatalf("another device's characteristic was found: %v", err)
	}
}
