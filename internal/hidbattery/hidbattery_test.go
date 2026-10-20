package hidbattery

import (
	"testing"

	"github.com/vladcuciureanu/betty/internal/domain"
)

func TestApplyCorrelatesAddressWithoutUsingName(t *testing.T) {
	devices := []domain.Device{
		{ID: "trackpad", Name: "Vlad’s Magic Trackpad", Endpoints: []domain.Endpoint{{Address: "00-81-2a-f1-46-01"}}},
		{ID: "same-name", Name: "Vlad’s Magic Trackpad", Endpoints: []domain.Endpoint{{Address: "11:22:33:44:55:66"}}},
	}
	Apply(devices, []Observation{{Address: "00:81:2A:F1:46:01", Percentage: 90}})
	if got := string(devices[0].Properties["battery"].Value); got != `{"level":90}` {
		t.Fatalf("battery=%s", got)
	}
	if _, guessed := devices[1].Properties["battery"]; guessed {
		t.Fatal("battery was correlated by display name")
	}
}

func TestApplyPreservesExistingBatteryAndRejectsAmbiguity(t *testing.T) {
	devices := []domain.Device{{Endpoints: []domain.Endpoint{{Address: "00:11:22:33:44:55"}}, Properties: map[string]domain.Property{"battery": {Value: []byte("75")}}}, {Endpoints: []domain.Endpoint{{Address: "AA:BB:CC:DD:EE:FF"}}}}
	observations := []Observation{{Address: "00:11:22:33:44:55", Percentage: 90}, {Address: "AA:BB:CC:DD:EE:FF", Percentage: 10}, {Address: "aa-bb-cc-dd-ee-ff", Percentage: 20}}
	Apply(devices, observations)
	if got := string(devices[0].Properties["battery"].Value); got != "75" {
		t.Fatalf("existing battery overwritten with %s", got)
	}
	if _, exists := devices[1].Properties["battery"]; exists {
		t.Fatal("ambiguous observation was applied")
	}
}
