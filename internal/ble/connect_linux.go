//go:build linux

package ble

import (
	"context"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/vladcuciureanu/betty/internal/domain"
)

// preconnect connects the device through BlueZ under ctx before tinygo's
// Connect runs. tinygo makes the same Device1.Connect call without a
// context and then waits for the Connected property with no timeout, so a
// cancelled command could leave it blocked; once the device is connected,
// tinygo returns at once. A device BlueZ doesn't know yet is left to tinygo.
func preconnect(ctx context.Context, address string) error {
	conn, err := dbus.SystemBus()
	if err != nil {
		return nil
	}
	var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if err := conn.Object("org.bluez", "/").CallWithContext(ctx, "org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objects); err != nil {
		return nil
	}
	suffix := "/dev_" + strings.ReplaceAll(strings.ToUpper(address), ":", "_")
	for path, interfaces := range objects {
		props, ok := interfaces["org.bluez.Device1"]
		if !ok || !strings.HasSuffix(string(path), suffix) {
			continue
		}
		if connected, _ := props["Connected"].Value().(bool); connected {
			return nil
		}
		device := conn.Object("org.bluez", path)
		call := device.CallWithContext(ctx, "org.bluez.Device1.Connect", 0)
		if ctx.Err() != nil {
			// BlueZ may still finish connecting; nobody would own the link.
			go func() {
				disconnect, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				device.CallWithContext(disconnect, "org.bluez.Device1.Disconnect", 0)
			}()
			return ctx.Err()
		}
		if call.Err != nil {
			return translate("connect", call.Err, domain.CodeDisconnected)
		}
		return nil
	}
	return nil
}
