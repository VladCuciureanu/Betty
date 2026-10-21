package backend

import (
	"context"

	"github.com/vladcuciureanu/betty/internal/domain"
)

// AdapterSelector is implemented by backends on platforms with several
// adapters to choose from.
type AdapterSelector interface {
	// SelectAdapter makes every later operation use the adapter id, such as
	// "hci1". An adapter that doesn't exist fails when it is first used.
	SelectAdapter(id string) error
}

// AdapterBackend is implemented by backends that can report, and possibly
// change, the local Bluetooth adapter.
type AdapterBackend interface {
	// Adapters lists the local adapters, marking the one commands use.
	Adapters(context.Context) ([]domain.Adapter, error)
	// SetAdapter applies change to the default adapter and returns its state
	// read back afterwards. A setting the platform cannot change fails with
	// unsupported.
	SetAdapter(context.Context, domain.AdapterChange) (domain.Adapter, error)
}
