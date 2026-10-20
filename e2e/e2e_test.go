//go:build e2e

// Package e2e_test runs the real bt binary and drivers against scripted
// Bluetooth devices. See README.md.
package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vladcuciureanu/betty/internal/domain"
)

const (
	notifyCharacteristic = "b0e7e2e0-0000-4000-8000-000000000002"
	writeCharacteristic  = "b0e7e2e0-0000-4000-8000-000000000003"
)

type property struct {
	DeviceID string          `json:"device_id"`
	Value    json.RawMessage `json:"value"`
	Event    string          `json:"event"`
}

type mutation struct {
	Action    string `json:"action"`
	Changed   bool   `json:"changed"`
	Paired    bool   `json:"paired"`
	Connected bool   `json:"connected"`
}

func TestScanFindsABLEDevice(t *testing.T) {
	p := startPeer(t, "sensor:Betty Sensor")
	r := bt(t, nil, "--verbose", "scan")
	t.Logf("scan diagnostics:\n%s", r.stderr)
	var devices []domain.Device
	decodeData(t, r, &devices)
	for _, d := range devices {
		if d.Name == p.device(0).Name {
			return
		}
	}
	t.Fatalf("scan did not find %q among %d devices", p.device(0).Name, len(devices))
}

func TestStandardGATTProperties(t *testing.T) {
	p := startPeer(t, "sensor:Betty Sensor")
	name := p.device(0).Name
	var battery property
	ok(t, &battery, "get", name, "battery")
	if string(battery.Value) != "87" {
		t.Fatalf("battery=%s", battery.Value)
	}
	var firmware property
	ok(t, &firmware, "get", name, "firmware_version")
	if string(firmware.Value) != `"1.2.3"` {
		t.Fatalf("firmware_version=%s", firmware.Value)
	}
}

func TestGATTReadWriteAndNotify(t *testing.T) {
	p := startPeer(t, "sensor:Betty Sensor")
	name := p.device(0).Name
	var read struct {
		Value string `json:"value"`
	}
	ok(t, &read, "gatt", name, "read", "2a19")
	if read.Value != "57" { // 87 percent
		t.Fatalf("read=%+v", read)
	}

	ok(t, nil, "gatt", name, "write", writeCharacteristic, "0102")
	if written := p.wait("written", 10*time.Second); written.Value != "0102" {
		t.Fatalf("the device received %q", written.Value)
	}
	ok(t, nil, "gatt", name, "write", writeCharacteristic, "03", "--without-response")
	if written := p.wait("written", 10*time.Second); written.Value != "03" {
		t.Fatalf("the device received %q", written.Value)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lines, wait := stream(t, ctx, "gatt", name, "watch", notifyCharacteristic)
	p.wait("connected", 30*time.Second)
	// Give the watch time to subscribe after it connects.
	time.Sleep(time.Second)
	p.do(map[string]any{"cmd": "notify", "device": 0, "value": "aa55"})
	nextLine(t, lines, `"value":"aa55"`, 15*time.Second)
	cancel()
	wait()
}

func TestWatchReportsPropertyChanges(t *testing.T) {
	p := startPeer(t, "sensor:Betty Sensor")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lines, wait := stream(t, ctx, "watch", p.device(0).Name, "battery", "--interval", "250ms")
	nextLine(t, lines, `"event":"snapshot"`, 30*time.Second)
	p.do(map[string]any{"cmd": "set_battery", "device": 0, "level": 42})
	line := nextLine(t, lines, `"event":"changed"`, 30*time.Second)
	if !strings.Contains(string(line), `"value":42`) {
		t.Fatalf("change=%s", line)
	}
	cancel()
	wait()
}

func TestADroppedLinkEndsAGATTWatch(t *testing.T) {
	p := startPeer(t, "sensor:Betty Sensor")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, wait := stream(t, ctx, "gatt", p.device(0).Name, "watch", notifyCharacteristic)
	p.wait("connected", 30*time.Second)
	time.Sleep(time.Second)
	p.do(map[string]any{"cmd": "drop", "device": 0})
	r := wait()
	if ctx.Err() != nil {
		t.Fatal("the watch kept running after the device dropped the link")
	}
	if want := domain.ExitCode(domain.NewError(domain.CodeDisconnected, "")); r.code != want {
		t.Fatalf("exit %d, want %d (device_disconnected)\n%s", r.code, want, r.stderr)
	}
}

func TestDuplicateNamesAreAmbiguous(t *testing.T) {
	requireVirtual(t)
	p := startPeer(t, "sensor:Twin", "sensor:Twin")
	fails(t, domain.CodeAmbiguous, "get", p.device(0).Name, "battery")
	var battery property
	ok(t, &battery, "get", p.device(1).Address, "battery")
	if string(battery.Value) != "87" {
		t.Fatalf("battery=%s", battery.Value)
	}
}

func TestClassicPairingLifecycle(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS cannot discover new Classic devices yet, so it cannot pair them")
	}
	// A keyboard, because BlueZ connects only profiles it knows, and HID
	// needs no other daemon.
	p := startPeer(t, "keyboard:Betty Keyboard")
	name := p.device(0).Name
	var m mutation
	ok(t, &m, "pair", name)
	if !m.Changed || !m.Paired {
		t.Fatalf("pair=%+v", m)
	}
	t.Cleanup(func() { bt(t, nil, "unpair", p.device(0).Address) })
	// The link may stay up for a while after pairing, as with real devices.
	assertPaired(t, p.device(0).Address)

	if runtime.GOOS == "windows" {
		fails(t, domain.CodeUnsupported, "connect", name)
	} else {
		ok(t, &m, "connect", name)
		if !m.Connected {
			t.Fatalf("connect=%+v", m)
		}
		ok(t, &m, "disconnect", name)
		if m.Connected {
			t.Fatalf("disconnect=%+v", m)
		}
	}
	ok(t, &m, "unpair", name)
	if m.Paired {
		t.Fatalf("unpair=%+v", m)
	}
}

