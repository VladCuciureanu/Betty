package resolver

import (
	"testing"

	"github.com/vladcuciureanu/betty/internal/domain"
)

func TestResolvePrecedence(t *testing.T) {
	devices := []domain.Device{{ID: "stable", Name: "Other"}, {ID: "second", Name: "AirPods Pro"}}
	for _, test := range []struct{ query, id string }{{"stable", "stable"}, {"AirPods Pro", "second"}, {"airpods pro", "second"}, {"pods", "second"}} {
		d, err := Resolve(devices, test.query)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", test.query, err)
		}
		if d.ID != test.id {
			t.Fatalf("Resolve(%q) ID = %q", test.query, d.ID)
		}
	}
}
func TestResolveAmbiguous(t *testing.T) {
	_, err := Resolve([]domain.Device{{ID: "1", Name: "Office Keyboard"}, {ID: "2", Name: "Travel Keyboard"}}, "keyboard")
	if domain.AsError(err).Code != domain.CodeAmbiguous {
		t.Fatalf("code = %q", domain.AsError(err).Code)
	}
}
func TestResolveNotFound(t *testing.T) {
	_, err := Resolve(nil, "missing")
	if domain.AsError(err).Code != domain.CodeNotFound {
		t.Fatalf("code = %q", domain.AsError(err).Code)
	}
}
func TestResolvePrefersPairedPossibleMatch(t *testing.T) {
	devices := []domain.Device{{ID: "classic", Name: "AirPods Pro", Paired: true}, {ID: "ble", Name: "AirPods Pro 2", PossibleMatches: []string{"classic"}}}
	got, err := Resolve(devices, "airpods")
	if err != nil || got.ID != "classic" {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}
