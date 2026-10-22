package driver

import (
	"context"

	protocol "github.com/vladcuciureanu/betty/driver"
	"github.com/vladcuciureanu/betty/internal/domain"
)

type Host interface {
	OpenL2CAP(context.Context, string, uint16) (string, error)
	ReadL2CAP(context.Context, string, int) ([]byte, error)
	WriteL2CAP(context.Context, string, []byte) (int, error)
	CloseL2CAP(context.Context, string) error
}

type RFCOMMHost interface {
	OpenRFCOMM(context.Context, string, string) (string, error)
	ReadRFCOMM(context.Context, string, int) ([]byte, error)
	WriteRFCOMM(context.Context, string, []byte) (int, error)
	CloseRFCOMM(context.Context, string) error
}

// GATTHost opens BLE connections for GATT host calls.
type GATTHost interface {
	OpenGATT(ctx context.Context, address string) (GATTConnection, error)
}

// GATTConnection is one BLE connection a driver session shares across its
// GATT host calls.
type GATTConnection interface {
	Services(context.Context) ([]protocol.GATTService, error)
	Read(ctx context.Context, serviceUUID, characteristicUUID string) ([]byte, error)
	Write(ctx context.Context, serviceUUID, characteristicUUID string, data []byte, withoutResponse bool) error
	// Subscribe calls emit with each notified value until stop is called.
	Subscribe(ctx context.Context, serviceUUID, characteristicUUID string, emit func([]byte)) (stop func(), err error)
	// Lost is closed when the link drops.
	Lost() <-chan struct{}
	Close() error
}

// HIDHost opens HID collections for hid host calls. Handles are opaque IDs
// the host allocates.
type HIDHost interface {
	// OpenHID opens the collection of the device at address whose top-level
	// usage matches; zero for both opens the first one it can.
	OpenHID(ctx context.Context, address string, usagePage, usage uint16) (protocol.HIDCollection, error)
	// ReadHID returns the next input report, starting with its report ID.
	ReadHID(ctx context.Context, handleID string) ([]byte, error)
	// GetHIDFeature returns a feature report, starting with its report ID.
	GetHIDFeature(ctx context.Context, handleID string, reportID uint8) ([]byte, error)
	CloseHID(ctx context.Context, handleID string) error
}

type UnsupportedHIDHost struct{}

func (UnsupportedHIDHost) OpenHID(context.Context, string, uint16, uint16) (protocol.HIDCollection, error) {
	return protocol.HIDCollection{}, domain.NewError(domain.CodeUnsupported, "HID is not supported by this platform backend")
}
func (UnsupportedHIDHost) ReadHID(context.Context, string) ([]byte, error) {
	return nil, domain.NewError(domain.CodeUnsupported, "HID is not supported by this platform backend")
}
func (UnsupportedHIDHost) GetHIDFeature(context.Context, string, uint8) ([]byte, error) {
	return nil, domain.NewError(domain.CodeUnsupported, "HID is not supported by this platform backend")
}
func (UnsupportedHIDHost) CloseHID(context.Context, string) error {
	return domain.NewError(domain.CodeUnsupported, "HID is not supported by this platform backend")
}

type UnsupportedGATTHost struct{}

func (UnsupportedGATTHost) OpenGATT(context.Context, string) (GATTConnection, error) {
	return nil, domain.NewError(domain.CodeUnsupported, "GATT is not supported by this platform backend")
}

type UnsupportedHost struct{}

func (UnsupportedHost) OpenL2CAP(context.Context, string, uint16) (string, error) {
	return "", domain.NewError(domain.CodeUnsupported, "L2CAP is not supported by this platform backend")
}
func (UnsupportedHost) ReadL2CAP(context.Context, string, int) ([]byte, error) {
	return nil, domain.NewError(domain.CodeUnsupported, "L2CAP is not supported by this platform backend")
}
func (UnsupportedHost) WriteL2CAP(context.Context, string, []byte) (int, error) {
	return 0, domain.NewError(domain.CodeUnsupported, "L2CAP is not supported by this platform backend")
}
func (UnsupportedHost) CloseL2CAP(context.Context, string) error {
	return domain.NewError(domain.CodeUnsupported, "L2CAP is not supported by this platform backend")
}

type UnsupportedRFCOMMHost struct{}

func (UnsupportedRFCOMMHost) OpenRFCOMM(context.Context, string, string) (string, error) {
	return "", domain.NewError(domain.CodeUnsupported, "RFCOMM is not supported by this platform backend")
}
func (UnsupportedRFCOMMHost) ReadRFCOMM(context.Context, string, int) ([]byte, error) {
	return nil, domain.NewError(domain.CodeUnsupported, "RFCOMM is not supported by this platform backend")
}
func (UnsupportedRFCOMMHost) WriteRFCOMM(context.Context, string, []byte) (int, error) {
	return 0, domain.NewError(domain.CodeUnsupported, "RFCOMM is not supported by this platform backend")
}
func (UnsupportedRFCOMMHost) CloseRFCOMM(context.Context, string) error {
	return domain.NewError(domain.CodeUnsupported, "RFCOMM is not supported by this platform backend")
}
