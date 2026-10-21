package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vladcuciureanu/betty/internal/ble"
	"github.com/vladcuciureanu/betty/internal/domain"
	"github.com/vladcuciureanu/betty/internal/hidbattery"
	"github.com/vladcuciureanu/betty/internal/output"
)

type fakeBackend struct {
	known     []domain.Device
	scanned   []domain.Device
	err       error
	scanErr   error
	scans     atomic.Int32
	mutations []string
}

func (b *fakeBackend) Capabilities() domain.Capabilities { return domain.Capabilities{} }
func (b *fakeBackend) Scan(context.Context) ([]domain.Device, error) {
	b.scans.Add(1)
	return b.scanned, b.scanErr
}
func (b *fakeBackend) List(context.Context) ([]domain.Device, error) { return b.known, b.err }
func (b *fakeBackend) Pair(_ context.Context, d domain.Device) error {
	b.mutations = append(b.mutations, "pair "+d.ID)
	return b.err
}
func (b *fakeBackend) Unpair(_ context.Context, d domain.Device) error {
	b.mutations = append(b.mutations, "unpair "+d.ID)
	return b.err
}
func (b *fakeBackend) Connect(_ context.Context, d domain.Device) error {
	b.mutations = append(b.mutations, "connect "+d.ID)
	return b.err
}
func (b *fakeBackend) Disconnect(_ context.Context, d domain.Device) error {
	b.mutations = append(b.mutations, "disconnect "+d.ID)
	return b.err
}

type fakeBLE struct {
	devices []domain.Device
	battery []byte
	info    map[string]string
	err     error
	scanErr error
	reads   atomic.Int32
}

func (b *fakeBLE) Scan(context.Context) ([]domain.Device, error) { return b.devices, b.scanErr }
func (*fakeBLE) Services(context.Context, string) ([]ble.Service, error) {
	return nil, nil
}
func (*fakeBLE) Characteristics(context.Context, string, string) ([]ble.Characteristic, error) {
	return nil, nil
}
func (b *fakeBLE) Read(context.Context, string, string) ([]byte, error) {
	b.reads.Add(1)
	return b.battery, b.err
}
func (*fakeBLE) Write(context.Context, string, string, []byte, bool) error       { return nil }
func (*fakeBLE) Watch(context.Context, string, string, func([]byte) error) error { return nil }
func (*fakeBLE) MTU(context.Context, string) (int, error)                        { return 247, nil }
func (b *fakeBLE) DeviceInformation(context.Context, string) (map[string]string, error) {
	return b.info, b.err
}

type fakeHID struct{ observations []hidbattery.Observation }

func (h fakeHID) Batteries(context.Context) ([]hidbattery.Observation, error) {
	return h.observations, nil
}

// harness runs bt against fakes and captures both streams.
type harness struct {
	backend *fakeBackend
	ble     *fakeBLE
	hid     hidbattery.Provider
	stdout  bytes.Buffer
	stderr  bytes.Buffer
}

func newHarness() *harness {
	return &harness{backend: &fakeBackend{}, ble: &fakeBLE{}}
}

func (h *harness) run(args ...string) int {
	h.stdout.Reset()
	h.stderr.Reset()
	return run(args, &h.stdout, &h.stderr, "test", func(a *application) {
		a.backend, a.ble, a.hid = h.backend, h.ble, h.hid
	})
}

// data decodes the single JSON envelope on stdout into v.
func (h *harness) data(t *testing.T, v any) {
	t.Helper()
	var envelope struct {
		SchemaVersion int             `json:"schema_version"`
		Data          json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(h.stdout.Bytes(), &envelope); err != nil || envelope.SchemaVersion != 1 {
		t.Fatalf("stdout is not one JSON envelope: %v\n%s", err, h.stdout.String())
	}
	if err := json.Unmarshal(envelope.Data, v); err != nil {
		t.Fatalf("data: %v\n%s", err, envelope.Data)
	}
}

func (h *harness) errorCode(t *testing.T) domain.ErrorCode {
	t.Helper()
	var envelope struct {
		Error domain.Error `json:"error"`
	}
	if err := json.Unmarshal(h.stderr.Bytes(), &envelope); err != nil {
		t.Fatalf("stderr is not a JSON error: %v\n%s", err, h.stderr.String())
	}
	return envelope.Error.Code
}

func classicDevice(id, name, address string) domain.Device {
	return domain.Device{ID: id, Name: name, Paired: true, Endpoints: []domain.Endpoint{{ID: address, Address: address, Transport: domain.TransportOS}}}
}

func bleDevice(address, name string) domain.Device {
	return domain.Device{ID: "ble:" + address, Name: name, Endpoints: []domain.Endpoint{{ID: address, Address: address, Transport: domain.TransportBLE}}}
}

func TestGetUsesKnownDevicesWithoutScanning(t *testing.T) {
	h := newHarness()
	h.backend.known = []domain.Device{bleDevice("AA:BB:CC:DD:EE:01", "Sensor")}
	h.ble.battery = []byte{89}
	if code := h.run("--json", "get", "Sensor", "battery"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr.String())
	}
	var result propertyResult
	h.data(t, &result)
	if result.Property != "battery" || string(result.Value) != `{"level":89}` || result.Unit != "%" {
		t.Fatalf("result=%+v", result)
	}
	if h.backend.scans.Load() != 0 {
		t.Fatal("get scanned although the device was already known")
	}
}

