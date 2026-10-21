//go:build linux

package ble

import (
	"context"
	"strings"

	"github.com/godbus/dbus/v5"

	"github.com/vladcuciureanu/betty/internal/domain"
)

// tinygo has no descriptor API, so on Linux Betty reads BlueZ's GATT object
// tree directly: each descriptor is a GattDescriptor1 object whose
// Characteristic property names its characteristic's object.

func descriptorSupport() error { return nil }

type bluezObjects = map[dbus.ObjectPath]map[string]map[string]dbus.Variant

func bluezTree(ctx context.Context) (*dbus.Conn, bluezObjects, error) {
	conn, err := dbus.SystemBus()
	if err != nil {
		return nil, nil, domain.NewError(domain.CodeUnavailable, "D-Bus system bus unavailable: %v", err)
	}
	var objects bluezObjects
	if err := conn.Object("org.bluez", "/").CallWithContext(ctx, "org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objects); err != nil {
		return nil, nil, translate("list GATT objects", err, domain.CodeUnavailable)
	}
	return conn, objects, nil
}

func uuidOf(props map[string]dbus.Variant) string {
	uuid, _ := props["UUID"].Value().(string)
	return strings.ToLower(uuid)
}

// findCharacteristic returns the object of the device's characteristic. A
// UUID in several services is ambiguous unless serviceUUID names one.
func findCharacteristic(objects bluezObjects, address, serviceUUID, characteristicUUID string) (dbus.ObjectPath, string, error) {
	device := "/dev_" + strings.ReplaceAll(strings.ToUpper(address), ":", "_") + "/"
	want, wantService := normalizeUUID(characteristicUUID), ""
	if serviceUUID != "" {
		wantService = normalizeUUID(serviceUUID)
	}
	var matches []dbus.ObjectPath
	var services []string
	for path, interfaces := range objects {
		props, ok := interfaces["org.bluez.GattCharacteristic1"]
		if !ok || !strings.Contains(string(path), device) || uuidOf(props) != want {
			continue
		}
		servicePath, _ := props["Service"].Value().(dbus.ObjectPath)
		service := uuidOf(objects[servicePath]["org.bluez.GattService1"])
		if wantService != "" && service != wantService {
			continue
		}
		matches = append(matches, path)
		services = append(services, service)
	}
	switch len(matches) {
	case 0:
		return "", "", domain.NewError(domain.CodeNotFound, "characteristic %s not found", characteristicUUID)
	case 1:
		return matches[0], services[0], nil
	default:
		return "", "", domain.NewError(domain.CodeAmbiguous, "characteristic %s is in several services (%s); name the service", characteristicUUID, strings.Join(services, ", "))
	}
}

func findDescriptors(objects bluezObjects, characteristic dbus.ObjectPath) map[string]dbus.ObjectPath {
	out := map[string]dbus.ObjectPath{}
	for path, interfaces := range objects {
		props, ok := interfaces["org.bluez.GattDescriptor1"]
		if !ok {
			continue
		}
		if owner, _ := props["Characteristic"].Value().(dbus.ObjectPath); owner == characteristic {
			out[uuidOf(props)] = path
		}
	}
	return out
}

func platformDescriptors(ctx context.Context, address, serviceUUID, characteristicUUID string) ([]Descriptor, error) {
	_, objects, err := bluezTree(ctx)
	if err != nil {
		return nil, err
	}
	path, service, err := findCharacteristic(objects, address, serviceUUID, characteristicUUID)
	if err != nil {
		return nil, err
	}
	var out []Descriptor
	for _, uuid := range sortedKeys(findDescriptors(objects, path)) {
		out = append(out, Descriptor{ServiceUUID: service, CharacteristicUUID: normalizeUUID(characteristicUUID), UUID: uuid})
	}
	return out, nil
}

func descriptorObject(ctx context.Context, address, serviceUUID, characteristicUUID, descriptorUUID string) (dbus.BusObject, error) {
	conn, objects, err := bluezTree(ctx)
	if err != nil {
		return nil, err
	}
	path, _, err := findCharacteristic(objects, address, serviceUUID, characteristicUUID)
	if err != nil {
		return nil, err
	}
	descriptor, ok := findDescriptors(objects, path)[normalizeUUID(descriptorUUID)]
	if !ok {
		return nil, domain.NewError(domain.CodeNotFound, "descriptor %s not found on characteristic %s", descriptorUUID, characteristicUUID)
	}
	return conn.Object("org.bluez", descriptor), nil
}

func platformReadDescriptor(ctx context.Context, address, serviceUUID, characteristicUUID, descriptorUUID string) ([]byte, error) {
	object, err := descriptorObject(ctx, address, serviceUUID, characteristicUUID, descriptorUUID)
	if err != nil {
		return nil, err
	}
	var value []byte
	if err := object.CallWithContext(ctx, "org.bluez.GattDescriptor1.ReadValue", 0, map[string]dbus.Variant{}).Store(&value); err != nil {
		return nil, translate("read descriptor", err, domain.CodeInternal)
	}
	return value, nil
}

func platformWriteDescriptor(ctx context.Context, address, serviceUUID, characteristicUUID, descriptorUUID string, value []byte) error {
	object, err := descriptorObject(ctx, address, serviceUUID, characteristicUUID, descriptorUUID)
	if err != nil {
		return err
	}
	call := object.CallWithContext(ctx, "org.bluez.GattDescriptor1.WriteValue", 0, value, map[string]dbus.Variant{})
	return translate("write descriptor", call.Err, domain.CodeInternal)
}
