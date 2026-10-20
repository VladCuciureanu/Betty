//go:build linux

package backend

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/vladcuciureanu/betty/internal/bluez"
	"github.com/vladcuciureanu/betty/internal/domain"
)

const bluezService = "org.bluez"

type linuxBackend struct {
	conn    *dbus.Conn
	initErr error
	pairing PairingIO
}

func newPlatformBackend(pairing PairingIO) Backend {
	conn, err := dbus.SystemBus()
	return &linuxBackend{conn: conn, initErr: err, pairing: pairing}
}
func (b *linuxBackend) Capabilities() domain.Capabilities {
	return domain.Capabilities{Scan: domain.Supported, Pair: domain.Supported, Unpair: domain.Supported, Connect: domain.Supported, Disconnect: domain.Supported, GATT: domain.Supported}
}

func (b *linuxBackend) Scan(ctx context.Context) ([]domain.Device, error) {
	adapter, err := b.adapter(ctx)
	if err != nil {
		return nil, err
	}
	if call := b.conn.Object(bluezService, adapter).CallWithContext(ctx, "org.bluez.Adapter1.StartDiscovery", 0); call.Err != nil {
		return nil, translateDBus(call.Err)
	}
	defer b.conn.Object(bluezService, adapter).Call("org.bluez.Adapter1.StopDiscovery", 0)
	<-ctx.Done()
	listCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return b.List(listCtx)
}

func (b *linuxBackend) List(ctx context.Context) ([]domain.Device, error) {
	objects, err := b.objects(ctx)
	if err != nil {
		return nil, err
	}
	var devices []domain.Device
	for path, interfaces := range objects {
		props, ok := interfaces["org.bluez.Device1"]
		if !ok {
			continue
		}
		address, _ := variant[string](props, "Address")
		name, _ := variant[string](props, "Alias")
		if name == "" {
			name, _ = variant[string](props, "Name")
		}
		if name == "" {
			name = address
		}
		paired, _ := variant[bool](props, "Paired")
		connected, _ := variant[bool](props, "Connected")
		rssi, hasRSSI := variant[int16](props, "RSSI")
		uuids, _ := variant[[]string](props, "UUIDs")
		d := domain.Device{ID: "bluez:" + address, Name: name, Paired: paired, Connected: connected, Services: uuids, Endpoints: []domain.Endpoint{{ID: string(path), Address: address, Transport: domain.TransportOS}}}
		if hasRSSI {
			d.RSSI = &rssi
		}
		devices = append(devices, d)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Name < devices[j].Name })
	return devices, nil
}

func (b *linuxBackend) Pair(ctx context.Context, d domain.Device) error {
	path, err := endpointPath(d)
	if err != nil {
		return err
	}
	cleanup, agent, err := b.registerPairingAgent()
	if err != nil {
		return err
	}
	defer cleanup()
	device := b.conn.Object(bluezService, path)
	callErr := device.CallWithContext(ctx, "org.bluez.Device1.Pair", 0).Err
	if ctx.Err() != nil {
		// Cancelling the D-Bus call only stops waiting for the reply;
		// bluetoothd keeps pairing until told to stop.
		cancelCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = device.CallWithContext(cancelCtx, "org.bluez.Device1.CancelPairing", 0).Err
		return ctx.Err()
	}
	// AlreadyExists means the device is already paired, which is the goal.
	if callErr != nil && dbusName(callErr) != "org.bluez.Error.AlreadyExists" {
		if agent.needsInteraction() {
			return domain.NewError(domain.CodeInteraction, "pairing requires interactive input")
		}
		return translateDBus(callErr)
	}
	return b.waitProperty(ctx, path, "Paired", true)
}
func (b *linuxBackend) Connect(ctx context.Context, d domain.Device) error {
	return b.deviceCall(ctx, d, "Connect", "Connected", true)
}
func (b *linuxBackend) Disconnect(ctx context.Context, d domain.Device) error {
	return b.deviceCall(ctx, d, "Disconnect", "Connected", false)
}
func (b *linuxBackend) Unpair(ctx context.Context, d domain.Device) error {
	path, err := endpointPath(d)
	if err != nil {
		return err
	}
	adapter := dbus.ObjectPath(strings.SplitN(string(path), "/dev_", 2)[0])
	call := b.conn.Object(bluezService, adapter).CallWithContext(ctx, "org.bluez.Adapter1.RemoveDevice", 0, path)
	if err := translateDBus(call.Err); err != nil {
		return err
	}
	return b.waitRemoved(ctx, path)
}
func (b *linuxBackend) deviceCall(ctx context.Context, d domain.Device, method, property string, desired bool) error {
	path, err := endpointPath(d)
	if err != nil {
		return err
	}
	if err := translateDBus(b.conn.Object(bluezService, path).CallWithContext(ctx, "org.bluez.Device1."+method, 0).Err); err != nil {
		return err
	}
	return b.waitProperty(ctx, path, property, desired)
}

