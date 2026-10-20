//go:build windows

package l2cap

import (
	"errors"
	"strings"
	"syscall"
	"testing"

	"github.com/vladcuciureanu/betty/internal/domain"
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

func TestWindowsAddressRejectsOpaqueID(t *testing.T) {
	if _, err := windowsAddress("windows:not-an-address"); err == nil {
		t.Fatal("expected address error")
	}
}

func TestIOCTLCodesMatchDriverHeader(t *testing.T) {
	const fileReadData, fileWriteData = 1, 2
	ctlCode := func(function, access uint32) uint32 {
		const fileDeviceBetty, methodBuffered = 0x8000, 0
		return fileDeviceBetty<<16 | access<<14 | function<<2 | methodBuffered
	}
	cases := map[string]struct{ got, want uint32 }{
		"connect":     {ioctlConnect, ctlCode(0x800, fileReadData|fileWriteData)},
		"disconnect":  {ioctlDisconnect, ctlCode(0x801, fileReadData|fileWriteData)},
		"send":        {ioctlSend, ctlCode(0x802, fileWriteData)},
		"receive":     {ioctlReceive, ctlCode(0x803, fileReadData)},
		"get address": {ioctlGetAddress, ctlCode(0x804, fileReadData)},
	}
	for name, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: 0x%08x, want 0x%08x", name, c.got, c.want)
		}
	}
}

func TestOpenBoundInstanceSelectsMatchingAddress(t *testing.T) {
	bound := map[string]uint64{`\?\a`: 0x111111111111, `\?\b`: 0x50f351cec560, `\?\c`: 0x222222222222}
	handles := map[string]syscall.Handle{`\?\a`: 1, `\?\b`: 2, `\?\c`: 3}
	var closed []syscall.Handle
	probe := func(path string) (syscall.Handle, uint64, error) { return handles[path], bound[path], nil }
	closeFn := func(h syscall.Handle) error { closed = append(closed, h); return nil }

	handle, err := openBoundInstance([]string{`\?\a`, `\?\b`, `\?\c`}, 0x50f351cec560, probe, closeFn)
	if err != nil {
		t.Fatal(err)
	}
	if handle != 2 {
		t.Fatalf("handle=%d, want 2", handle)
	}
	if len(closed) != 1 || closed[0] != 1 {
		t.Fatalf("closed=%v, want only the non-matching instance probed first", closed)
	}
}

func TestOpenBoundInstanceReportsUnboundDevice(t *testing.T) {
	probe := func(string) (syscall.Handle, uint64, error) { return 1, 0x111111111111, nil }
	closeFn := func(syscall.Handle) error { return nil }
	_, err := openBoundInstance([]string{`\?\a`}, 0x50f351cec560, probe, closeFn)
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != domain.CodeUnsupported {
		t.Fatalf("err=%v, want unsupported", err)
	}
	if !strings.Contains(err.Error(), "50:F3:51:CE:C5:60") {
		t.Fatalf("err=%q does not name the device", err)
	}
}

func TestOpenBoundInstanceSurfacesOpenFailures(t *testing.T) {
	probe := func(string) (syscall.Handle, uint64, error) {
		return syscall.InvalidHandle, 0, syscall.ERROR_ACCESS_DENIED
	}
	closeFn := func(syscall.Handle) error { t.Fatal("unexpected close"); return nil }
	_, err := openBoundInstance([]string{`\?\a`}, 0x50f351cec560, probe, closeFn)
	if !errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
		t.Fatalf("err=%v, want access denied", err)
	}
}
