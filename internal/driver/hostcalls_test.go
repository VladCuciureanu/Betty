package driver

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	protocol "github.com/vladcuciureanu/betty/driver"
	"github.com/vladcuciureanu/betty/internal/domain"
)

type fakeGATTHost struct {
	opened []string
	conn   *fakeGATTConn
}

func (h *fakeGATTHost) OpenGATT(_ context.Context, address string) (GATTConnection, error) {
	h.opened = append(h.opened, address)
	return h.conn, nil
}

type fakeGATTConn struct {
	mu      sync.Mutex
	written map[string]string
	emit    func([]byte)
	stopped bool
	closed  bool
	lost    chan struct{}
}

func newFakeGATTConn() *fakeGATTConn {
	return &fakeGATTConn{written: map[string]string{}, lost: make(chan struct{})}
}

func (c *fakeGATTConn) Services(context.Context) ([]protocol.GATTService, error) {
	return []protocol.GATTService{{UUID: "180f", Characteristics: []string{"2a19"}}}, nil
}

func (c *fakeGATTConn) Read(_ context.Context, _, characteristic string) ([]byte, error) {
	if characteristic != "2a19" {
		return nil, domain.NewError(domain.CodeNotFound, "characteristic %s not found", characteristic)
	}
	return []byte{0x55}, nil
}

func (c *fakeGATTConn) Write(_ context.Context, _, characteristic string, data []byte, _ bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.written[characteristic] = hex.EncodeToString(data)
	return nil
}

func (c *fakeGATTConn) Subscribe(_ context.Context, _, _ string, emit func([]byte)) (func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.emit = emit
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.stopped = true
	}, nil
}

func (c *fakeGATTConn) Lost() <-chan struct{} { return c.lost }

func (c *fakeGATTConn) Close() error {
	c.closed = true
	return nil
}

func gattTest(t *testing.T) (*Manager, *fakeGATTHost, *hostSession, protocol.Manifest) {
	t.Helper()
	m := NewAt(t.TempDir())
	host := &fakeGATTHost{conn: newFakeGATTConn()}
	m.SetGATTHost(host)
	session := newHostSession([]protocol.Device{
		{ID: "sensor", Endpoints: []protocol.Endpoint{{ID: "AA:BB:CC:DD:EE:FF", Transport: "ble", Address: "AA:BB:CC:DD:EE:FF"}}},
		{ID: "classic", Endpoints: []protocol.Endpoint{{ID: "11:22:33:44:55:66", Transport: "classic", Address: "11:22:33:44:55:66"}}},
	})
	return m, host, session, protocol.Manifest{ID: "example/sensor", Permissions: []string{protocol.PermissionGATT}}
}

func call(t *testing.T, m *Manager, driver protocol.Manifest, s *hostSession, method string, params any) (map[string]any, error) {
	t.Helper()
	raw, _ := json.Marshal(params)
	result, err := m.handleHost(context.Background(), driver, protocol.Message{Method: method, Params: raw}, s)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

func TestGATTHostCallsShareOneConnection(t *testing.T) {
	m, host, session, driver := gattTest(t)
	services, err := call(t, m, driver, session, protocol.HostGATTServices, protocol.GATTServicesParams{DeviceID: "sensor"})
	if err != nil || len(services["services"].([]any)) != 1 {
		t.Fatalf("services=%v err=%v", services, err)
	}
	read, err := call(t, m, driver, session, protocol.HostGATTRead, protocol.GATTCharacteristicParams{DeviceID: "sensor", CharacteristicUUID: "2a19"})
	if err != nil || read["data"] != "55" {
		t.Fatalf("read=%v err=%v", read, err)
	}
	write := protocol.GATTWriteParams{GATTCharacteristicParams: protocol.GATTCharacteristicParams{DeviceID: "sensor", CharacteristicUUID: "2a06"}, Data: "01"}
	if _, err := call(t, m, driver, session, protocol.HostGATTWrite, write); err != nil || host.conn.written["2a06"] != "01" {
		t.Fatalf("written=%v err=%v", host.conn.written, err)
	}
	if len(host.opened) != 1 || host.opened[0] != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("opened=%v", host.opened)
	}
	m.closeSession(session)
	if !host.conn.closed {
		t.Fatal("the connection was left open")
	}
}

