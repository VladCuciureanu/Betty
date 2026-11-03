//go:build linux

package ble

import (
	"github.com/godbus/dbus/v5"
	"tinygo.org/x/bluetooth"
)

// UseAdapter makes the client use the BlueZ adapter id, such as "hci1". It
// must be called before the client is first used.
func (b *TinyGo) UseAdapter(id string) error {
	b.adapter, b.adapterID = bluetooth.NewAdapter(id), id
	return nil
}

// endScanSession ends the discovery session that an interrupted tinygo scan
// leaves open. BlueZ keeps a client's session when it restarts discovery,
// and refuses that client's next StartDiscovery as already in progress.
// tinygo scans on the shared system bus connection, so this call ends the
// same client's session.
func endScanSession(adapterID string) {
	bus, err := dbus.SystemBus()
	if err != nil {
		return
	}
	_ = bus.Object("org.bluez", dbus.ObjectPath("/org/bluez/"+adapterID)).Call("org.bluez.Adapter1.StopDiscovery", 0).Err
}
