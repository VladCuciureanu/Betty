//go:build darwin

package hidbattery

/*
#cgo LDFLAGS: -framework Foundation -framework IOKit -framework CoreFoundation -framework IOBluetooth
#include <stdlib.h>
#include "provider_darwin.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"unsafe"
)

type platformProvider struct{}

func newPlatformProvider() Provider { return platformProvider{} }

func (platformProvider) Batteries(ctx context.Context) ([]Observation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value := C.betty_copy_hid_batteries_json()
	if value == nil {
		return nil, fmt.Errorf("macOS HID battery enumeration failed")
	}
	defer C.free(unsafe.Pointer(value))
	var observations []Observation
	if err := json.Unmarshal([]byte(C.GoString(value)), &observations); err != nil {
		return nil, fmt.Errorf("decode macOS HID batteries: %w", err)
	}
	return observations, nil
}

// runHelper does nothing: only Windows reads batteries in a helper.
func runHelper(io.Writer) {}