func TestGetScansWhenTheKnownDeviceLacksABLEEndpoint(t *testing.T) {
	h := newHarness()
	h.backend.known = []domain.Device{classicDevice("os:1", "Headphones", "AA:BB:CC:DD:EE:02")}
	h.ble.devices = []domain.Device{bleDevice("aa-bb-cc-dd-ee-02", "")}
	h.ble.battery = []byte{70}
	if code := h.run("--json", "get", "Headphones", "battery"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr.String())
	}
	var result propertyResult
	h.data(t, &result)
	if result.DeviceID != "os:1" || string(result.Value) != `{"level":70}` {
		t.Fatalf("the BLE endpoint was not merged into the OS device: %+v", result)
	}
}

func TestDuplicateExactNamesAreAmbiguous(t *testing.T) {
	h := newHarness()
	h.backend.known = []domain.Device{classicDevice("os:1", "Keyboard", "AA:BB:CC:DD:EE:03"), classicDevice("os:2", "Keyboard", "AA:BB:CC:DD:EE:04")}
	if code := h.run("connect", "Keyboard"); code != domain.ExitCode(domain.NewError(domain.CodeAmbiguous, "")) {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(h.stderr.String(), "os:1") || !strings.Contains(h.stderr.String(), "os:2") {
		t.Fatalf("human error does not list the candidates:\n%s", h.stderr.String())
	}
	if len(h.backend.mutations) != 0 {
		t.Fatalf("mutated an ambiguous device: %v", h.backend.mutations)
	}
}

func TestDeviceCanBeNamedByAddress(t *testing.T) {
	h := newHarness()
	h.backend.known = []domain.Device{classicDevice("os:1", "Keyboard", "AA:BB:CC:DD:EE:03"), classicDevice("os:2", "Keyboard", "AA:BB:CC:DD:EE:04")}
	if code := h.run("--json", "connect", "aabbccddee04"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr.String())
	}
	if len(h.backend.mutations) != 1 || h.backend.mutations[0] != "connect os:2" {
		t.Fatalf("mutations=%v", h.backend.mutations)
	}
}

func TestMutationsReportVerifiedStateAndAreIdempotent(t *testing.T) {
	h := newHarness()
	device := classicDevice("os:1", "Speaker", "AA:BB:CC:DD:EE:05")
	h.backend.known = []domain.Device{device}
	if code := h.run("--json", "connect", "Speaker"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr.String())
	}
	var result mutationResult
	h.data(t, &result)
	if result != (mutationResult{DeviceID: "os:1", Name: "Speaker", Action: "connect", Changed: true, Paired: true, Connected: true}) {
		t.Fatalf("result=%+v", result)
	}
	device.Connected = true
	h.backend.known = []domain.Device{device}
	h.backend.mutations = nil
	if code := h.run("--json", "connect", "Speaker"); code != 0 {
		t.Fatalf("code=%d", code)
	}
	h.data(t, &result)
	if result.Changed || len(h.backend.mutations) != 0 {
		t.Fatalf("connecting a connected device was not a no-op: %+v %v", result, h.backend.mutations)
	}
	if code := h.run("connect", "Speaker"); code != 0 || !strings.Contains(h.stdout.String(), "already connected") {
		t.Fatalf("human output=%q", h.stdout.String())
	}
	if code := h.run("--quiet", "connect", "Speaker"); code != 0 || h.stdout.Len() != 0 {
		t.Fatalf("--quiet printed %q", h.stdout.String())
	}
}

