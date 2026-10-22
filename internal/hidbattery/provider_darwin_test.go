//go:build darwin

package hidbattery

import (
	"context"
	"testing"
)

func TestDarwinProviderReturnsValidObservations(t *testing.T) {
	observations, err := New().Batteries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, observation := range observations {
		if normalizeAddress(observation.Address) == "" {
			t.Errorf("invalid address in observation: %#v", observation)
		}
		if _, valid := observation.value(); !valid {
			t.Errorf("invalid level in observation: %#v", observation)
		}
	}
	t.Logf("macOS HID battery observations: %#v", observations)
}
