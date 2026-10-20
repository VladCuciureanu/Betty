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
