//go:build e2e

package e2e_test

import (
	"runtime"
	"testing"

	"github.com/vladcuciureanu/betty/internal/domain"
)

// TestAdapterControl changes the computer's own adapter, so it runs only on
// the virtual link, where that adapter exists just for the test.
func TestAdapterControl(t *testing.T) {
	requireVirtual(t)
	if runtime.GOOS != "linux" {
		t.Skip("only Linux can change the adapter")
	}
	startPeer(t, "sensor:Betty Sensor") // the peer creates the adapter
	waitForAdapter(t)
	var adapters []domain.Adapter
	ok(t, &adapters, "adapter")
	if len(adapters) != 1 || !adapters[0].Default || adapters[0].Powered == nil || !*adapters[0].Powered {
		t.Fatalf("adapters=%+v", adapters)
	}
	original := adapters[0].Name
	t.Cleanup(func() {
		bt(t, nil, "adapter", "power", "on")
		bt(t, nil, "adapter", "discoverable", "off")
		bt(t, nil, "adapter", "name", original)
	})

	var state domain.Adapter
	ok(t, &state, "adapter", "name", "Betty Test Adapter")
	if state.Name != "Betty Test Adapter" {
		t.Fatalf("name=%q", state.Name)
	}
	ok(t, &state, "adapter", "discoverable", "on")
	if state.Discoverable == nil || !*state.Discoverable {
		t.Fatalf("state=%+v", state)
	}
	ok(t, &state, "adapter", "power", "off")
	if state.Powered == nil || *state.Powered {
		t.Fatalf("state=%+v", state)
	}
	ok(t, &state, "adapter", "power", "on")
	if state.Powered == nil || !*state.Powered {
		t.Fatalf("state=%+v", state)
	}
}
