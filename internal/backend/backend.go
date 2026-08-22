package backend

import (
	"context"
	"io"

	"github.com/vladcuciureanu/betty/internal/domain"
)

type PairingIO struct {
	In  io.Reader
	Out io.Writer
}

type Backend interface {
	Capabilities() domain.Capabilities
	Scan(context.Context) ([]domain.Device, error)
	List(context.Context) ([]domain.Device, error)
	Pair(context.Context, domain.Device) error
	Unpair(context.Context, domain.Device) error
	Connect(context.Context, domain.Device) error
	Disconnect(context.Context, domain.Device) error
}

func New(pairing PairingIO) Backend { return newPlatformBackend(pairing) }
