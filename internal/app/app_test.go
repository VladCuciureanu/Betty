package app

import (
	"bytes"
	"testing"
	"time"

	"github.com/vladcuciureanu/betty/internal/domain"
	"github.com/vladcuciureanu/betty/internal/output"
)

func TestParseGlobal(t *testing.T) {
	o, args, err := parseGlobal([]string{"--json", "--non-interactive", "--timeout", "3s", "scan"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if o.mode != output.JSON || !o.nonInteractive || o.timeout != 3*time.Second || len(args) != 1 || args[0] != "scan" {
		t.Fatalf("unexpected parse: %+v %v", o, args)
	}
}

func TestWatchCommandsDoNotReceiveGlobalDeadline(t *testing.T) {
	if !isWatchCommand([]string{"watch", "device", "battery"}) || !isWatchCommand([]string{"gatt", "device", "watch", "2a19"}) || isWatchCommand([]string{"get", "device", "battery"}) {
		t.Fatal("watch command classification is incorrect")
	}
}
func TestParseGlobalRejectsConflictingOutput(t *testing.T) {
	_, _, err := parseGlobal([]string{"--json", "--quiet", "scan"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error")
	}
}
func TestJSONFlagParseErrorDoesNotLeakHumanFlagOutput(t *testing.T) {
	var out, stderr bytes.Buffer
	code := Main([]string{"--json", "--timeout", "invalid", "scan"}, bytes.NewBuffer(nil), &out, &stderr, "test")
	if code != domain.ExitCode(domain.NewError(domain.CodeUsage, "")) {
		t.Fatalf("code=%d", code)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout=%q", out.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte(`"schema_version":1`)) || !bytes.Contains(stderr.Bytes(), []byte(`"retryable":false`)) {
		t.Fatalf("stderr=%q", stderr.String())
	}
}
func TestVersion(t *testing.T) {
	var out bytes.Buffer
	if code := Main([]string{"--version"}, bytes.NewBuffer(nil), &out, &bytes.Buffer{}, "1.2.3"); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if out.String() != "bt 1.2.3\n" {
		t.Fatalf("output=%q", out.String())
	}
}

func TestPropertiesThatRequireOptionalDrivers(t *testing.T) {
	for _, property := range []string{"noise_control", "ambient_level", "sony.speak_to_chat"} {
		if !requiresDeviceDriver(property) {
			t.Fatalf("%q should require a driver", property)
		}
	}
	if requiresDeviceDriver("battery") {
		t.Fatal("standard battery must not require a driver")
	}
}

func TestPropertyInputPreservesNumericTypesAndRange(t *testing.T) {
	minimum, maximum := 0.0, 20.0
	value, err := propertyInput(domain.DeviceCapability{Kind: "integer", Minimum: &minimum, Maximum: &maximum}, "ambient_level", "12")
	if err != nil || string(value) != "12" {
		t.Fatalf("value=%s err=%v", value, err)
	}
	if _, err := propertyInput(domain.DeviceCapability{Kind: "integer", Minimum: &minimum, Maximum: &maximum}, "ambient_level", "21"); domain.AsError(err).Code != domain.CodeUsage {
		t.Fatalf("err=%v", err)
	}
}

func TestMergeDoesNotUseDeviceNameAsIdentity(t *testing.T) {
	devices := merge([]domain.Device{{ID: "one", Name: "Fridge", Endpoints: []domain.Endpoint{{Transport: domain.TransportBLE, Address: "a"}}}}, []domain.Device{{ID: "two", Name: "Fridge", Endpoints: []domain.Endpoint{{Transport: domain.TransportBLE, Address: "b"}}}})
	if len(devices) != 2 {
		t.Fatalf("merged distinct same-name devices: %#v", devices)
	}
}

func TestMergeDeduplicatesEndpointsAndUnionsServices(t *testing.T) {
	endpoint := domain.Endpoint{ID: "a", Transport: domain.TransportBLE, Address: "AA"}
	devices := merge(
		[]domain.Device{{ID: "one", Name: "Sensor", Services: []string{"180f"}, Endpoints: []domain.Endpoint{endpoint}}},
		[]domain.Device{{ID: "one", Name: "Sensor", Services: []string{"180f", "180a"}, Endpoints: []domain.Endpoint{endpoint}}},
	)
	if len(devices) != 1 || len(devices[0].Endpoints) != 1 || len(devices[0].Services) != 2 {
		t.Fatalf("devices=%#v", devices)
	}
}
