package btsock

import "testing"

func TestParseAddress(t *testing.T) {
	want := [6]byte{0x50, 0xf3, 0x51, 0xce, 0xc5, 0x60}
	for _, id := range []string{"bluez:50-F3-51-CE-C5-60", "bluez:50:f3:51:ce:c5:60", "bluez:50F351CEC560"} {
		got, err := ParseAddress(id, "bluez:")
		if err != nil || got != want {
			t.Errorf("%s: address=%x err=%v", id, got, err)
		}
	}
	for _, id := range []string{"bluez:not-an-address", "bluez:50:F3:51:CE:C5", "bluez:50:F3:51:CE:C5:6Z"} {
		if _, err := ParseAddress(id, "bluez:"); err == nil {
			t.Errorf("%s: expected an address error", id)
		}
	}
	if got := Uint64(want); got != 0x50f351cec560 {
		t.Errorf("Uint64=%x", got)
	}
}
