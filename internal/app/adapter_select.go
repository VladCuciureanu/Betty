package app

import (
	"github.com/vladcuciureanu/betty/internal/backend"
	"github.com/vladcuciureanu/betty/internal/ble"
	"github.com/vladcuciureanu/betty/internal/domain"
)

// selectAdapter points the backend and the BLE client at the adapter id.
// Only Linux has more than one adapter to choose from.
func selectAdapter(b backend.Backend, client *ble.TinyGo, id string) error {
	selector, ok := b.(backend.AdapterSelector)
	if !ok {
		return domain.NewError(domain.CodeUnsupported, "--adapter works only on Linux; this platform uses one Bluetooth adapter")
	}
	if err := selector.SelectAdapter(id); err != nil {
		return err
	}
	return client.UseAdapter(id)
}
