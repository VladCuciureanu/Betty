package app

import (
	"strings"
	"testing"

	"github.com/vladcuciureanu/betty/internal/domain"
)

// headphones is a device whose battery a driver can read only over a
// connection.
func headphones(connected bool) domain.Device {
	d := bleDevice("AA:BB:CC:DD:EE:03", "Headphones")
	d.Connected = connected
	d.Capabilities = map[string]domain.DeviceCapability{
		"battery": {Status: domain.StatusAvailable, Kind: domain.KindBattery, Readable: true, RequiresConnection: true},
	}
	return d
}

func TestACapabilityThatNeedsAConnectionIsRefusedWhileDisconnected(t *testing.T) {
	h := newHarness()
	h.backend.known = []domain.Device{headphones(false)}
	code := h.run("--json", "get", "Headphones", "battery")
	if code != domain.ExitCode(domain.NewError(domain.CodeDisconnected, "")) || h.errorCode(t) != domain.CodeDisconnected {
		t.Fatalf("exit %d, stderr %s", code, h.stderr.String())
	}
	if !strings.Contains(h.stderr.String(), "reading battery needs Headphones to be connected over Bluetooth") {
		t.Fatalf("stderr %s", h.stderr.String())
	}
	if h.ble.reads.Load() != 0 {
		t.Fatal("Betty read the battery of a disconnected device")
	}
}

func TestACapabilityThatNeedsAConnectionWorksWhileConnected(t *testing.T) {
	h := newHarness()
	h.backend.known = []domain.Device{headphones(true)}
	h.ble.battery = []byte{64}
	if code := h.run("--json", "get", "Headphones", "battery"); code != 0 {
		t.Fatalf("exit %d, stderr %s", code, h.stderr.String())
	}
}

func TestInspectSaysWhichCapabilitiesNeedAConnection(t *testing.T) {
	h := newHarness()
	h.backend.known = []domain.Device{headphones(false)}
	if code := h.run("inspect", "Headphones"); code != 0 {
		t.Fatalf("exit %d, stderr %s", code, h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "battery            available, needs a connection") {
		t.Fatalf("stdout %s", h.stdout.String())
	}
}
