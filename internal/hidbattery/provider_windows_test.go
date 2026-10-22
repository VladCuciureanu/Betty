//go:build windows

package hidbattery

import "testing"

func TestParentAddressReadsBluetoothParents(t *testing.T) {
	cases := map[string]string{
		`BTHENUM\{00001124-0000-1000-8000-00805F9B34FB}_VID&0001004C_PID&029C\A&AE70F6&0&B0BE83F34CD4_C00000000`: "b0be83f34cd4",
		`USB\VID_046D&PID_C52B\5&1A2B3C&0&2`: "",
		`BTHENUM\{00001124-0000-1000-8000-00805F9B34FB}_VID&0001004C_PID&029C\A&AE70F6&0&NOTANADDRESS_C00000000`: "",
	}
	for parent, want := range cases {
		if got := parentAddress(parent); got != want {
			t.Errorf("parentAddress(%q) = %q, want %q", parent, got, want)
		}
	}
}

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
