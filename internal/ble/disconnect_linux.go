//go:build linux

package ble

import (
	"strings"

	"github.com/godbus/dbus/v5"
)

// watchLinkLoss calls lost when BlueZ reports the device as disconnected.
// tinygo only forwards Linux connection changes to its connect handler while
// advertising as a peripheral, so a central has to watch D-Bus itself.
func watchLinkLoss(address string, lost func()) func() {
	conn, err := dbus.SystemBus()
	if err != nil {
		return func() {}
	}
	suffix := "/dev_" + strings.ReplaceAll(strings.ToUpper(address), ":", "_")
	match := []dbus.MatchOption{
		dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
		dbus.WithMatchMember("PropertiesChanged"),
		dbus.WithMatchArg(0, "org.bluez.Device1"),
	}
	if err := conn.AddMatchSignal(match...); err != nil {
		return func() {}
	}
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case signal := <-signals:
				if signal == nil || !strings.HasSuffix(string(signal.Path), suffix) || len(signal.Body) < 2 {
					continue
				}
				changes, ok := signal.Body[1].(map[string]dbus.Variant)
				if !ok {
					continue
				}
				if connected, ok := changes["Connected"].Value().(bool); ok && !connected {
					lost()
				}
			}
		}
	}()
	return func() {
		conn.RemoveSignal(signals)
		_ = conn.RemoveMatchSignal(match...)
		close(done)
	}
}
