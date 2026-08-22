//go:build windows

package rfcomm

import (
	"testing"
	"unsafe"
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

func TestSockaddrBTHLayout(t *testing.T) {
	if unsafe.Sizeof(sockaddrBTH{}) != 40 || unsafe.Offsetof(sockaddrBTH{}.Address) != 8 || unsafe.Offsetof(sockaddrBTH{}.Service) != 16 || unsafe.Offsetof(sockaddrBTH{}.Port) != 32 {
		t.Fatalf("invalid SOCKADDR_BTH layout: size=%d address=%d service=%d port=%d", unsafe.Sizeof(sockaddrBTH{}), unsafe.Offsetof(sockaddrBTH{}.Address), unsafe.Offsetof(sockaddrBTH{}.Service), unsafe.Offsetof(sockaddrBTH{}.Port))
	}
}
