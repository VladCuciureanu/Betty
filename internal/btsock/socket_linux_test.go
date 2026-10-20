//go:build linux

package btsock

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestWaitFDDoesNotReportReadyBeforeSubMillisecondDeadline(t *testing.T) {
	var fds [2]int
	if err := unix.Pipe2(fds[:], unix.O_CLOEXEC); err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fds[0])
	defer unix.Close(fds[1])
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Microsecond)
	defer cancel()
	if err := WaitFD(ctx, fds[0], unix.POLLIN); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitFD on an idle pipe = %v, want deadline exceeded", err)
	}
}

func TestCloseWaitsForAReadAndReportsTheChannelClosed(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fds[1])
	channels := NewChannels("L2CAP", "test-")
	id := channels.Add(fds[0])
	reading := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- channels.Use(id, func(fd int) error {
			close(reading)
			_, err := Read(context.Background(), fd, 64)
			return err
		})
	}()
	<-reading
	if err := channels.Close(id); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil || err.Error() != `L2CAP channel "test-1" is not open` {
			t.Fatalf("the interrupted read returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not wake the read")
	}
	if err := channels.Use(id, func(int) error { return nil }); err == nil {
		t.Fatal("a closed channel was still usable")
	}
}
