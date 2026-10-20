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

// SetAdapter is unsupported: macOS has no public API to change the
// controller's power, discoverability, or name (see #15).
func (*darwinBackend) SetAdapter(context.Context, domain.AdapterChange) (domain.Adapter, error) {
	return domain.Adapter{}, domain.NewError(domain.CodeUnsupported, "macOS has no public API to change the Bluetooth adapter")
}
