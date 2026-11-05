//go:build linux

package backend

import (
	"context"
	"fmt"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/vladcuciureanu/betty/internal/domain"
)

func TestTranslateDBus(t *testing.T) {
	cases := map[string]domain.ErrorCode{
		"org.freedesktop.DBus.Error.ServiceUnknown": domain.CodeUnavailable,
		"org.freedesktop.DBus.Error.UnknownObject":  domain.CodeNotFound,
		"org.freedesktop.DBus.Error.AccessDenied":   domain.CodePermission,
		"org.freedesktop.DBus.Error.NoReply":        domain.CodeTimeout,
		"org.bluez.Error.InProgress":                domain.CodeUnavailable,
		"org.bluez.Error.Failed":                    domain.CodeDisconnected,
	}
	for name, want := range cases {
		for _, err := range []error{dbus.NewError(name, []any{"detail"}), *dbus.NewError(name, nil)} {
			if got := domain.AsError(translateDBus(fmt.Errorf("call: %w", err))).Code; got != want {
				t.Errorf("translateDBus(%s %T) = %q, want %q", name, err, got, want)
			}
		}
	}
	if err := translateDBus(context.Canceled); err != context.Canceled {
		t.Errorf("context errors must pass through, got %v", err)
	}
}

func TestDBusNameRecognizesAlreadyExists(t *testing.T) {
	if got := dbusName(dbus.NewError("org.bluez.Error.AlreadyExists", nil)); got != "org.bluez.Error.AlreadyExists" {
		t.Fatalf("dbusName=%q", got)
	}
}

func TestDeviceFromObjectReadsBlueZBattery(t *testing.T) {
	interfaces := map[string]map[string]dbus.Variant{
		"org.bluez.Device1":  {"Address": dbus.MakeVariant("AA:BB:CC:DD:EE:FF"), "Alias": dbus.MakeVariant("Keyboard"), "Paired": dbus.MakeVariant(true)},
		"org.bluez.Battery1": {"Percentage": dbus.MakeVariant(byte(64))},
	}
	d, ok := deviceFromObject("/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF", interfaces)
	if !ok || d.Name != "Keyboard" || !d.Paired {
		t.Fatalf("device=%+v", d)
	}
	if battery := d.Properties["battery"]; string(battery.Value) != `{"level":64}` || battery.Display != "64%" {
		t.Fatalf("battery=%+v", battery)
	}
	if d.Capabilities["battery"].Status != domain.StatusAvailable {
		t.Fatalf("capabilities=%+v", d.Capabilities)
	}
	delete(interfaces, "org.bluez.Battery1")
	if d, _ := deviceFromObject("/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF", interfaces); d.Properties != nil {
		t.Fatalf("a device without Battery1 got %+v", d.Properties)
	}
	if _, ok := deviceFromObject("/org/bluez/hci0", map[string]map[string]dbus.Variant{"org.bluez.Adapter1": {}}); ok {
		t.Fatal("an adapter was read as a device")
	}
}

func TestListKeepsToTheSelectedAdapter(t *testing.T) {
	device := func(name, address string) map[string]map[string]dbus.Variant {
		return map[string]map[string]dbus.Variant{"org.bluez.Device1": {"Address": dbus.MakeVariant(address), "Alias": dbus.MakeVariant(name)}}
	}
	adapter := map[string]map[string]dbus.Variant{"org.bluez.Adapter1": {"Powered": dbus.MakeVariant(true)}}
	objects := map[dbus.ObjectPath]map[string]map[string]dbus.Variant{
		"/org/bluez/hci0":                       adapter,
		"/org/bluez/hci0/dev_AA_AA_AA_AA_AA_AA": device("Keyboard", "AA:AA:AA:AA:AA:AA"),
		"/org/bluez/hci1":                       adapter,
		"/org/bluez/hci1/dev_BB_BB_BB_BB_BB_BB": device("Speaker", "BB:BB:BB:BB:BB:BB"),
		// hci10 must not count as hci1.
		"/org/bluez/hci10":                       adapter,
		"/org/bluez/hci10/dev_CC_CC_CC_CC_CC_CC": device("Mouse", "CC:CC:CC:CC:CC:CC"),
	}
	names := func(devices []domain.Device) []string {
		var out []string
		for _, d := range devices {
			out = append(out, d.Name)
		}
		return out
	}

	all, err := (&linuxBackend{}).devices(objects)
	if err != nil || len(all) != 3 {
		t.Fatalf("without --adapter: %v, %v", names(all), err)
	}
	selected, err := (&linuxBackend{selected: "hci1"}).devices(objects)
	if err != nil || len(selected) != 1 || selected[0].Name != "Speaker" {
		t.Fatalf("--adapter hci1: %v, %v", names(selected), err)
	}
	if _, err := (&linuxBackend{selected: "hci9"}).devices(objects); domain.AsError(err).Code != domain.CodeNotFound {
		t.Fatalf("--adapter hci9: %v", err)
	}
}
