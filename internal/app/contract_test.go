package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/vladcuciureanu/betty/internal/ble"
	"github.com/vladcuciureanu/betty/internal/domain"
	"github.com/vladcuciureanu/betty/internal/output"
)

type contractBackend struct {
	devices []domain.Device
	err     error
}

func (b contractBackend) Capabilities() domain.Capabilities               { return domain.Capabilities{} }
func (b contractBackend) Scan(context.Context) ([]domain.Device, error)   { return b.devices, b.err }
func (b contractBackend) List(context.Context) ([]domain.Device, error)   { return b.devices, b.err }
func (b contractBackend) Pair(context.Context, domain.Device) error       { return b.err }
func (b contractBackend) Unpair(context.Context, domain.Device) error     { return b.err }
func (b contractBackend) Connect(context.Context, domain.Device) error    { return b.err }
func (b contractBackend) Disconnect(context.Context, domain.Device) error { return b.err }

type contractBLE struct {
	devices []domain.Device
	battery []byte
	info    map[string]string
	err     error
}

func (b contractBLE) Scan(context.Context) ([]domain.Device, error)         { return b.devices, b.err }
func (contractBLE) Services(context.Context, string) ([]ble.Service, error) { return nil, nil }
func (contractBLE) Characteristics(context.Context, string, string) ([]ble.Characteristic, error) {
	return nil, nil
}
func (b contractBLE) Read(context.Context, string, string) ([]byte, error)          { return b.battery, b.err }
func (contractBLE) Write(context.Context, string, string, []byte, bool) error       { return nil }
func (contractBLE) Watch(context.Context, string, string, func([]byte) error) error { return nil }
func (b contractBLE) DeviceInformation(context.Context, string) (map[string]string, error) {
	return b.info, b.err
}

func TestStandardDeviceInformationProperty(t *testing.T) {
	device := domain.Device{ID: "ble:one", Name: "Sensor", Endpoints: []domain.Endpoint{{ID: "one", Address: "one", Transport: domain.TransportBLE}}}
	var out bytes.Buffer
	a := application{
		backend: contractBackend{devices: []domain.Device{device}},
		ble:     contractBLE{devices: []domain.Device{device}, info: map[string]string{"firmware_revision": "1.2.3"}},
		render:  output.Renderer{Out: &out, Err: &out, Mode: output.JSON},
		timeout: time.Second,
	}
	if err := a.run(context.Background(), []string{"get", device.ID, "firmware_version"}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"property":"firmware_version"`)) || !bytes.Contains(out.Bytes(), []byte(`"value":"1.2.3"`)) {
		t.Fatalf("output=%s", out.Bytes())
	}
}

func TestGenericBatteryAliasUsesPropertyEnvelope(t *testing.T) {
	device := domain.Device{ID: "ble:one", Name: "Sensor", Endpoints: []domain.Endpoint{{ID: "one", Address: "one", Transport: domain.TransportBLE}}}
	for _, args := range [][]string{{"get", "ble:one", "battery"}, {"ble:one", "battery"}} {
		var out bytes.Buffer
		a := application{
			backend: contractBackend{devices: []domain.Device{device}},
			ble:     contractBLE{devices: []domain.Device{device}, battery: []byte{89}},
			render:  output.Renderer{Out: &out, Err: &out, Mode: output.JSON},
			timeout: time.Second,
		}
		if err := a.run(context.Background(), args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		var envelope struct {
			Data struct {
				DeviceID string `json:"device_id"`
				Property string `json:"property"`
				Value    int    `json:"value"`
			} `json:"data"`
		}
		if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Data.DeviceID != device.ID || envelope.Data.Property != "battery" || envelope.Data.Value != 89 {
			t.Fatalf("data=%+v", envelope.Data)
		}
	}
}

func TestBackendErrorsKeepTheirStructuredCategory(t *testing.T) {
	want := domain.NewError(domain.CodePermission, "Bluetooth permission denied")
	a := application{backend: contractBackend{err: want}, ble: contractBLE{err: want}, timeout: time.Second}
	_, err := a.inventory(context.Background(), false)
	if !errors.Is(err, want) || domain.AsError(err).Code != domain.CodePermission {
		t.Fatalf("err=%v", err)
	}
}
