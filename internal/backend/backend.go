package backend

import (
	"context"

	"github.com/vladcuciureanu/betty/internal/domain"
)

type Backend interface {
	Capabilities() domain.Capabilities
	Scan(context.Context) ([]domain.Device, error)
	List(context.Context) ([]domain.Device, error)
	Pair(context.Context, domain.Device) error
	Unpair(context.Context, domain.Device) error
	Connect(context.Context, domain.Device) error
	Disconnect(context.Context, domain.Device) error
}

func New() Backend { return newPlatformBackend() }