func TestRefusedPairingIsAnAuthenticationFailure(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS cannot discover new Classic devices yet, so it cannot pair them")
	}
	p := startPeer(t, "speaker:Betty Speaker")
	p.do(map[string]any{"cmd": "accept_pairing", "device": 0, "accept": false})
	fails(t, domain.CodeAuthentication, "pair", p.device(0).Name)
}

func TestSonyBatteryThroughTheDriver(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS cannot discover new Classic devices yet, so it cannot pair them")
	}
	if !virtual() && os.Getenv("BETTY_E2E_HOST_ADDRESS") == "" {
		t.Skip("set BETTY_E2E_HOST_ADDRESS so the headset can connect to this computer")
	}
	drivers := t.TempDir()
	t.Setenv("BETTY_DRIVER_DIR", drivers)
	ok(t, nil, "driver", "install", filepath.Join(binDir, executable("bt-driver-sony")))

	t.Setenv("BETTY_PEER_LOG", "DEBUG")
	p := startPeer(t, "sony:WH-1000XM5")
	address := p.device(0).Address
	if !virtual() {
		// Bumble's virtual controller cannot re-authenticate a bonded Classic
		// link, which the driver's RFCOMM connection would need, so on the
		// virtual link the headset connects without pairing.
		ok(t, nil, "pair", address)
		t.Cleanup(func() { bt(t, nil, "unpair", address) })
	} else {
		var devices []domain.Device
		ok(t, &devices, "scan")
	}
	// The driver reads battery only from a connected device. Headphones
	// connect to the computer themselves; the link may also still be up
	// from pairing, and a second connection would confuse both sides.
	if !connected(t, address) {
		p.do(map[string]any{"cmd": "connect_host", "device": 0})
	}
	waitConnected(t, address)

	if virtual() {
		t.Cleanup(func() {
			if t.Failed() {
				output, _ := exec.Command("bluetoothctl", "info", address).CombinedOutput()
				t.Logf("BlueZ's view of the headset:\n%s", output)
			}
		})
	}
	var battery property
	ok(t, &battery, "get", address, "battery")
	if string(battery.Value) != `{"headset":{"level":73}}` {
		t.Fatalf("battery=%s", battery.Value)
	}
}

func assertPaired(t *testing.T, address string) {
	t.Helper()
	var d domain.Device
	ok(t, &d, "inspect", address)
	if !d.Paired {
		t.Fatalf("%s is not paired", address)
	}
}

func connected(t *testing.T, address string) bool {
	t.Helper()
	var d domain.Device
	ok(t, &d, "inspect", address)
	return d.Connected
}

func waitConnected(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		var d domain.Device
		ok(t, &d, "inspect", address)
		if d.Connected {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never showed as connected", address)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
