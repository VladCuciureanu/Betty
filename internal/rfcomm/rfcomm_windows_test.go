//go:build windows

package rfcomm

import (
	"bytes"
	"testing"
)

func TestWindowsAddress(t *testing.T) {
	address, err := windowsAddress("windows:50:F3:51:CE:C5:60")
	if err != nil {
		t.Fatal(err)
	}
	if address != 0x50f351cec560 {
		t.Fatalf("address=%012x", address)
	}
}

func TestParseWindowsGUID(t *testing.T) {
	guid, err := parseWindowsGUID("96cc203e506846adb32de316f5e069ba")
	if err != nil {
		t.Fatal(err)
	}
	if guid.Data1 != 0x96cc203e || guid.Data2 != 0x5068 || guid.Data3 != 0x46ad || guid.Data4 != [8]byte{0xb3, 0x2d, 0xe3, 0x16, 0xf5, 0xe0, 0x69, 0xba} {
		t.Fatalf("unexpected GUID: %#v", guid)
	}
}

func TestSockaddrBTHIsPacked(t *testing.T) {
	guid, err := parseWindowsGUID("96cc203e-5068-46ad-b32d-e316f5e069ba")
	if err != nil {
		t.Fatal(err)
	}
	got := sockaddrBTH(0x50f351cec560, guid, 7)
	want := []byte{
		32, 0, // AF_BTH
		0x60, 0xc5, 0xce, 0x51, 0xf3, 0x50, 0, 0, // btAddr at offset 2
		0x3e, 0x20, 0xcc, 0x96, 0x68, 0x50, 0xad, 0x46, // GUID Data1-3, little-endian
		0xb3, 0x2d, 0xe3, 0x16, 0xf5, 0xe0, 0x69, 0xba, // GUID Data4
		7, 0, 0, 0, // port at offset 26
	}
	if !bytes.Equal(got[:], want) {
		t.Fatalf("SOCKADDR_BTH=%x want=%x", got, want)
	}
}
