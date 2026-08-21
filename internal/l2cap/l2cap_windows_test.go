//go:build windows

package l2cap

import "testing"

func TestWindowsAddress(t *testing.T) {
	address, err := windowsAddress("windows:50:F3:51:CE:C5:60")
	if err != nil {
		t.Fatal(err)
	}
	if address != 0x50f351cec560 {
		t.Fatalf("address=%012x", address)
	}
}

func TestWindowsAddressRejectsOpaqueID(t *testing.T) {
	if _, err := windowsAddress("windows:not-an-address"); err == nil {
		t.Fatal("expected address error")
	}
}