func TestPairScansForAnUnknownDevice(t *testing.T) {
	h := newHarness()
	h.backend.scanned = []domain.Device{{ID: "os:9", Name: "New Mouse", Endpoints: []domain.Endpoint{{Address: "AA:BB:CC:DD:EE:09", Transport: domain.TransportOS}}}}
	if code := h.run("--json", "pair", "New Mouse"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr.String())
	}
	if h.backend.scans.Load() != 1 || len(h.backend.mutations) != 1 || h.backend.mutations[0] != "pair os:9" {
		t.Fatalf("scans=%d mutations=%v", h.backend.scans.Load(), h.backend.mutations)
	}
}

func TestScanWarnsWhenOneSourceFails(t *testing.T) {
	h := newHarness()
	h.backend.scanned = []domain.Device{classicDevice("os:1", "Speaker", "AA:BB:CC:DD:EE:05")}
	h.ble.scanErr = domain.NewError(domain.CodePermission, "Bluetooth permission denied")
	if code := h.run("--json", "scan"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr.String())
	}
	var devices []domain.Device
	h.data(t, &devices)
	if len(devices) != 1 {
		t.Fatalf("devices=%+v", devices)
	}
	if !strings.Contains(h.stderr.String(), `"warning":{"message":"BLE scan failed: Bluetooth permission denied"}`) {
		t.Fatalf("stderr=%s", h.stderr.String())
	}
}

func TestEmptyResultsAreEmptyArrays(t *testing.T) {
	h := newHarness()
	if code := h.run("--json", "scan"); code != 0 || strings.TrimSpace(h.stdout.String()) != `{"schema_version":1,"data":[]}` {
		t.Fatalf("code=%d stdout=%s", code, h.stdout.String())
	}
}

func TestStandardDeviceInformationProperty(t *testing.T) {
	h := newHarness()
	h.backend.known = []domain.Device{bleDevice("AA:BB:CC:DD:EE:01", "Sensor")}
	h.ble.info = map[string]string{"firmware_version": "1.2.3"}
	if code := h.run("--json", "get", "Sensor", "firmware_version"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr.String())
	}
	var result propertyResult
	h.data(t, &result)
	if string(result.Value) != `"1.2.3"` {
		t.Fatalf("result=%+v", result)
	}
}

func TestUnknownPropertyWithoutDriversNeedsADriver(t *testing.T) {
	h := newHarness()
	h.backend.known = []domain.Device{bleDevice("AA:BB:CC:DD:EE:01", "Sensor")}
	if code := h.run("--json", "get", "Sensor", "noise_control"); code != domain.ExitCode(domain.NewError(domain.CodeDriverRequired, "")) {
		t.Fatalf("code=%d", code)
	}
	if h.errorCode(t) != domain.CodeDriverRequired {
		t.Fatalf("stderr=%s", h.stderr.String())
	}
}

func TestBackendErrorsKeepTheirCategory(t *testing.T) {
	h := newHarness()
	h.backend.err = domain.NewError(domain.CodePermission, "Bluetooth permission denied")
	if code := h.run("--json", "list"); code != domain.ExitCode(h.backend.err) || h.errorCode(t) != domain.CodePermission {
		t.Fatalf("code=%d stderr=%s", code, h.stderr.String())
	}
}

func TestListIncludesCorrelatedHIDBattery(t *testing.T) {
	h := newHarness()
	h.backend.known = []domain.Device{{ID: "macos:trackpad", Name: "Trackpad", Endpoints: []domain.Endpoint{{Address: "00-81-2a-f1-46-01"}}}}
	h.hid = fakeHID{observations: []hidbattery.Observation{{Address: "00:81:2A:F1:46:01", Percentage: 90}}}
	if code := h.run("--json", "list"); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(h.stdout.String(), `"battery":{"value":{"level":90},"unit":"%","display":"90%"}`) {
		t.Fatalf("stdout=%s", h.stdout.String())
	}
}

