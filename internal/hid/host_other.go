//go:build !windows

// Package hid opens the HID collections of Bluetooth devices for drivers'
// hid host calls. Only Windows has a host so far.
package hid

import (
	"context"

	protocol "github.com/vladcuciureanu/betty/driver"
	"github.com/vladcuciureanu/betty/internal/domain"
)

type Host struct{}

func New() *Host { return &Host{} }

func unsupported() error {
	return domain.NewError(domain.CodeUnsupported, "HID host calls are supported only on Windows so far")
}

func (*Host) OpenHID(context.Context, string, uint16, uint16) (protocol.HIDCollection, error) {
	return protocol.HIDCollection{}, unsupported()
}
func (*Host) ReadHID(context.Context, string) ([]byte, error) { return nil, unsupported() }
func (*Host) GetHIDFeature(context.Context, string, uint8) ([]byte, error) {
	return nil, unsupported()
}
func (*Host) CloseHID(context.Context, string) error { return unsupported() }
