//go:build linux

package backend

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/godbus/dbus/v5"
	"github.com/vladcuciureanu/betty/internal/domain"
)

const bluez = "org.bluez"

type linuxBackend struct {
	conn    *dbus.Conn
	initErr error
}

func newPlatformBackend() Backend {
	conn, err := dbus.SystemBus()
	return &linuxBackend{conn: conn, initErr: err}
}
func (b *linuxBackend) Capabilities() domain.Capabilities {
	return domain.Capabilities{Scan: domain.Supported, Pair: domain.Supported, Unpair: domain.Supported, Connect: domain.Supported, Disconnect: domain.Supported, GATT: domain.Supported}
}

func (b *linuxBackend) Scan(ctx context.Context) ([]domain.Device, error) {
	adapter, err := b.adapter(ctx)
	if err != nil {
		return nil, err
	}
	if call := b.conn.Object(bluez, adapter).CallWithContext(ctx, "org.bluez.Adapter1.StartDiscovery", 0); call.Err != nil {
		return nil, translateDBus(call.Err)
	}
	defer b.conn.Object(bluez, adapter).Call("org.bluez.Adapter1.StopDiscovery", 0)
	<-ctx.Done()
	return b.List(context.Background())
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
	return b.deviceCall(ctx, d, "Pair")
}
func (b *linuxBackend) Connect(ctx context.Context, d domain.Device) error {
	return b.deviceCall(ctx, d, "Connect")
}
func (b *linuxBackend) Disconnect(ctx context.Context, d domain.Device) error {
	return b.deviceCall(ctx, d, "Disconnect")
}
func (b *linuxBackend) Unpair(ctx context.Context, d domain.Device) error {
	path, err := endpointPath(d)
	if err != nil {
		return err
	}
	adapter := dbus.ObjectPath(strings.SplitN(string(path), "/dev_", 2)[0])
	call := b.conn.Object(bluez, adapter).CallWithContext(ctx, "org.bluez.Adapter1.RemoveDevice", 0, path)
	return translateDBus(call.Err)
}
func (b *linuxBackend) deviceCall(ctx context.Context, d domain.Device, method string) error {
	path, err := endpointPath(d)
	if err != nil {
		return err
	}
	return translateDBus(b.conn.Object(bluez, path).CallWithContext(ctx, "org.bluez.Device1."+method, 0).Err)
}
func (b *linuxBackend) adapter(ctx context.Context) (dbus.ObjectPath, error) {
	objects, err := b.objects(ctx)
	if err != nil {
		return "", err
	}
	for path, i := range objects {
		if _, ok := i["org.bluez.Adapter1"]; ok {
			return path, nil
		}
	}
	return "", domain.NewError(domain.CodeUnavailable, "no BlueZ Bluetooth adapter found")
}
func (b *linuxBackend) objects(ctx context.Context) (map[dbus.ObjectPath]map[string]map[string]dbus.Variant, error) {
	if b.initErr != nil {
		return nil, domain.NewError(domain.CodeUnavailable, "cannot connect to system D-Bus: %v", b.initErr)
	}
	var out map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	err := b.conn.Object(bluez, "/").CallWithContext(ctx, "org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&out)
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
	var de dbus.Error
	if ok := errorAs(err, &de); ok {
		switch de.Name {
		case "org.bluez.Error.NotSupported":
			return domain.NewError(domain.CodeUnsupported, "%s", de.Body)
		case "org.bluez.Error.NotReady":
			return domain.NewError(domain.CodeUnavailable, "Bluetooth adapter is not ready")
		case "org.bluez.Error.AuthenticationFailed", "org.bluez.Error.AuthenticationCanceled", "org.bluez.Error.AuthenticationRejected":
			return domain.NewError(domain.CodeAuthentication, "Bluetooth authentication failed")
		case "org.bluez.Error.NotAuthorized":
			return domain.NewError(domain.CodePermission, "Bluetooth permission denied")
		}
	}
	return fmt.Errorf("BlueZ: %w", err)
}
func errorAs(err error, target any) bool {
	de, ok := err.(dbus.Error)
	if !ok {
		if p, ok := err.(*dbus.Error); ok {
			de = *p
		} else {
			return false
		}
	}
	*(target.(*dbus.Error)) = de
	return true
}