func TestGATTSubscriptionQueuesValues(t *testing.T) {
	m, host, session, driver := gattTest(t)
	sub, err := call(t, m, driver, session, protocol.HostGATTSubscribe, protocol.GATTCharacteristicParams{DeviceID: "sensor", CharacteristicUUID: "2a37"})
	if err != nil {
		t.Fatal(err)
	}
	id := sub["subscription_id"].(string)
	for i := range subscriptionQueue + 2 {
		host.conn.emit([]byte{byte(i)})
	}
	// The oldest two values were dropped.
	next, err := call(t, m, driver, session, protocol.HostGATTNext, protocol.GATTNextParams{SubscriptionID: id, TimeoutMS: 100})
	if err != nil || next["data"] != "02" {
		t.Fatalf("next=%v err=%v", next, err)
	}
	for range subscriptionQueue - 1 {
		if _, err := call(t, m, driver, session, protocol.HostGATTNext, protocol.GATTNextParams{SubscriptionID: id, TimeoutMS: 100}); err != nil {
			t.Fatal(err)
		}
	}
	_, err = call(t, m, driver, session, protocol.HostGATTNext, protocol.GATTNextParams{SubscriptionID: id, TimeoutMS: 20})
	if domain.AsError(err).Code != domain.CodeTimeout {
		t.Fatalf("an empty queue must time out: %v", err)
	}
	if _, err := call(t, m, driver, session, protocol.HostGATTUnsubscribe, protocol.GATTSubscriptionParams{SubscriptionID: id}); err != nil || !host.conn.stopped {
		t.Fatalf("unsubscribe err=%v stopped=%t", err, host.conn.stopped)
	}
	if _, err := call(t, m, driver, session, protocol.HostGATTNext, protocol.GATTNextParams{SubscriptionID: id}); domain.AsError(err).Code != domain.CodePermission {
		t.Fatalf("a stopped subscription must be gone: %v", err)
	}
}

func TestGATTNextReportsALostLink(t *testing.T) {
	m, host, session, driver := gattTest(t)
	sub, err := call(t, m, driver, session, protocol.HostGATTSubscribe, protocol.GATTCharacteristicParams{DeviceID: "sensor", CharacteristicUUID: "2a37"})
	if err != nil {
		t.Fatal(err)
	}
	close(host.conn.lost)
	_, err = call(t, m, driver, session, protocol.HostGATTNext, protocol.GATTNextParams{SubscriptionID: sub["subscription_id"].(string), TimeoutMS: 1000})
	if domain.AsError(err).Code != domain.CodeDisconnected {
		t.Fatalf("err=%v", err)
	}
	m.closeSession(session)
	if !host.conn.stopped {
		t.Fatal("the subscription was left running")
	}
}

func TestGATTHostCallsAreScoped(t *testing.T) {
	m, _, session, driver := gattTest(t)
	read := protocol.GATTCharacteristicParams{DeviceID: "sensor", CharacteristicUUID: "2a19"}
	if _, err := call(t, m, protocol.Manifest{ID: "example/denied"}, session, protocol.HostGATTRead, read); domain.AsError(err).Code != domain.CodePermission {
		t.Fatalf("a driver without the gatt permission must be refused: %v", err)
	}
	if _, err := call(t, m, driver, session, protocol.HostGATTRead, protocol.GATTCharacteristicParams{DeviceID: "stranger", CharacteristicUUID: "2a19"}); domain.AsError(err).Code != domain.CodePermission {
		t.Fatalf("a device outside the request must be refused: %v", err)
	}
	if _, err := call(t, m, driver, session, protocol.HostGATTRead, protocol.GATTCharacteristicParams{DeviceID: "classic", CharacteristicUUID: "2a19"}); err == nil || !strings.Contains(err.Error(), "no BLE endpoint") {
		t.Fatalf("a device without a BLE endpoint must be unsupported: %v", err)
	}
	if _, err := call(t, m, driver, session, "gatt.teleport", read); domain.AsError(err).Code != domain.CodeUnsupported {
		t.Fatalf("err=%v", err)
	}
}

func TestChannelsWithTheSameIDOnTwoTransportsStayApart(t *testing.T) {
	m := NewAt(t.TempDir())
	m.SetHost(&fakeHost{})
	m.SetRFCOMMHost(&fakeRFCOMMHost{})
	session := newHostSession([]protocol.Device{{ID: "device"}})
	driver := protocol.Manifest{ID: "example/both", Permissions: []string{protocol.PermissionL2CAP, protocol.PermissionRFCOMM}}
	if _, err := call(t, m, driver, session, protocol.HostL2CAPOpen, protocol.L2CAPOpenParams{DeviceID: "device", PSM: 25}); err != nil {
		t.Fatal(err)
	}
	// The L2CAP channel's ID must not grant access to an RFCOMM channel.
	if _, err := call(t, m, driver, session, protocol.HostRFCOMMWrite, protocol.RFCOMMWriteParams{ChannelID: "channel-1", Data: "00"}); domain.AsError(err).Code != domain.CodePermission {
		t.Fatalf("err=%v", err)
	}
}
