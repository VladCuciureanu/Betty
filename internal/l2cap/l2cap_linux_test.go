//go:build linux

package l2cap

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

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

func TestWaitFDDoesNotReportReadyBeforeSubMillisecondDeadline(t *testing.T) {
	var fds [2]int
	if err := unix.Pipe2(fds[:], unix.O_CLOEXEC); err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fds[0])
	defer unix.Close(fds[1])
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Microsecond)
	defer cancel()
	if err := waitFD(ctx, fds[0], unix.POLLIN); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waitFD on an idle pipe = %v, want deadline exceeded", err)
	}
}
