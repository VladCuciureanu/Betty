package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vladcuciureanu/betty/internal/domain"
)

// The backend contract: whatever a platform backend reports, bt passes on
// with its category and exit status intact, for every command that reaches
// the backend.

func TestBackendFailuresKeepTheirCategoryOnEveryCommand(t *testing.T) {
	failures := []domain.ErrorCode{
		domain.CodeUnavailable,    // adapter off or missing
		domain.CodePermission,     // OS permission denied
		domain.CodeAuthentication, // pairing refused or cancelled
		domain.CodeTimeout,        // the backend's own deadline
		domain.CodeDisconnected,   // the link dropped
	}
	commands := [][]string{
		{"list"},
		{"pair", "Speaker"},
		{"unpair", "Speaker"},
		{"connect", "Speaker"},
		{"disconnect", "Speaker"},
	}
	for _, code := range failures {
		for _, args := range commands {
			h := newHarness()
			speaker := classicDevice("os:1", "Speaker", "AA:BB:CC:DD:EE:02")
			// A state each mutation has to change, so it reaches the backend.
			switch args[0] {
			case "pair":
				speaker.Paired = false
			case "disconnect":
				speaker.Connected = true
			}
			h.backend.known = []domain.Device{speaker}
			failure := domain.NewError(code, "the platform failed")
			if args[0] == "list" {
				h.backend.err = failure
			} else {
				h.backend.mutationErr = failure
			}
			got := h.run(append([]string{"--json"}, args...)...)
			if got != domain.ExitCode(failure) || h.errorCode(t) != code {
				t.Errorf("%s from %v: exit %d, stderr %s", code, args, got, h.stderr.String())
			}
		}
	}
}

// blockingBackend's pair waits for its context, like a native pairing the
// user never answers.
type blockingBackend struct{ fakeBackend }

func (b *blockingBackend) Pair(ctx context.Context, _ domain.Device) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestABackendThatBlocksEndsAtTheDeadlineAsATimeout(t *testing.T) {
	speaker := classicDevice("os:1", "Speaker", "AA:BB:CC:DD:EE:02")
	speaker.Paired = false
	backend := &blockingBackend{fakeBackend{known: []domain.Device{speaker}}}
	h := newHarness()
	started := time.Now()
	var stdout, stderr strings.Builder
	code := run([]string{"--json", "--timeout", "200ms", "pair", "Speaker"}, &stdout, &stderr, "test", func(a *application) { a.backend, a.ble = backend, h.ble })
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("pair took %s after a 200ms timeout", elapsed)
	}
	if code != domain.ExitCode(domain.NewError(domain.CodeTimeout, "")) || !strings.Contains(stderr.String(), `"code":"timeout"`) || !strings.Contains(stderr.String(), "--timeout") {
		t.Fatalf("exit %d, stderr %s", code, stderr.String())
	}
}

func TestADroppedLinkEndsAGATTWatchWithDisconnected(t *testing.T) {
	h := newHarness()
	h.backend.known = []domain.Device{bleDevice("AA:BB:CC:DD:EE:01", "Sensor")}
	h.ble.watchErr = domain.NewError(domain.CodeDisconnected, "BLE device disconnected")
	code := h.run("--json", "gatt", "Sensor", "watch", "2a37")
	if code != domain.ExitCode(h.ble.watchErr) || h.errorCode(t) != domain.CodeDisconnected {
		t.Fatalf("exit %d, stderr %s", code, h.stderr.String())
	}
}

func TestARotatedBLEAddressIsNotMergedByName(t *testing.T) {
	// The OS remembers the sensor under its old private address; a scan
	// sees it advertise under a new one. Only an address joins endpoints, so
	// they stay two devices, and the name alone is ambiguous.
	h := newHarness()
	h.backend.scanned = []domain.Device{bleDevice("5A:00:00:00:00:01", "Sensor")}
	h.ble.devices = []domain.Device{bleDevice("5A:00:00:00:00:02", "Sensor")}
	if code := h.run("--json", "scan"); code != 0 {
		t.Fatalf("scan: exit %d, stderr %s", code, h.stderr.String())
	}
	var devices []domain.Device
	h.data(t, &devices)
	if len(devices) != 2 {
		t.Fatalf("devices=%+v", devices)
	}
}
