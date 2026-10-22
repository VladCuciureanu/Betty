package driver

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	protocol "github.com/vladcuciureanu/betty/driver"
	"github.com/vladcuciureanu/betty/internal/domain"
)

// hostSession is the state one driver request may touch through host calls:
// the devices Betty supplied, and the channels, GATT connections and
// subscriptions the driver opened. Betty releases all of it when the request
// finishes.
type hostSession struct {
	devices       map[string]protocol.Device
	channels      map[channelKey]bool
	gatt          map[string]GATTConnection
	subscriptions map[string]*subscription
	hid           map[string]bool
	next          int
}

// channelKey names a channel by transport as well as ID, since the L2CAP and
// RFCOMM hosts allocate IDs independently.
type channelKey struct{ transport, id string }

func newHostSession(devices []protocol.Device) *hostSession {
	s := &hostSession{devices: map[string]protocol.Device{}, channels: map[channelKey]bool{}, gatt: map[string]GATTConnection{}, subscriptions: map[string]*subscription{}, hid: map[string]bool{}}
	for _, device := range devices {
		s.devices[device.ID] = device
	}
	return s
}

func (s *hostSession) device(id string) (protocol.Device, error) {
	device, ok := s.devices[id]
	if !ok {
		return device, domain.NewError(domain.CodePermission, "driver cannot access device %q", id)
	}
	return device, nil
}

// close releases everything the driver left open. Each release gets its own
// deadline because the request's context may already be done.
func (m *Manager) closeSession(s *hostSession) {
	release := func(f func(context.Context) error) {
		ctx, cancel := context.WithTimeout(context.Background(), channelCloseTimeout)
		defer cancel()
		_ = f(ctx)
	}
	for key := range s.channels {
		release(func(ctx context.Context) error { return m.stream(key.transport).close(ctx, key.id) })
	}
	for _, sub := range s.subscriptions {
		sub.stop()
	}
	for _, conn := range s.gatt {
		_ = conn.Close()
	}
	for id := range s.hid {
		release(func(ctx context.Context) error { return m.hid.CloseHID(ctx, id) })
	}
}

// subscription queues the values one GATT subscription receives until the
// driver asks for them.
type subscription struct {
	stop func()
	lost <-chan struct{}

	mu     sync.Mutex
	queue  [][]byte
	signal chan struct{}
}

const subscriptionQueue = 64

func (sub *subscription) push(value []byte) {
	sub.mu.Lock()
	if len(sub.queue) == subscriptionQueue {
		sub.queue = sub.queue[1:]
	}
	sub.queue = append(sub.queue, value)
	sub.mu.Unlock()
	select {
	case sub.signal <- struct{}{}:
	default:
	}
}

func (sub *subscription) pop() ([]byte, bool) {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if len(sub.queue) == 0 {
		return nil, false
	}
	value := sub.queue[0]
	sub.queue = sub.queue[1:]
	return value, true
}

func (sub *subscription) wait(ctx context.Context) ([]byte, error) {
	for {
		if value, ok := sub.pop(); ok {
			return value, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-sub.lost:
			if value, ok := sub.pop(); ok {
				return value, nil
			}
			return nil, domain.NewError(domain.CodeDisconnected, "BLE device disconnected")
		case <-sub.signal:
		}
	}
}

// streamHost is the part of the L2CAP and RFCOMM hosts that is the same for
// both: an open channel is read, written and closed by ID.
type streamHost struct {
	name  string
	read  func(context.Context, string, int) ([]byte, error)
	write func(context.Context, string, []byte) (int, error)
	close func(context.Context, string) error
}

func (m *Manager) stream(transport string) streamHost {
	if transport == protocol.PermissionRFCOMM {
		return streamHost{"RFCOMM", m.rfcomm.ReadRFCOMM, m.rfcomm.WriteRFCOMM, m.rfcomm.CloseRFCOMM}
	}
	return streamHost{"L2CAP", m.host.ReadL2CAP, m.host.WriteL2CAP, m.host.CloseL2CAP}
}

