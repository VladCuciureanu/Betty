//go:build linux

package l2cap

import "testing"

func TestLinuxAddress(t *testing.T) {
	address, err := linuxAddress("bluez:50-F3-51-CE-C5-60")
	if err != nil {
		t.Fatal(err)
	}
	want := [6]byte{0x50, 0xf3, 0x51, 0xce, 0xc5, 0x60}
	if address != want {
		t.Fatalf("address=%x want=%x", address, want)
	}
}

func TestLinuxAddressRejectsOpaqueID(t *testing.T) {
	if _, err := linuxAddress("bluez:not-an-address"); err == nil {
		t.Fatal("expected address error")
	}
}
