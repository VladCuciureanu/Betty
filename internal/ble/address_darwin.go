//go:build darwin

package ble

import (
	"strings"

	"tinygo.org/x/bluetooth"
)

// parseAddress parses a CoreBluetooth peripheral identifier. macOS never
// exposes BLE MAC addresses, so tinygo addresses there are UUIDs.
func parseAddress(value string) (bluetooth.Address, bool) {
	value = strings.TrimSpace(value)
	if len(value) != 36 {
		return bluetooth.Address{}, false
	}
	uuid, err := bluetooth.ParseUUID(value)
	if err != nil {
		return bluetooth.Address{}, false
	}
	return bluetooth.Address{UUID: uuid}, true
}
