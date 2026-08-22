//go:build darwin || linux

package ble

import "tinygo.org/x/bluetooth"

func stopNotifications(characteristic bluetooth.DeviceCharacteristic) {
	_ = characteristic.EnableNotifications(nil)
}
