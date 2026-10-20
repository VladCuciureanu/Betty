//go:build e2e

package e2e_test

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/vladcuciureanu/betty/internal/domain"
)

var (
	buildProbe sync.Once
	probeErr   error
)

// installProbe builds the GATT probe test driver once and installs it into a
// fresh driver directory, returning the environment that selects it.
func installProbe(t *testing.T) []string {
	t.Helper()
	path := filepath.Join(binDir, executable("gattprobe"))
	buildProbe.Do(func() {
		build := exec.Command("go", "build", "-o", path, "./e2e/drivers/gattprobe")
		build.Dir = ".."
		if output, err := build.CombinedOutput(); err != nil {
			probeErr = err
			t.Logf("%s", output)
		}
	})
	if probeErr != nil {
		t.Fatalf("build the GATT probe: %v", probeErr)
	}
	env := []string{"BETTY_DRIVER_DIR=" + t.TempDir()}
	if r := bt(t, env, "driver", "install", path); r.code != 0 {
		t.Fatalf("install the GATT probe: %s", r.stderr)
	}
	return env
}

func probe(t *testing.T, env []string, device, name string) string {
	t.Helper()
	r := bt(t, env, "get", device, name)
	if r.code != 0 {
		t.Fatalf("bt get %s %s: exit %d\n%s", device, name, r.code, r.stderr)
	}
	var result property
	decodeData(t, r, &result)
	var value string
	if err := json.Unmarshal(result.Value, &value); err != nil {
		t.Fatalf("%s=%s", name, result.Value)
	}
	return value
}

func TestADriverReadsGATTThroughHostCalls(t *testing.T) {
	env := installProbe(t)
	p := startPeer(t, "sensor:Betty Sensor")
	name := p.device(0).Name
	if count, err := strconv.Atoi(probe(t, env, name, "probe.services")); err != nil || count < 1 {
		t.Fatalf("gatt.services listed %d services (%v)", count, err)
	}
	// The sensor's Battery Level is 87%.
	if level := probe(t, env, name, "probe.battery"); level != "57" {
		t.Fatalf("gatt.read returned %q, want 57", level)
	}
}

func TestADriverReceivesGATTNotifications(t *testing.T) {
	env := installProbe(t)
	p := startPeer(t, "sensor:Betty Sensor")
	done := make(chan result, 1)
	go func() { done <- bt(t, env, "get", p.device(0).Name, "probe.notify") }()
	p.wait("connected", 30*time.Second)
	// Give the driver time to subscribe after Betty connects.
	time.Sleep(2 * time.Second)
	p.do(map[string]any{"cmd": "notify", "device": 0, "value": "aa55"})
	r := <-done
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	var result property
	decodeData(t, r, &result)
	if string(result.Value) != `"aa55"` {
		t.Fatalf("gatt.next returned %s", result.Value)
	}
}

func TestADroppedLinkEndsADriversGATTWait(t *testing.T) {
	env := installProbe(t)
	p := startPeer(t, "sensor:Betty Sensor")
	done := make(chan result, 1)
	go func() { done <- bt(t, env, "get", p.device(0).Name, "probe.notify") }()
	p.wait("connected", 30*time.Second)
	time.Sleep(2 * time.Second)
	p.do(map[string]any{"cmd": "drop", "device": 0})
	select {
	case r := <-done:
		if want := domain.ExitCode(domain.NewError(domain.CodeDisconnected, "")); r.code != want {
			t.Fatalf("exit %d, want %d (device_disconnected)\n%s", r.code, want, r.stderr)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the driver kept waiting after the device dropped the link")
	}
}
