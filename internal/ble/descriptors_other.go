//go:build !linux

package ble

import (
	"context"

	"github.com/vladcuciureanu/betty/internal/domain"
)

// tinygo has no descriptor API, and Betty reads descriptors itself only
// through BlueZ so far.

func descriptorSupport() error {
	return domain.NewError(domain.CodeUnsupported, "GATT descriptors are supported only on Linux so far")
}

func platformDescriptors(context.Context, string, string, string) ([]Descriptor, error) {
	return nil, descriptorSupport()
}

func platformReadDescriptor(context.Context, string, string, string, string) ([]byte, error) {
	return nil, descriptorSupport()
}

func platformWriteDescriptor(context.Context, string, string, string, string, []byte) error {
	return descriptorSupport()
}