func (b *linuxBackend) waitProperty(ctx context.Context, path dbus.ObjectPath, property string, desired bool) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var value dbus.Variant
		err := b.conn.Object(bluezService, path).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.bluez.Device1", property).Store(&value)
		if err != nil {
			return translateDBus(err)
		}
		if current, ok := value.Value().(bool); ok && current == desired {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (b *linuxBackend) waitRemoved(ctx context.Context, path dbus.ObjectPath) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		objects, err := b.objects(ctx)
		if err != nil {
			return err
		}
		if _, exists := objects[path]; !exists {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func (b *linuxBackend) adapter(ctx context.Context) (dbus.ObjectPath, error) {
	objects, err := b.objects(ctx)
	if err != nil {
		return "", err
	}
	// Prefer a powered adapter, then the lowest path (hci0 before hci1), so
	// the choice does not depend on map iteration order.
	var adapters []dbus.ObjectPath
	for path, i := range objects {
		if _, ok := i["org.bluez.Adapter1"]; ok {
			adapters = append(adapters, path)
		}
	}
	slices.Sort(adapters)
	for _, path := range adapters {
		if powered, _ := variant[bool](objects[path]["org.bluez.Adapter1"], "Powered"); powered {
			return path, nil
		}
	}
	if len(adapters) > 0 {
		return adapters[0], nil
	}
	return "", domain.NewError(domain.CodeUnavailable, "no BlueZ Bluetooth adapter found")
}
func (b *linuxBackend) objects(ctx context.Context) (map[dbus.ObjectPath]map[string]map[string]dbus.Variant, error) {
	if b.initErr != nil {
		return nil, domain.NewError(domain.CodeUnavailable, "cannot connect to system D-Bus: %v", b.initErr)
	}
	var out map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	err := b.conn.Object(bluezService, "/").CallWithContext(ctx, "org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&out)
	return out, translateDBus(err)
}
func variant[T any](props map[string]dbus.Variant, key string) (T, bool) {
	var zero T
	value, ok := props[key]
	if !ok {
		return zero, false
	}
	typed, ok := value.Value().(T)
	return typed, ok
}
func endpointPath(d domain.Device) (dbus.ObjectPath, error) {
	for _, e := range d.Endpoints {
		if strings.HasPrefix(e.ID, "/org/bluez/") {
			return dbus.ObjectPath(e.ID), nil
		}
	}
	return "", domain.NewError(domain.CodeUnsupported, "device has no BlueZ endpoint")
}
func translateDBus(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if name := dbusName(err); name != "" {
		if code, ok := bluez.Code(name); ok {
			return domain.NewError(code, "BlueZ: %v (%s)", err, name)
		}
	}
	return fmt.Errorf("BlueZ: %w", err)
}

// dbusName returns the D-Bus error name of err, or "". godbus returns both
// dbus.Error values and *dbus.Error pointers depending on the call path.
func dbusName(err error) string {
	var value dbus.Error
	if errors.As(err, &value) {
		return value.Name
	}
	var pointer *dbus.Error
	if errors.As(err, &pointer) && pointer != nil {
		return pointer.Name
	}
	return ""
}
