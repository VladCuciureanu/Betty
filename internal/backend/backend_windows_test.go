//go:build windows

package backend

import (
	"syscall"
	"testing"
	"unsafe"

	"github.com/vladcuciureanu/betty/internal/domain"
)

func TestWindowsErrorUsesReturnedCode(t *testing.T) {
	cases := []struct {
		op    string
		errno syscall.Errno
		want  domain.ErrorCode
	}{
		{"unpair", errorNotFound, domain.CodeNotFound},
		{"unpair", syscall.ERROR_ACCESS_DENIED, domain.CodePermission},
		{"pair", errorCancelled, domain.CodeAuthentication},
		{"pair", errorTimeout, domain.CodeTimeout},
		{"enumerate", syscall.Errno(1), domain.CodeUnavailable},
	}
	for _, c := range cases {
		if got := domain.AsError(windowsError(c.op, c.errno)).Code; got != c.want {
			t.Errorf("windowsError(%s, %d) = %q, want %q", c.op, c.errno, got, c.want)
		}
	}
}

func TestDeviceInfoLayout(t *testing.T) {
	// BLUETOOTH_DEVICE_INFO is 560 bytes with its 8-byte-aligned address at
	// offset 8, on 32-bit and 64-bit Windows alike.
	if got := unsafe.Offsetof(bluetoothDeviceInfo{}.Address); got != 8 {
		t.Fatalf("Address offset=%d want=8", got)
	}
	if got := unsafe.Sizeof(bluetoothDeviceInfo{}); got != 560 {
		t.Fatalf("size=%d want=560", got)
	}
}

func TestDeviceSearchParamsLayout(t *testing.T) {
	// BLUETOOTH_DEVICE_SEARCH_PARAMS: six DWORD-sized fields, a UCHAR, then a
	// pointer-aligned HANDLE.
	want := uintptr(32)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		want = 28
	}
	if got := unsafe.Offsetof(deviceSearchParams{}.RadioHandle); got != want {
		t.Fatalf("RadioHandle offset=%d want=%d", got, want)
	}
}

func TestRadioInfoLayout(t *testing.T) {
	// BLUETOOTH_RADIO_INFO is 520 bytes with its address at offset 8, on
	// 32-bit and 64-bit Windows alike.
	if got := unsafe.Offsetof(bluetoothRadioInfo{}.Address); got != 8 {
		t.Fatalf("Address offset=%d want=8", got)
	}
	if got := unsafe.Sizeof(bluetoothRadioInfo{}); got != 520 {
		t.Fatalf("size=%d want=520", got)
	}
}