// hostPermissions maps each host-call namespace to the manifest permission it
// requires.
var hostPermissions = map[string]string{
	"l2cap":   protocol.PermissionL2CAP,
	"rfcomm":  protocol.PermissionRFCOMM,
	"storage": protocol.PermissionStorage,
	"gatt":    protocol.PermissionGATT,
	"hid":     protocol.PermissionHID,
}

func (m *Manager) handleHost(ctx context.Context, driver protocol.Manifest, message protocol.Message, s *hostSession) (json.RawMessage, error) {
	namespace, operation, _ := strings.Cut(message.Method, ".")
	permission, known := hostPermissions[namespace]
	if !known {
		return nil, domain.NewError(domain.CodeUnsupported, "unsupported host method %q", message.Method)
	}
	if !hasPermission(driver, permission) {
		return nil, domain.NewError(domain.CodePermission, "driver %s lacks %s permission", driver.ID, permission)
	}
	var result any
	var err error
	switch namespace {
	case "l2cap", "rfcomm":
		result, err = m.handleStream(ctx, namespace, operation, message.Params, s)
	case "storage":
		result, err = m.handleStorage(driver, operation, message.Params)
	case "gatt":
		result, err = m.handleGATT(ctx, operation, message.Params, s)
	case "hid":
		result, err = m.handleHID(ctx, operation, message.Params, s)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func decode(params json.RawMessage, into any) error {
	if err := json.Unmarshal(params, into); err != nil {
		return domain.NewError(domain.CodeUsage, "invalid host call parameters: %v", err)
	}
	return nil
}

// readContext applies a host call's optional timeout_ms.
func readContext(ctx context.Context, timeoutMS int) (context.Context, context.CancelFunc, error) {
	if timeoutMS <= 0 {
		return ctx, func() {}, nil
	}
	if timeoutMS > 30000 {
		return nil, nil, domain.NewError(domain.CodeUsage, "timeout_ms must not exceed 30000")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	return ctx, cancel, nil
}

func (m *Manager) handleStream(ctx context.Context, transport, operation string, params json.RawMessage, s *hostSession) (any, error) {
	host := m.stream(transport)
	if operation == "open" {
		var deviceID, channel string
		var err error
		if transport == protocol.PermissionRFCOMM {
			var p protocol.RFCOMMOpenParams
			if err := decode(params, &p); err != nil {
				return nil, err
			}
			if _, err := s.device(p.DeviceID); err != nil {
				return nil, err
			}
			deviceID = p.DeviceID
			channel, err = m.rfcomm.OpenRFCOMM(ctx, deviceID, p.ServiceUUID)
		} else {
			var p protocol.L2CAPOpenParams
			if err := decode(params, &p); err != nil {
				return nil, err
			}
			if _, err := s.device(p.DeviceID); err != nil {
				return nil, err
			}
			deviceID = p.DeviceID
			channel, err = m.host.OpenL2CAP(ctx, deviceID, p.PSM)
		}
		if err != nil {
			return nil, err
		}
		s.channels[channelKey{transport, channel}] = true
		return map[string]string{"channel_id": channel}, nil
	}
	// Read, write and close take the same parameters on both transports.
	var p struct {
		ChannelID string `json:"channel_id"`
		MaxBytes  int    `json:"max_bytes"`
		TimeoutMS int    `json:"timeout_ms"`
		Data      string `json:"data"`
	}
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	key := channelKey{transport, p.ChannelID}
	if !s.channels[key] {
		return nil, domain.NewError(domain.CodePermission, "driver does not own %s channel %q", host.name, p.ChannelID)
	}
	switch operation {
	case "read":
		if p.MaxBytes < 1 || p.MaxBytes > 65535 {
			return nil, domain.NewError(domain.CodeUsage, "max_bytes must be between 1 and 65535")
		}
		readCtx, cancel, err := readContext(ctx, p.TimeoutMS)
		if err != nil {
			return nil, err
		}
		defer cancel()
		data, err := host.read(readCtx, p.ChannelID, p.MaxBytes)
		if err != nil {
			return nil, err
		}
		return map[string]string{"data": hex.EncodeToString(data)}, nil
	case "write":
		data, err := hex.DecodeString(p.Data)
		if err != nil {
			return nil, domain.NewError(domain.CodeUsage, "invalid hexadecimal %s data", host.name)
		}
		written, err := host.write(ctx, p.ChannelID, data)
		if err != nil {
			return nil, err
		}
		return map[string]int{"written": written}, nil
	case "close":
		if err := host.close(ctx, p.ChannelID); err != nil {
			return nil, err
		}
		delete(s.channels, key)
		return map[string]bool{"closed": true}, nil
	}
	return nil, domain.NewError(domain.CodeUnsupported, "unsupported host method %q", transport+"."+operation)
}

func (m *Manager) handleHID(ctx context.Context, operation string, params json.RawMessage, s *hostSession) (any, error) {
	if operation == "open" {
		var p protocol.HIDOpenParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		device, err := s.device(p.DeviceID)
		if err != nil {
			return nil, err
		}
		address := ""
		for _, endpoint := range device.Endpoints {
			if endpoint.Address != "" {
				address = endpoint.Address
				break
			}
		}
		if address == "" {
			return nil, domain.NewError(domain.CodeUnsupported, "device %q has no Bluetooth address", p.DeviceID)
		}
		collection, err := m.hid.OpenHID(ctx, address, p.UsagePage, p.Usage)
		if err != nil {
			return nil, err
		}
		s.hid[collection.HandleID] = true
		return collection, nil
	}
	var p struct {
		HandleID  string `json:"handle_id"`
		TimeoutMS int    `json:"timeout_ms"`
		ReportID  uint8  `json:"report_id"`
	}
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	if !s.hid[p.HandleID] {
		return nil, domain.NewError(domain.CodePermission, "driver does not own HID handle %q", p.HandleID)
	}
	switch operation {
	case "read":
		readCtx, cancel, err := readContext(ctx, p.TimeoutMS)
		if err != nil {
			return nil, err
		}
		defer cancel()
		data, err := m.hid.ReadHID(readCtx, p.HandleID)
		if err != nil {
			return nil, err
		}
		return map[string]string{"data": hex.EncodeToString(data)}, nil
	case "get_feature":
		data, err := m.hid.GetHIDFeature(ctx, p.HandleID, p.ReportID)
		if err != nil {
			return nil, err
		}
		return map[string]string{"data": hex.EncodeToString(data)}, nil
	case "close":
		if err := m.hid.CloseHID(ctx, p.HandleID); err != nil {
			return nil, err
		}
		delete(s.hid, p.HandleID)
		return map[string]bool{"closed": true}, nil
	}
	return nil, domain.NewError(domain.CodeUnsupported, "unsupported host method %q", "hid."+operation)
}

func (m *Manager) handleStorage(driver protocol.Manifest, operation string, params json.RawMessage) (any, error) {
	switch operation {
	case "get":
		var p protocol.StorageGetParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		value, found, err := m.storageGet(driver, p.Key)
		if err != nil {
			return nil, err
		}
		return map[string]any{"value": value, "found": found}, nil
	case "set":
		var p protocol.StorageSetParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		if err := m.storageSet(driver, p.Key, p.Value); err != nil {
			return nil, err
		}
		return map[string]bool{"stored": true}, nil
	}
	return nil, domain.NewError(domain.CodeUnsupported, "unsupported host method %q", "storage."+operation)
}

// gattConnection returns the session's connection to a device, connecting
// through its BLE endpoint on first use.
func (m *Manager) gattConnection(ctx context.Context, s *hostSession, deviceID string) (GATTConnection, error) {
	if conn, ok := s.gatt[deviceID]; ok {
		return conn, nil
	}
	device, err := s.device(deviceID)
	if err != nil {
		return nil, err
	}
	address := ""
	for _, endpoint := range device.Endpoints {
		if endpoint.Transport == string(domain.TransportBLE) && endpoint.Address != "" {
			address = endpoint.Address
			break
		}
	}
	if address == "" {
		return nil, domain.NewError(domain.CodeUnsupported, "device %q has no BLE endpoint", deviceID)
	}
	conn, err := m.gattHost.OpenGATT(ctx, address)
	if err != nil {
		return nil, err
	}
	s.gatt[deviceID] = conn
	return conn, nil
}

func (m *Manager) handleGATT(ctx context.Context, operation string, params json.RawMessage, s *hostSession) (any, error) {
	switch operation {
	case "services":
		var p protocol.GATTServicesParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		conn, err := m.gattConnection(ctx, s, p.DeviceID)
		if err != nil {
			return nil, err
		}
		services, err := conn.Services(ctx)
		if err != nil {
			return nil, err
		}
		return map[string][]protocol.GATTService{"services": services}, nil
	case "read", "subscribe":
		var p protocol.GATTCharacteristicParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		conn, err := m.gattConnection(ctx, s, p.DeviceID)
		if err != nil {
			return nil, err
		}
		if operation == "read" {
			data, err := conn.Read(ctx, p.ServiceUUID, p.CharacteristicUUID)
			if err != nil {
				return nil, err
			}
			return map[string]string{"data": hex.EncodeToString(data)}, nil
		}
		sub := &subscription{lost: conn.Lost(), signal: make(chan struct{}, 1)}
		stop, err := conn.Subscribe(ctx, p.ServiceUUID, p.CharacteristicUUID, sub.push)
		if err != nil {
			return nil, err
		}
		sub.stop = stop
		s.next++
		id := "gatt-" + strconv.Itoa(s.next)
		s.subscriptions[id] = sub
		return map[string]string{"subscription_id": id}, nil
	case "write":
		var p protocol.GATTWriteParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		data, err := hex.DecodeString(p.Data)
		if err != nil {
			return nil, domain.NewError(domain.CodeUsage, "invalid hexadecimal GATT data")
		}
		conn, err := m.gattConnection(ctx, s, p.DeviceID)
		if err != nil {
			return nil, err
		}
		if err := conn.Write(ctx, p.ServiceUUID, p.CharacteristicUUID, data, p.WithoutResponse); err != nil {
			return nil, err
		}
		return map[string]bool{"written": true}, nil
	case "next":
		var p protocol.GATTNextParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		sub, ok := s.subscriptions[p.SubscriptionID]
		if !ok {
			return nil, domain.NewError(domain.CodePermission, "driver does not own subscription %q", p.SubscriptionID)
		}
		waitCtx, cancel, err := readContext(ctx, p.TimeoutMS)
		if err != nil {
			return nil, err
		}
		defer cancel()
		data, err := sub.wait(waitCtx)
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, domain.NewError(domain.CodeTimeout, "no GATT value arrived within %d ms", p.TimeoutMS)
		}
		if err != nil {
			return nil, err
		}
		return map[string]string{"data": hex.EncodeToString(data)}, nil
	case "unsubscribe":
		var p protocol.GATTSubscriptionParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		sub, ok := s.subscriptions[p.SubscriptionID]
		if !ok {
			return nil, domain.NewError(domain.CodePermission, "driver does not own subscription %q", p.SubscriptionID)
		}
		sub.stop()
		delete(s.subscriptions, p.SubscriptionID)
		return map[string]bool{"unsubscribed": true}, nil
	}
	return nil, domain.NewError(domain.CodeUnsupported, "unsupported host method %q", "gatt."+operation)
}
