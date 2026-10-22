//go:build darwin

package backend

/*
#include <stdlib.h>
#include "bridge_darwin.h"
*/
import "C"

import (
	"context"
	"strings"
	"time"
	"unsafe"

	"github.com/vladcuciureanu/betty/internal/domain"
)

func (*darwinBackend) Adapters(context.Context) ([]domain.Adapter, error) {
	var address, name *C.char
	var powered C.int
	if result := uint32(C.betty_adapter(&address, &name, &powered)); result != 0 {
		if result == ioReturnNoDevice {
			return nil, domain.NewError(domain.CodeUnavailable, "macOS reports no Bluetooth controller")
		}
		return nil, darwinError("read adapter", result)
	}
	defer C.free(unsafe.Pointer(address))
	defer C.free(unsafe.Pointer(name))
	isPowered := powered != 0
	return []domain.Adapter{{
		ID:      "default",
		Name:    C.GoString(name),
		Address: strings.ToUpper(strings.ReplaceAll(C.GoString(address), "-", ":")),
		Powered: &isPowered,
		Default: true,
	}}, nil
}

// SetAdapter changes only power, through a private IOBluetooth API (#15):
// macOS has no public API for any adapter setting, and no private one Betty
// uses for discoverability or the name.
func (b *darwinBackend) SetAdapter(ctx context.Context, change domain.AdapterChange) (domain.Adapter, error) {
	if change.Discoverable != nil || change.Name != nil {
		return domain.Adapter{}, domain.NewError(domain.CodeUnsupported, "macOS can change only the Bluetooth adapter's power")
	}
	if change.Powered != nil {
		if err := setDarwinPower(ctx, *change.Powered); err != nil {
			return domain.Adapter{}, err
		}
	}
	adapters, err := b.Adapters(ctx)
	if err != nil {
		return domain.Adapter{}, err
	}
	return adapters[0], nil
}

// setDarwinPower asks for the power state and waits until macOS reports it,
// because the controller turns on or off asynchronously.
func setDarwinPower(ctx context.Context, on bool) error {
	want := C.int(0)
	if on {
		want = 1
	}
	if result := uint32(C.betty_set_adapter_power(want)); result == ioReturnUnsupported {
		return domain.NewError(domain.CodeUnsupported, "this macOS release lacks the private API Betty uses for adapter power")
	} else if result != 0 {
		return darwinError("set adapter power", result)
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var state C.int
		if result := uint32(C.betty_adapter_power_state(&state)); result != 0 {
			return darwinError("read adapter power", result)
		}
		if state == want {
			return nil
		}
		select {
		case <-ctx.Done():
			return domain.NewError(domain.CodeTimeout, "macOS did not change the Bluetooth adapter's power before the deadline")
		case <-ticker.C:
		}
	}
}
