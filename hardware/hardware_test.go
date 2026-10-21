//go:build hardware

package hardware_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/vladcuciureanu/betty/internal/domain"
	"github.com/vladcuciureanu/betty/internal/resolver"
)

type envelope struct {
	SchemaVersion int             `json:"schema_version"`
	Data          json.RawMessage `json:"data"`
	Error         *domain.Error   `json:"error,omitempty"`
}

func TestGenericBLE(t *testing.T) {
	device := os.Getenv("BETTY_HARDWARE_BLE")
	if device == "" {
		t.Skip("set BETTY_HARDWARE_BLE to an isolated test peripheral")
	}
	runJSON(t, "scan")
	runJSON(t, "gatt", device, "services")
	runJSON(t, "gatt", device, "characteristics")
	runJSON(t, "get", device, "battery")
	if characteristic := os.Getenv("BETTY_HARDWARE_READ_CHARACTERISTIC"); characteristic != "" {
		runJSON(t, "gatt", device, "read", characteristic)
	}
	if characteristic := os.Getenv("BETTY_HARDWARE_WRITE_CHARACTERISTIC"); characteristic != "" {
		value := os.Getenv("BETTY_HARDWARE_WRITE_HEX")
		if value == "" {
			t.Fatal("BETTY_HARDWARE_WRITE_HEX is required with BETTY_HARDWARE_WRITE_CHARACTERISTIC")
		}
		runMutation(t, "gatt", device, "write", characteristic, value, "--with-response")
		runMutation(t, "gatt", device, "write", characteristic, value, "--without-response")
	}
}

func TestClassicLifecycle(t *testing.T) {
	device := os.Getenv("BETTY_HARDWARE_CLASSIC")
	if device == "" {
		t.Skip("set BETTY_HARDWARE_CLASSIC to a disposable test device")
	}
	runMutation(t, "pair", device)
	t.Cleanup(func() { _ = command("--json", "unpair", device).Run() })
	assertDevicePaired(t, device, true)
	if runtime.GOOS == "windows" {
		runError(t, domain.CodeUnsupported, "connect", device)
		runError(t, domain.CodeUnsupported, "disconnect", device)
	} else {
		runMutation(t, "connect", device)
		assertDeviceState(t, device, true, true)
		runMutation(t, "disconnect", device)
		assertDeviceState(t, device, true, false)
	}
	if runtime.GOOS == "darwin" {
		runError(t, domain.CodeUnsupported, "unpair", device)
		return
	}
	runMutation(t, "unpair", device)
	assertDeviceState(t, device, false, false)
}

func assertDevicePaired(t *testing.T, query string, paired bool) {
	t.Helper()
	var devices []domain.Device
	if err := json.Unmarshal(runJSON(t, "list"), &devices); err != nil {
		t.Fatal(err)
	}
	device, err := resolver.Resolve(devices, query)
	if err != nil {
		if !paired && domain.AsError(err).Code == domain.CodeNotFound {
			return
		}
		t.Fatal(err)
	}
	if device.Paired != paired {
		t.Fatalf("device paired=%t, want %t", device.Paired, paired)
	}
}

func runError(t *testing.T, code domain.ErrorCode, args ...string) {
	t.Helper()
	args = append([]string{"--json", "--non-interactive", "--timeout=30s"}, args...)
	output, err := command(args...).CombinedOutput()
	if err == nil {
		t.Fatalf("bt %v unexpectedly succeeded: %s", args, output)
	}
	var decoded envelope
	if json.Unmarshal(output, &decoded) != nil || decoded.SchemaVersion != 1 || decoded.Error == nil || decoded.Error.Code != code {
		t.Fatalf("bt %v returned wrong error: %s", args, output)
	}
}

func runJSON(t *testing.T, args ...string) json.RawMessage {
	t.Helper()
	args = append([]string{"--json", "--timeout=30s"}, args...)
	// Only stdout carries the result; warnings and errors go to stderr.
	output, err := command(args...).Output()
	if err != nil {
		t.Fatalf("bt %v: %v\n%s", args, err, stderrOf(err))
	}
	var decoded envelope
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, output)
	}
	if decoded.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d", decoded.SchemaVersion)
	}
	return decoded.Data
}

// runMutation runs pair, unpair, connect, or disconnect and checks that the
// result names the action.
func runMutation(t *testing.T, args ...string) {
	t.Helper()
	var result struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(runJSON(t, append([]string{"--non-interactive"}, args...)...), &result); err != nil || result.Action != args[0] {
		t.Fatalf("bt %v: result=%+v err=%v", args, result, err)
	}
}

