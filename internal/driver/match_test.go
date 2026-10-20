package driver

import (
	"context"
	"testing"
	"time"

	protocol "github.com/vladcuciureanu/betty/driver"
	"github.com/vladcuciureanu/betty/internal/domain"
)

func TestMatchHints(t *testing.T) {
	match := &protocol.Match{CompanyIDs: []uint16{0x004c}, ServiceUUIDs: []string{"180F", "956c7b26-d49a-4ba8-b03f-b17d393cb6e2"}, NameContains: []string{"AirPods"}}
	cases := []struct {
		name   string
		device domain.Device
		want   bool
	}{
		{"company ID", domain.Device{ManufacturerData: []domain.ManufacturerData{{CompanyID: 0x004c}}}, true},
		{"other company", domain.Device{ManufacturerData: []domain.ManufacturerData{{CompanyID: 0x0006}}}, false},
		{"16-bit service as 128-bit", domain.Device{Services: []string{"0000180f-0000-1000-8000-00805f9b34fb"}}, true},
		{"128-bit service without dashes", domain.Device{Services: []string{"956C7B26D49A4BA8B03FB17D393CB6E2"}}, true},
		{"service data", domain.Device{ServiceData: []domain.ServiceData{{UUID: "180f"}}}, true},
		{"name, any case", domain.Device{Name: "Vlad's airpods Pro"}, true},
		{"nothing", domain.Device{Name: "Keyboard", Services: []string{"1812"}}, false},
	}
	for _, tc := range cases {
		if got := matches(match, tc.device); got != tc.want {
			t.Errorf("%s: matches=%t, want %t", tc.name, got, tc.want)
		}
	}
	if !matches(nil, domain.Device{}) || !matchesAny(nil, nil) {
		t.Error("a driver without hints must get every request")
	}
	if matchesAny(match, nil) {
		t.Error("a hinted driver must not run for a request without devices")
	}
}

func TestDriversWhoseHintsDontMatchAreNotStarted(t *testing.T) {
	m := newTestManager(t)
	// This driver hangs if started, which would make inspect fail.
	skipped := protocol.Manifest{ID: "example/skipped", Name: "skipped", Version: "1", Match: &protocol.Match{NameContains: []string{"AirPods"}}}
	matched := protocol.Manifest{ID: "example/matched", Name: "matched", Version: "1", Match: &protocol.Match{NameContains: []string{"sensor"}}}
	installTestDriver(t, m.root, skipped)
	installTestDriver(t, m.root, matched)
	t.Setenv(testDriverEnv, skipped.ID+"=hang,"+matched.ID+"=ok")
	m.OnInspectError = func(id string, err error) { t.Errorf("driver %s ran: %v", id, err) }
	started := time.Now()
	devices := m.Inspect(context.Background(), []domain.Device{{ID: "device", Name: "Kitchen sensor"}})
	if devices[0].Identification == nil || devices[0].Identification.Vendor != matched.ID {
		t.Fatalf("devices=%+v", devices)
	}
	if elapsed := time.Since(started); elapsed > m.inspectBudget() {
		t.Fatalf("inspect took %s; the skipped driver was started", elapsed)
	}
}

func TestInvalidMatchUUIDsAreRejected(t *testing.T) {
	err := validateManifest(protocol.Manifest{Protocol: protocol.Version, ID: "example/bad", Match: &protocol.Match{ServiceUUIDs: []string{"not-a-uuid"}}})
	if domain.AsError(err).Code != domain.CodeUsage {
		t.Fatalf("err=%v", err)
	}
}
