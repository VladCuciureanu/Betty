//go:build linux

package backend

import (
	"context"
	"path"
	"slices"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/vladcuciureanu/betty/internal/domain"
)

func (b *linuxBackend) Adapters(ctx context.Context) ([]domain.Adapter, error) {
	if b.initErr != nil {
		return nil, translateDBus(b.initErr)
	}
	objects, err := b.objects(ctx)
	if err != nil {
		return nil, err
	}
	selected, err := b.adapter(ctx)
	if err != nil {
		return nil, err
	}
	var adapters []domain.Adapter
	for objectPath, interfaces := range objects {
		if props, ok := interfaces["org.bluez.Adapter1"]; ok {
			adapters = append(adapters, adapterFromProperties(objectPath, props, objectPath == selected))
		}
	}
	slices.SortFunc(adapters, func(a, b domain.Adapter) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return adapters, nil
}

func adapterFromProperties(objectPath dbus.ObjectPath, props map[string]dbus.Variant, selected bool) domain.Adapter {
	flag := func(name string) *bool {
		value, ok := variant[bool](props, name)
		if !ok {
			return nil
		}
		return &value
	}
	address, _ := variant[string](props, "Address")
	name, _ := variant[string](props, "Alias")
	return domain.Adapter{
		ID:           path.Base(string(objectPath)),
		Name:         name,
		Address:      address,
		Powered:      flag("Powered"),
		Discoverable: flag("Discoverable"),
		Pairable:     flag("Pairable"),
		Default:      selected,
	}
}

func (b *linuxBackend) SetAdapter(ctx context.Context, change domain.AdapterChange) (domain.Adapter, error) {
	if b.initErr != nil {
		return domain.Adapter{}, translateDBus(b.initErr)
	}
	objectPath, err := b.adapter(ctx)
	if err != nil {
		return domain.Adapter{}, err
	}
	object := b.conn.Object(bluezService, objectPath)
	set := func(name string, value any) error {
		call := object.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Set", 0, "org.bluez.Adapter1", name, dbus.MakeVariant(value))
		return translateDBus(call.Err)
	}
	// Power first: BlueZ refuses to make a powered-off adapter discoverable.
	if change.Powered != nil {
		if err := set("Powered", *change.Powered); err != nil {
			return domain.Adapter{}, err
		}
	}
	if change.Discoverable != nil {
		if err := set("Discoverable", *change.Discoverable); err != nil {
			return domain.Adapter{}, err
		}
	}
	if change.Name != nil {
		if err := set("Alias", *change.Name); err != nil {
			return domain.Adapter{}, err
		}
	}
	// BlueZ applies some settings, such as the name, a moment after the
	// property write returns, so read until they show or 3 seconds pass.
	deadline := time.Now().Add(3 * time.Second)
	for {
		var props map[string]dbus.Variant
		if err := object.CallWithContext(ctx, "org.freedesktop.DBus.Properties.GetAll", 0, "org.bluez.Adapter1").Store(&props); err != nil {
			return domain.Adapter{}, translateDBus(err)
		}
		adapter := adapterFromProperties(objectPath, props, true)
		if applied(adapter, change) || time.Now().After(deadline) {
			return adapter, nil
		}
		select {
		case <-ctx.Done():
			return adapter, nil
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// applied reports whether adapter shows every setting in change.
func applied(adapter domain.Adapter, change domain.AdapterChange) bool {
	same := func(want, have *bool) bool { return want == nil || (have != nil && *have == *want) }
	return same(change.Powered, adapter.Powered) && same(change.Discoverable, adapter.Discoverable) &&
		(change.Name == nil || adapter.Name == *change.Name)
}