func stderrOf(err error) []byte {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.Stderr
	}
	return nil
}

func assertDeviceState(t *testing.T, query string, paired, connected bool) {
	t.Helper()
	var devices []domain.Device
	if err := json.Unmarshal(runJSON(t, "list"), &devices); err != nil {
		t.Fatal(err)
	}
	device, err := resolver.Resolve(devices, query)
	if err != nil {
		if !paired && domain.AsError(err).Code == domain.CodeNotFound {
			return
		}
		t.Fatal(err)
	}
	if device.Paired != paired || device.Connected != connected {
		t.Fatalf("device state paired=%t connected=%t, want paired=%t connected=%t", device.Paired, device.Connected, paired, connected)
	}
}

func TestGATTWatchCancellation(t *testing.T) {
	device := os.Getenv("BETTY_HARDWARE_BLE")
	characteristic := os.Getenv("BETTY_HARDWARE_WATCH_CHARACTERISTIC")
	if device == "" || characteristic == "" {
		t.Skip("set BETTY_HARDWARE_BLE and BETTY_HARDWARE_WATCH_CHARACTERISTIC")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := command("--json", "gatt", device, "watch", characteristic)
	cmd = exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	output, err := cmd.Output()
	if ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("watch exited before cancellation: %v\n%s", err, output)
	}
	for _, line := range bytesLines(output) {
		var decoded envelope
		if json.Unmarshal(line, &decoded) != nil || decoded.SchemaVersion != 1 {
			t.Fatalf("invalid watch JSON line: %s", line)
		}
	}
}

func TestSonyControlReadWriteReadback(t *testing.T) {
	if os.Getenv("BETTY_VALIDATE_SONY_CONTROLS") != "1" {
		t.Skip("set BETTY_VALIDATE_SONY_CONTROLS=1 for opt-in Sony control validation")
	}
	device := os.Getenv("BETTY_HARDWARE_SONY")
	if device == "" {
		t.Fatal("BETTY_HARDWARE_SONY is required when Sony control validation is enabled")
	}
	mode := propertyString(t, runJSON(t, "get", device, "noise_control"), "noise_control")
	if got := propertyString(t, runJSON(t, "set", device, "noise_control", mode), "noise_control"); got != mode {
		t.Fatalf("verified noise_control=%q, want %q", got, mode)
	}
	var inspected domain.Device
	if err := json.Unmarshal(runJSON(t, "inspect", device), &inspected); err != nil {
		t.Fatal(err)
	}
	if inspected.Capabilities["transparency_level"].Status != "available" {
		return
	}
	data := runJSON(t, "get", device, "transparency_level")
	var property struct {
		Property string          `json:"property"`
		Value    json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(data, &property); err == nil && property.Property == "transparency_level" {
		var level int
		if json.Unmarshal(property.Value, &level) != nil {
			t.Fatalf("invalid ambient level: %s", property.Value)
		}
		runJSON(t, "set", device, "transparency_level", fmt.Sprintf("%d", level))
	}
}

func TestAppleAudioControlReadWriteReadback(t *testing.T) {
	if os.Getenv("BETTY_VALIDATE_APPLE_AUDIO_CONTROLS") != "1" {
		t.Skip("set BETTY_VALIDATE_APPLE_AUDIO_CONTROLS=1 for opt-in Apple Audio control validation")
	}
	device := os.Getenv("BETTY_HARDWARE_APPLE_AUDIO")
	if device == "" {
		t.Fatal("BETTY_HARDWARE_APPLE_AUDIO is required when Apple Audio control validation is enabled")
	}
	mode := propertyString(t, runJSON(t, "get", device, "noise_control"), "noise_control")
	if got := propertyString(t, runJSON(t, "set", device, "noise_control", mode), "noise_control"); got != mode {
		t.Fatalf("verified noise_control=%q, want %q", got, mode)
	}
}

func propertyString(t *testing.T, data json.RawMessage, name string) string {
	t.Helper()
	var property struct {
		Property string `json:"property"`
		Value    string `json:"value"`
	}
	if err := json.Unmarshal(data, &property); err != nil || property.Property != name {
		t.Fatalf("invalid %s property: %s", name, data)
	}
	return property.Value
}

func bytesLines(value []byte) [][]byte {
	var lines [][]byte
	for _, line := range bytes.Split(value, []byte{'\n'}) {
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}

func command(args ...string) *exec.Cmd {
	binary := os.Getenv("BETTY_BINARY")
	if binary == "" {
		binary = "bt"
	}
	return exec.Command(binary, args...)
}
