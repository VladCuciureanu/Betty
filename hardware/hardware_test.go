//go:build hardware

package hardware_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

type envelope struct {
	SchemaVersion int             `json:"schema_version"`
	Data          json.RawMessage `json:"data"`
}

func TestGenericBLE(t *testing.T) {
	device := os.Getenv("BETTY_HARDWARE_BLE")
	if device == "" {
		t.Skip("set BETTY_HARDWARE_BLE to an isolated test peripheral")
	}
	runJSON(t, "scan")
	runJSON(t, "gatt", device, "services")
	runJSON(t, device, "battery")
}

func TestClassicLifecycle(t *testing.T) {
	device := os.Getenv("BETTY_HARDWARE_CLASSIC")
	if device == "" {
		t.Skip("set BETTY_HARDWARE_CLASSIC to a disposable test device")
	}
	runJSON(t, "pair", device)
	t.Cleanup(func() { _ = command("--json", "unpair", device).Run() })
	runJSON(t, "connect", device)
	runJSON(t, "disconnect", device)
	runJSON(t, "unpair", device)
}

func runJSON(t *testing.T, args ...string) {
	t.Helper()
	args = append([]string{"--json", "--timeout=30s"}, args...)
	output, err := command(args...).CombinedOutput()
	if err != nil {
		t.Fatalf("bt %v: %v\n%s", args, err, output)
	}
	var decoded envelope
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, output)
	}
	if decoded.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d", decoded.SchemaVersion)
	}
}

func command(args ...string) *exec.Cmd {
	binary := os.Getenv("BETTY_BINARY")
	if binary == "" {
		binary = "bt"
	}
	return exec.Command(binary, args...)
}
