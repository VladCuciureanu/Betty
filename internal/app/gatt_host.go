package app

import (
	"context"

	protocol "github.com/vladcuciureanu/betty/driver"
	"github.com/vladcuciureanu/betty/internal/ble"
	"github.com/vladcuciureanu/betty/internal/driver"
)

// gattHost serves drivers' GATT host calls over Betty's BLE client.
type gattHost struct{ ble *ble.TinyGo }

func (h gattHost) OpenGATT(ctx context.Context, address string) (driver.GATTConnection, error) {
	conn, err := h.ble.Open(ctx, address)
	if err != nil {
		return nil, err
	}
	return gattConnection{conn}, nil
}

type gattConnection struct{ *ble.Conn }

func (c gattConnection) Services(ctx context.Context) ([]protocol.GATTService, error) {
	services, err := c.Conn.Services(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]protocol.GATTService, len(services))
	for i, service := range services {
		out[i] = protocol.GATTService{UUID: service.UUID, Characteristics: service.Characteristics}
	}
	return out, nil
}