func TestWatchReportsChangesAndStopsOnCancel(t *testing.T) {
	h := newHarness()
	h.backend.known = []domain.Device{bleDevice("AA:BB:CC:DD:EE:01", "Sensor")}
	h.ble.battery = []byte{50}
	var out bytes.Buffer
	a := &application{backend: h.backend, ble: h.ble, render: output.Renderer{Out: &out, Err: &out, Mode: output.JSON}, opts: options{timeout: time.Second, interval: 5 * time.Millisecond}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.watch(ctx, []string{"Sensor", "battery"}) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"event":"snapshot"`) {
		t.Fatalf("an unchanged value must be reported once:\n%s", out.String())
	}
	if h.backend.scans.Load() != 0 {
		t.Fatal("watch scanned")
	}
	if h.ble.reads.Load() < 2 {
		t.Fatal("watch did not poll")
	}
}

func TestClassifyContextError(t *testing.T) {
	interrupted, cancel := context.WithCancel(context.Background())
	if got := classifyContextError(context.DeadlineExceeded, interrupted, time.Second); domain.AsError(got).Code != domain.CodeTimeout {
		t.Fatalf("deadline: %v", got)
	}
	coded := domain.NewError(domain.CodeDisconnected, "gone")
	if got := classifyContextError(coded, interrupted, time.Second); !errors.Is(got, coded) {
		t.Fatalf("a coded error was replaced: %v", got)
	}
	cancel()
	if got := classifyContextError(errors.New("read failed"), interrupted, time.Second); domain.AsError(got).Code != domain.CodeCanceled || domain.ExitCode(got) != 130 {
		t.Fatalf("interrupt: %v", got)
	}
}

func TestGetAllReadsEveryKnownDevice(t *testing.T) {
	h := newHarness()
	h.backend.known = []domain.Device{
		bleDevice("AA:BB:CC:DD:EE:01", "Sensor"),
		classicDevice("os:1", "Speaker", "AA:BB:CC:DD:EE:02"),
	}
	h.ble.battery = []byte{89}
	if code := h.run("--json", "get", "--all", "battery"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr.String())
	}
	var entries []struct {
		DeviceID string          `json:"device_id"`
		Value    json.RawMessage `json:"value"`
		Error    *domain.Error   `json:"error"`
	}
	h.data(t, &entries)
	if len(entries) != 2 {
		t.Fatalf("entries=%+v", entries)
	}
	for _, entry := range entries {
		switch entry.DeviceID {
		case "ble:AA:BB:CC:DD:EE:01":
			if !strings.Contains(string(entry.Value), "89") || entry.Error != nil {
				t.Errorf("sensor=%+v", entry)
			}
		default:
			// A Classic device without a driver has no battery to read.
			if entry.Value != nil || entry.Error == nil {
				t.Errorf("speaker=%+v", entry)
			}
		}
	}
	if h.backend.scans.Load() != 0 {
		t.Fatal("get --all scanned")
	}
}

func TestGetAllTakesOnePropertyAndNoDevice(t *testing.T) {
	h := newHarness()
	for _, args := range [][]string{{"get", "--all"}, {"get", "--all", "Sensor", "battery"}, {"get", "Sensor"}, {"list", "--all"}} {
		if code := h.run(args...); code != 2 {
			t.Errorf("bt %v: code=%d, want a usage error", args, code)
		}
	}
}

// adapterBackend adds adapter control to fakeBackend.
type adapterBackend struct {
	fakeBackend
	adapter domain.Adapter
	ignore  bool // pretend to apply changes without applying them
}

func (b *adapterBackend) Adapters(context.Context) ([]domain.Adapter, error) {
	return []domain.Adapter{b.adapter}, nil
}

func (b *adapterBackend) SetAdapter(_ context.Context, change domain.AdapterChange) (domain.Adapter, error) {
	if !b.ignore {
		if change.Powered != nil {
			b.adapter.Powered = change.Powered
		}
		if change.Name != nil {
			b.adapter.Name = *change.Name
		}
	}
	return b.adapter, nil
}

func TestAdapterReportsAndChangesTheAdapter(t *testing.T) {
	off := false
	backend := &adapterBackend{adapter: domain.Adapter{ID: "hci0", Name: "laptop", Powered: &off, Default: true}}
	h := newHarness()
	run := func(args ...string) int {
		h.stdout.Reset()
		h.stderr.Reset()
		return run(args, &h.stdout, &h.stderr, "test", func(a *application) { a.backend, a.ble = backend, h.ble })
	}
	if code := run("--json", "adapter"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr.String())
	}
	var adapters []domain.Adapter
	h.data(t, &adapters)
	if len(adapters) != 1 || adapters[0].ID != "hci0" || *adapters[0].Powered {
		t.Fatalf("adapters=%+v", adapters)
	}
	if code := run("--json", "adapter", "power", "on"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr.String())
	}
	var state domain.Adapter
	h.data(t, &state)
	if state.Powered == nil || !*state.Powered {
		t.Fatalf("state=%+v", state)
	}
	backend.ignore = true
	if code := run("adapter", "power", "off"); code == 0 {
		t.Fatal("a change the adapter didn't make was reported as done")
	}
	for _, args := range [][]string{{"adapter", "power", "maybe"}, {"adapter", "volume", "up"}, {"adapter", "name", " "}, {"adapter", "power"}} {
		if code := run(args...); code != 2 {
			t.Errorf("bt %v: code=%d, want a usage error", args, code)
		}
	}
}
