//go:build windows

package hidbattery

import "testing"

func TestScaleBatteryUsesTheLogicalRange(t *testing.T) {
	for _, c := range []struct {
		value, minimum, maximum int64
		want                    int
		ok                      bool
	}{
		{100, 0, 100, 100, true},
		{128, 0, 255, 50, true},
		{3, 1, 5, 50, true},
		{6, 1, 5, 0, false},
		{0, 0, 0, 0, false},
	} {
		if got, ok := scaleBattery(c.value, c.minimum, c.maximum); got != c.want || ok != c.ok {
			t.Errorf("scaleBattery(%d, %d, %d) = %d, %v", c.value, c.minimum, c.maximum, got, ok)
		}
	}
}
