//go:build linux

package ble

import "tinygo.org/x/bluetooth"

// UseAdapter makes the client use the BlueZ adapter id, such as "hci1". It
// must be called before the client is first used.
func (b *TinyGo) UseAdapter(id string) error {
	b.adapter = bluetooth.NewAdapter(id)
	return nil
}
