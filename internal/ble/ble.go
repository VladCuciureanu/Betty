package ble

import (
	"context"
	"encoding/hex"
	"slices"
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
	MTU(context.Context, string) (int, error)
	DeviceInformation(context.Context, string) (map[string]string, error)
}

type TinyGo struct {
	adapter   *bluetooth.Adapter
	once      sync.Once
	enableErr error

	mu          sync.Mutex
	disconnects map[string][]chan struct{}
}

func New() *TinyGo { return &TinyGo{adapter: bluetooth.DefaultAdapter} }

func (b *TinyGo) enable() error {
	b.once.Do(func() {
		b.adapter.SetConnectHandler(b.connectionChanged)
		b.enableErr = b.adapter.Enable()
	})
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

// advertisement accumulates every advertisement and scan response seen for
// one device. Advertising packets often carry only part of the data, so a
// later packet without a name or services must not erase an earlier one.
type advertisement struct {
	address      string
	name         string
	rssi         int16
	services     []string
	manufacturer map[uint16]string
	serviceData  map[string]string
	lastSeen     time.Time
}

func (a *advertisement) merge(result bluetooth.ScanResult, now time.Time) {
	if name := result.LocalName(); name != "" {
		a.name = name
	}
	a.rssi = result.RSSI
	a.lastSeen = now
	for _, uuid := range result.ServiceUUIDs() {
		if value := uuid.String(); !slices.Contains(a.services, value) {
			a.services = append(a.services, value)
		}
	}
	for _, item := range result.ManufacturerData() {
		a.manufacturer[item.CompanyID] = hex.EncodeToString(item.Data)
	}
	for _, item := range result.ServiceData() {
		a.serviceData[item.UUID.String()] = hex.EncodeToString(item.Data)
	}
}

func (a *advertisement) device() domain.Device {
	name := a.name
	if name == "" {
		name = a.address
	}
	rssi, lastSeen := a.rssi, a.lastSeen
	d := domain.Device{ID: "ble:" + a.address, Name: name, RSSI: &rssi, Services: slices.Clone(a.services), LastSeenAt: &lastSeen,
		Endpoints: []domain.Endpoint{{ID: a.address, Address: a.address, Transport: domain.TransportBLE}}}
	for _, id := range sortedKeys(a.manufacturer) {
		d.ManufacturerData = append(d.ManufacturerData, domain.ManufacturerData{CompanyID: id, Data: a.manufacturer[id]})
	}
	for _, uuid := range sortedKeys(a.serviceData) {
		d.ServiceData = append(d.ServiceData, domain.ServiceData{UUID: uuid, Data: a.serviceData[uuid]})
	}
	return d
}

func sortedKeys[K uint16 | string, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func (b *TinyGo) Scan(ctx context.Context) ([]domain.Device, error) {
	if err := b.enableContext(ctx); err != nil {
		return nil, err
	}
	var mu sync.Mutex
	byAddress := map[string]*advertisement{}
	done := make(chan error, 1)
	go func() {
		done <- b.adapter.Scan(func(_ *bluetooth.Adapter, result bluetooth.ScanResult) {
			address := result.Address.String()
			now := time.Now().UTC()
			mu.Lock()
			defer mu.Unlock()
			a, ok := byAddress[address]
			if !ok {
				a = &advertisement{address: address, manufacturer: map[uint16]string{}, serviceData: map[string]string{}}
				byAddress[address] = a
			}
			a.merge(result, now)
		})
	}()
	select {
	case <-ctx.Done():
		_ = b.adapter.StopScan()
		<-done
	case err := <-done:
		if err != nil {
			return nil, translate("scan", err, domain.CodeUnavailable)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	out := make([]domain.Device, 0, len(byAddress))
	for _, a := range byAddress {
		out = append(out, a.device())
	}
	return out, nil
}

func (b *TinyGo) connect(ctx context.Context, address string) (bluetooth.Device, error) {
	addr, ok := parseAddress(address)
	if !ok {
		return bluetooth.Device{}, domain.NewError(domain.CodeUsage, "invalid BLE address %q", address)
	}
	if err := b.enableContext(ctx); err != nil {
		return bluetooth.Device{}, err
	}
	type result struct {
		device bluetooth.Device
		err    error
	}
	ch := make(chan result, 1)
	go func() { d, err := b.adapter.Connect(addr, bluetooth.ConnectionParams{}); ch <- result{d, err} }()
	select {
	case <-ctx.Done():
		// tinygo's Connect cannot be interrupted. If it still succeeds, nobody
		// owns the connection, so close it as soon as it arrives.
		go func() {
			if r := <-ch; r.err == nil {
				_ = r.device.Disconnect()
			}
		}()
		return bluetooth.Device{}, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return bluetooth.Device{}, translate("connect", r.err, domain.CodeDisconnected)
		}
		return r.device, nil
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
		return nil, translate("service discovery", err, domain.CodeInternal)
	}
	out := make([]Service, 0, len(services))
	for _, s := range services {
		out = append(out, Service{UUID: s.UUID().String()})
	}
	return out, nil
}

func (b *TinyGo) Characteristics(ctx context.Context, address, serviceUUID string) ([]Characteristic, error) {
	filter, err := uuidFilter(serviceUUID)
	if err != nil {
		return nil, err
	}
	d, err := b.connect(ctx, address)
	if err != nil {
		return nil, err
	}
	defer d.Disconnect()
	services, err := d.DiscoverServices(filter)
	if err != nil {
		return nil, translate("service discovery", err, domain.CodeInternal)
	}
	var out []Characteristic
	for _, s := range services {
		chars, e := s.DiscoverCharacteristics(nil)
		if e != nil {
			return nil, translate("characteristic discovery", e, domain.CodeInternal)
		}
		for _, c := range chars {
			out = append(out, Characteristic{ServiceUUID: s.UUID().String(), UUID: c.UUID().String()})
		}
	}
	return out, nil
}

// splitCharacteristic splits "service/characteristic" into its UUIDs. A bare
// characteristic UUID names no service.
func splitCharacteristic(spec string) (service, characteristic string) {
	if service, characteristic, ok := strings.Cut(spec, "/"); ok {
		return service, characteristic
	}
	return "", spec
}

// Read reads a characteristic, given as a UUID or "service/characteristic".
// A UUID found in several services is ambiguous unless the service is given.
func (b *TinyGo) Read(ctx context.Context, address, spec string) ([]byte, error) {
	conn, err := b.Open(ctx, address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	service, characteristic := splitCharacteristic(spec)
	return conn.Read(ctx, service, characteristic)
}

// Write writes a characteristic, given as in Read.
func (b *TinyGo) Write(ctx context.Context, address, spec string, value []byte, withoutResponse bool) error {
	conn, err := b.Open(ctx, address)
	if err != nil {
		return err
	}
	defer conn.Close()
	service, characteristic := splitCharacteristic(spec)
	return conn.Write(ctx, service, characteristic, value, withoutResponse)
}

// Watch calls emit with each notification of a characteristic, given as in
// Read, until ctx ends, emit fails, or the link drops.
func (b *TinyGo) Watch(ctx context.Context, address, spec string, emit func([]byte) error) error {
	conn, err := b.Open(ctx, address)
	if err != nil {
		return err
	}
	defer conn.Close()
	errCh := make(chan error, 1)
	service, characteristic := splitCharacteristic(spec)
	stop, err := conn.Subscribe(ctx, service, characteristic, func(value []byte) {
		if e := emit(value); e != nil {
			select {
			case errCh <- e:
			default:
			}
		}
	})
	if err != nil {
		return err
	}
	defer stop()
	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	case <-conn.Lost():
		return domain.NewError(domain.CodeDisconnected, "BLE device %s disconnected", address)
	}
}

// MTU reports the ATT MTU the operating system negotiated for a connection
// to the device. No platform lets an app choose it.
func (b *TinyGo) MTU(ctx context.Context, address string) (int, error) {
	conn, err := b.Open(ctx, address)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	return conn.MTU(ctx)
}

// watchDisconnect returns a channel that is closed when the device at address
// disconnects. tinygo reports disconnects through the adapter's connect
// handler on macOS and Windows; Linux needs a D-Bus property watch.
func (b *TinyGo) watchDisconnect(address string) (<-chan struct{}, func()) {
	key := strings.ToUpper(address)
	lost := make(chan struct{})
	b.mu.Lock()
	if b.disconnects == nil {
		b.disconnects = map[string][]chan struct{}{}
	}
	b.disconnects[key] = append(b.disconnects[key], lost)
	b.mu.Unlock()
	stopPlatform := watchLinkLoss(address, func() { b.notifyDisconnect(key) })
	return lost, func() {
		stopPlatform()
		b.mu.Lock()
		defer b.mu.Unlock()
		b.disconnects[key] = slices.DeleteFunc(b.disconnects[key], func(ch chan struct{}) bool { return ch == lost })
		if len(b.disconnects[key]) == 0 {
			delete(b.disconnects, key)
		}
	}
}

func (b *TinyGo) connectionChanged(device bluetooth.Device, connected bool) {
	if !connected {
		b.notifyDisconnect(strings.ToUpper(device.Address.String()))
	}
}

func (b *TinyGo) notifyDisconnect(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.disconnects[key] {
		select {
		case <-ch:
		default:
			close(ch)
		}
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
		return nil, translate("service discovery", err, domain.CodeInternal)
	}
	chars, err := services[0].DiscoverCharacteristics(nil)
	if err != nil {
		return nil, translate("characteristic discovery", err, domain.CodeInternal)
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
