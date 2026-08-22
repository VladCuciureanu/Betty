//go:build windows

package ble

import "tinygo.org/x/bluetooth"

func stopNotifications(bluetooth.DeviceCharacteristic) {
	// tinygo.org/x/bluetooth v0.15.0 does not implement the documented nil
	// callback unsubscribe path on Windows. Disconnecting the process-scoped
	// GATT device immediately below closes the subscription and its resources.
}
