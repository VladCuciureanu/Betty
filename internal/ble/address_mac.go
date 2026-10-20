//go:build linux || windows

package ble

import (
	"strings"

	"tinygo.org/x/bluetooth"
)

// parseAddress parses a BLE MAC address such as AA:BB:CC:DD:EE:FF. tinygo's
// Address.Set silently keeps the zero address on malformed input, so callers
// must validate here first.
func parseAddress(value string) (bluetooth.Address, bool) {
	value = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), "-", ":"))
	if len(value) != 17 {
		return bluetooth.Address{}, false
	}
	for i := 2; i < len(value); i += 3 {
		if value[i] != ':' {
			return bluetooth.Address{}, false
		}
	}
	mac, err := bluetooth.ParseMAC(value)
	if err != nil {
		return bluetooth.Address{}, false
	}
	return bluetooth.Address{MACAddress: bluetooth.MACAddress{MAC: mac}}, true
}
