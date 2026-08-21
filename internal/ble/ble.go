package ble

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"

	"github.com/vladcuciureanu/betty/internal/domain"
)

type Service struct {
	UUID string `json:"uuid"`
}
type Characteristic struct {
	ServiceUUID string `json:"service_uuid"`
	UUID        string `json:"uuid"`
}

type Client interface {
	Scan(context.Context) ([]domain.Device, error)
	Services(context.Context, string) ([]Service, error)
	Characteristics(context.Context, string, string) ([]Characteristic, error)
	Read(context.Context, string, string) ([]byte, error)
	Write(context.Context, string, string, []byte, bool) error
	Watch(context.Context, string, string, func([]byte) error) error
	DeviceInformation(context.Context, string) (map[string]string, error)
}

type TinyGo struct {
	adapter   *bluetooth.Adapter
	once      sync.Once
	enableErr error
}

func New() Client { return &TinyGo{adapter: bluetooth.DefaultAdapter} }

func (b *TinyGo) enable() error {
	b.once.Do(func() { b.enableErr = b.adapter.Enable() })
	if b.enableErr != nil {
		return domain.NewError(domain.CodeUnavailable, "Bluetooth adapter unavailable: %v", b.enableErr)
	}
	return nil
}

func (b *TinyGo) enableContext(ctx context.Context) error {
	done := make(chan error, 1)
	go func() { done <- b.enable() }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func (b *TinyGo) Scan(ctx context.Context) ([]domain.Device, error) {
	if err := b.enableContext(ctx); err != nil {
		return nil, err
	}
	var mu sync.Mutex
	byID := map[string]domain.Device{}
	done := make(chan error, 1)
	go func() {
		done <- b.adapter.Scan(func(_ *bluetooth.Adapter, result bluetooth.ScanResult) {
			id := "ble:" + result.Address.String()
			name := result.LocalName()
			if name == "" {
				name = result.Address.String()
			}
			rssi := result.RSSI
			services := make([]string, 0, len(result.ServiceUUIDs()))
			for _, uuid := range result.ServiceUUIDs() {
				services = append(services, uuid.String())
			}
			now := time.Now().UTC()
			mu.Lock()
			byID[id] = domain.Device{ID: id, Name: name, RSSI: &rssi, Services: services, LastSeenAt: &now,
				Endpoints: []domain.Endpoint{{ID: result.Address.String(), Address: result.Address.String(), Transport: domain.TransportBLE}}}
			mu.Unlock()
		})
	}()
	select {
	case <-ctx.Done():
		_ = b.adapter.StopScan()
		<-done
	case err := <-done:
		if err != nil {
			return nil, fmt.Errorf("BLE scan: %w", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	out := make([]domain.Device, 0, len(byID))
	for _, d := range byID {
		out = append(out, d)
	}
	return out, nil
}

func (b *TinyGo) connect(ctx context.Context, address string) (bluetooth.Device, error) {
	if err := b.enableContext(ctx); err != nil {
		return bluetooth.Device{}, err
	}
	var addr bluetooth.Address
	addr.Set(address)
	type result struct {
		device bluetooth.Device
		err    error
	}
	ch := make(chan result, 1)
	go func() { d, err := b.adapter.Connect(addr, bluetooth.ConnectionParams{}); ch <- result{d, err} }()
	select {
	case <-ctx.Done():
		return bluetooth.Device{}, ctx.Err()
	case r := <-ch:
		return r.device, r.err
	}
}

func (b *TinyGo) Services(ctx context.Context, address string) ([]Service, error) {
	d, err := b.connect(ctx, address)
	if err != nil {
		return nil, err
	}
	defer d.Disconnect()
	services, err := d.DiscoverServices(nil)
	if err != nil {
		return nil, err
	}
	out := make([]Service, 0, len(services))
	for _, s := range services {
		out = append(out, Service{UUID: s.UUID().String()})
	}
	return out, nil
}

func (b *TinyGo) Characteristics(ctx context.Context, address, serviceUUID string) ([]Characteristic, error) {
	d, err := b.connect(ctx, address)
	if err != nil {
		return nil, err
	}
	defer d.Disconnect()
	filter, err := uuidFilter(serviceUUID)
	if err != nil {
		return nil, err
	}
	services, err := d.DiscoverServices(filter)
	if err != nil {
		return nil, err
	}
	var out []Characteristic
	for _, s := range services {
		chars, e := s.DiscoverCharacteristics(nil)
		if e != nil {
			return nil, e
		}
		for _, c := range chars {
			out = append(out, Characteristic{ServiceUUID: s.UUID().String(), UUID: c.UUID().String()})
		}
	}
	return out, nil
}

func (b *TinyGo) find(ctx context.Context, address, characteristicUUID string) (bluetooth.Device, bluetooth.DeviceCharacteristic, error) {
	d, err := b.connect(ctx, address)
	if err != nil {
		return d, bluetooth.DeviceCharacteristic{}, err
	}
	uuid, err := bluetooth.ParseUUID(characteristicUUID)
	if err != nil {
		d.Disconnect()
		return d, bluetooth.DeviceCharacteristic{}, domain.NewError(domain.CodeUsage, "invalid characteristic UUID %q", characteristicUUID)
	}
	services, err := d.DiscoverServices(nil)
	if err != nil {
		d.Disconnect()
		return d, bluetooth.DeviceCharacteristic{}, err
	}
	for _, s := range services {
		chars, e := s.DiscoverCharacteristics([]bluetooth.UUID{uuid})
		if e != nil {
			continue
		}
		if len(chars) > 0 {
			return d, chars[0], nil
		}
	}
	d.Disconnect()
	return d, bluetooth.DeviceCharacteristic{}, domain.NewError(domain.CodeNotFound, "characteristic %s not found", characteristicUUID)
}

func (b *TinyGo) Read(ctx context.Context, address, uuid string) ([]byte, error) {
	d, c, err := b.find(ctx, address, uuid)
	if err != nil {
		return nil, err
	}
	defer d.Disconnect()
	buf := make([]byte, 512)
	n, err := c.Read(buf)
	return buf[:n], err
}

func (b *TinyGo) Write(ctx context.Context, address, uuid string, value []byte, withoutResponse bool) error {
	d, c, err := b.find(ctx, address, uuid)
	if err != nil {
		return err
	}
	defer d.Disconnect()
	if withoutResponse {
		_, err = c.WriteWithoutResponse(value)
	} else {
		_, err = c.Write(value)
	}
	return err
}

func (b *TinyGo) Watch(ctx context.Context, address, uuid string, emit func([]byte) error) error {
	d, c, err := b.find(ctx, address, uuid)
	if err != nil {
		return err
	}
	defer d.Disconnect()
	errCh := make(chan error, 1)
	if err := c.EnableNotifications(func(value []byte) {
		copied := append([]byte(nil), value...)
		if e := emit(copied); e != nil {
			select {
			case errCh <- e:
			default:
			}
		}
	}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

func (b *TinyGo) DeviceInformation(ctx context.Context, address string) (map[string]string, error) {
	d, err := b.connect(ctx, address)
	if err != nil {
		return nil, err
	}
	defer d.Disconnect()
	serviceUUID, _ := bluetooth.ParseUUID("180a")
	services, err := d.DiscoverServices([]bluetooth.UUID{serviceUUID})
	if err != nil || len(services) == 0 {
		return nil, err
	}
	chars, err := services[0].DiscoverCharacteristics(nil)
	if err != nil {
		return nil, err
	}
	names := map[string]string{"2a29": "manufacturer", "2a24": "model", "2a25": "serial_number", "2a27": "hardware_revision", "2a26": "firmware_revision", "2a28": "software_revision"}
	out := map[string]string{}
	for _, c := range chars {
		name, ok := names[strings.ToLower(shortUUID(c.UUID().String()))]
		if !ok {
			continue
		}
		buf := make([]byte, 512)
		n, readErr := c.Read(buf)
		if readErr == nil {
			out[name] = strings.TrimRight(string(buf[:n]), "\x00")
		}
	}
	return out, nil
}

func shortUUID(value string) string {
	value = strings.ReplaceAll(value, "-", "")
	if len(value) == 32 && strings.HasSuffix(value, "00001000800000805f9b34fb") {
		return value[4:8]
	}
	return value
}

func uuidFilter(value string) ([]bluetooth.UUID, error) {
	if value == "" {
		return nil, nil
	}
	uuid, err := bluetooth.ParseUUID(value)
	if err != nil {
		return nil, domain.NewError(domain.CodeUsage, "invalid service UUID %q", value)
	}
	return []bluetooth.UUID{uuid}, nil
}
