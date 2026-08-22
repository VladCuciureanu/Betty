package driver

import (
	"context"

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
