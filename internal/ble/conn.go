package ble

import (
	"context"
	"strings"
	"sync"

	"tinygo.org/x/bluetooth"

	"github.com/vladcuciureanu/betty/internal/domain"
)

// GATTService is a discovered service and the UUIDs of its characteristics.
type GATTService struct {
	UUID            string
	Characteristics []string
}

// Conn is one BLE connection that several GATT operations share, so that a
// read does not tear down the link a subscription is using.
type Conn struct {
	address string
	device  bluetooth.Device
	lost    <-chan struct{}
	unwatch func()

	mu         sync.Mutex
	discovered bool
	services   []GATTService
	chars      []foundCharacteristic
}

type foundCharacteristic struct {
	service        string
	characteristic bluetooth.DeviceCharacteristic
}

// Open connects to the device at address. The caller must Close the
// connection.
func (b *TinyGo) Open(ctx context.Context, address string) (*Conn, error) {
	device, err := b.connect(ctx, address)
	if err != nil {
		return nil, err
	}
	lost, unwatch := b.watchDisconnect(device.Address.String())
	return &Conn{address: address, device: device, lost: lost, unwatch: unwatch}, nil
}

// Lost is closed when the link drops.
func (c *Conn) Lost() <-chan struct{} { return c.lost }

// Close disconnects.
func (c *Conn) Close() error {
	c.unwatch()
	return c.device.Disconnect()
}

// Services discovers every service and characteristic once per connection.
func (c *Conn) Services(ctx context.Context) ([]GATTService, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.discover(ctx); err != nil {
		return nil, err
	}
	return c.services, nil
}

func (c *Conn) discover(ctx context.Context) error {
	if c.discovered {
		return nil
	}
	// The discovery may outlive a cancelled context, so it fills locals and
	// the connection is only updated once it returns in time.
	var found []GATTService
	var chars []foundCharacteristic
	err := run(ctx, func() error {
		services, err := c.device.DiscoverServices(nil)
		if err != nil {
			return translate("service discovery", err, domain.CodeInternal)
		}
		for _, s := range services {
			// Discover every characteristic and match locally: with a UUID
			// filter, tinygo reports "not found" as an error.
			discovered, err := s.DiscoverCharacteristics(nil)
			if err != nil {
				return translate("characteristic discovery", err, domain.CodeInternal)
			}
			service := GATTService{UUID: s.UUID().String()}
			for _, characteristic := range discovered {
				service.Characteristics = append(service.Characteristics, characteristic.UUID().String())
				chars = append(chars, foundCharacteristic{service: service.UUID, characteristic: characteristic})
			}
			found = append(found, service)
		}
		return nil
	})
	if err != nil {
		return err
	}
	c.services, c.chars, c.discovered = found, chars, true
	return nil
}

// characteristic finds a characteristic by UUID, within serviceUUID when it
// is set. Without a service, a UUID that appears in several services is
// ambiguous rather than silently resolved to the first one.
func (c *Conn) characteristic(ctx context.Context, serviceUUID, characteristicUUID string) (bluetooth.DeviceCharacteristic, error) {
	want, err := bluetooth.ParseUUID(characteristicUUID)
	if err != nil {
		return bluetooth.DeviceCharacteristic{}, domain.NewError(domain.CodeUsage, "invalid characteristic UUID %q", characteristicUUID)
	}
	var service bluetooth.UUID
	if serviceUUID != "" {
		if service, err = bluetooth.ParseUUID(serviceUUID); err != nil {
			return bluetooth.DeviceCharacteristic{}, domain.NewError(domain.CodeUsage, "invalid service UUID %q", serviceUUID)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.discover(ctx); err != nil {
		return bluetooth.DeviceCharacteristic{}, err
	}
	var matches []foundCharacteristic
	for _, found := range c.chars {
		if found.characteristic.UUID() != want {
			continue
		}
		if serviceUUID != "" && !strings.EqualFold(found.service, service.String()) {
			continue
		}
		matches = append(matches, found)
	}
	switch len(matches) {
	case 0:
		return bluetooth.DeviceCharacteristic{}, domain.NewError(domain.CodeNotFound, "characteristic %s not found", characteristicUUID)
	case 1:
		return matches[0].characteristic, nil
	default:
		services := make([]string, len(matches))
		for i, match := range matches {
			services[i] = match.service
		}
		return bluetooth.DeviceCharacteristic{}, domain.NewError(domain.CodeAmbiguous, "characteristic %s is in several services (%s); name the service", characteristicUUID, strings.Join(services, ", "))
	}
}

// Read reads a characteristic's value.
func (c *Conn) Read(ctx context.Context, serviceUUID, characteristicUUID string) ([]byte, error) {
	characteristic, err := c.characteristic(ctx, serviceUUID, characteristicUUID)
	if err != nil {
		return nil, err
	}
	var value []byte
	err = run(ctx, func() error {
		buf := make([]byte, 512)
		n, err := characteristic.Read(buf)
		if err != nil {
			return translate("read", err, domain.CodeInternal)
		}
		value = buf[:n]
		return nil
	})
	return value, err
}

// Write writes a characteristic's value, with or without a response.
func (c *Conn) Write(ctx context.Context, serviceUUID, characteristicUUID string, value []byte, withoutResponse bool) error {
	characteristic, err := c.characteristic(ctx, serviceUUID, characteristicUUID)
	if err != nil {
		return err
	}
	return run(ctx, func() error {
		var err error
		if withoutResponse {
			_, err = characteristic.WriteWithoutResponse(value)
		} else {
			_, err = characteristic.Write(value)
		}
		return translate("write", err, domain.CodeInternal)
	})
}

// Subscribe enables notifications or indications and calls emit with a copy
// of each value until stop is called.
func (c *Conn) Subscribe(ctx context.Context, serviceUUID, characteristicUUID string, emit func([]byte)) (stop func(), err error) {
	characteristic, err := c.characteristic(ctx, serviceUUID, characteristicUUID)
	if err != nil {
		return nil, err
	}
	var mu sync.Mutex
	stopped := false
	err = run(ctx, func() error {
		return translate("enable notifications", characteristic.EnableNotifications(func(value []byte) {
			mu.Lock()
			defer mu.Unlock()
			if !stopped {
				emit(append([]byte(nil), value...))
			}
		}), domain.CodeInternal)
	})
	if err != nil {
		return nil, err
	}
	return func() {
		mu.Lock()
		stopped = true
		mu.Unlock()
		stopNotifications(characteristic)
	}, nil
}

// run calls f and returns its error, or the context's error if the context
// ends first. tinygo's GATT calls can't be interrupted, so f keeps running in
// the background in that case.
func run(ctx context.Context, f func() error) error {
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}
