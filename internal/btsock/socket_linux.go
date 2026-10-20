//go:build linux

package btsock

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/vladcuciureanu/betty/internal/domain"
)

// Channels tracks open sockets by ID. Every read or write holds its channel
// for as long as it runs, so Close can't close the file descriptor under it
// and let the kernel hand the same number to another socket.
type Channels struct {
	kind   string // "L2CAP" or "RFCOMM", for messages
	prefix string // ID prefix

	mu   sync.Mutex
	open map[string]*channel
	next uint64
}

type channel struct {
	fd int
	// In use while held for reading; Close holds it for writing.
	inUse  sync.RWMutex
	closed bool // set under the Channels lock
}

func NewChannels(kind, prefix string) *Channels {
	return &Channels{kind: kind, prefix: prefix, open: map[string]*channel{}}
}

// Add registers an open socket and returns its ID.
func (c *Channels) Add(fd int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	id := c.prefix + strconv.FormatUint(c.next, 10)
	c.open[id] = &channel{fd: fd}
	return id
}

// Use runs f with the channel's file descriptor while holding the channel
// open.
func (c *Channels) Use(id string, f func(fd int) error) error {
	c.mu.Lock()
	ch, ok := c.open[id]
	if ok {
		ch.inUse.RLock()
	}
	c.mu.Unlock()
	if !ok {
		return c.notOpen(id)
	}
	err := f(ch.fd)
	ch.inUse.RUnlock()
	c.mu.Lock()
	closed := ch.closed
	c.mu.Unlock()
	if closed {
		// The error f saw came from Close shutting the socket down.
		return c.notOpen(id)
	}
	return err
}

// Close closes a channel. It wakes any read or write still using the
// channel, waits for them to return, and only then closes the descriptor.
func (c *Channels) Close(id string) error {
	c.mu.Lock()
	ch, ok := c.open[id]
	if ok {
		delete(c.open, id)
		ch.closed = true
	}
	c.mu.Unlock()
	if !ok {
		return c.notOpen(id)
	}
	_ = unix.Shutdown(ch.fd, unix.SHUT_RDWR)
	ch.inUse.Lock()
	defer ch.inUse.Unlock()
	return unix.Close(ch.fd)
}

func (c *Channels) notOpen(id string) error {
	return domain.NewError(domain.CodeNotFound, "%s channel %q is not open", c.kind, id)
}

// ErrPeerClosed reports a read of zero bytes: the device closed the channel.
var ErrPeerClosed = errors.New("channel closed by the device")

// Connect connects a non-blocking socket, waiting for ctx.
func Connect(ctx context.Context, fd int, address unix.Sockaddr) error {
	err := unix.Connect(fd, address)
	if err == nil {
		return nil
	}
	if !errors.Is(err, unix.EINPROGRESS) {
		return err
	}
	if err := WaitFD(ctx, fd, unix.POLLOUT); err != nil {
		return err
	}
	socketErr, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_ERROR)
	if err != nil {
		return err
	}
	if socketErr != 0 {
		return unix.Errno(socketErr)
	}
	return nil
}

// Read reads at most max bytes from a non-blocking socket, waiting for ctx.
// Reading zero bytes returns ErrPeerClosed.
func Read(ctx context.Context, fd, max int) ([]byte, error) {
	for {
		buffer := make([]byte, max)
		n, err := unix.Read(fd, buffer)
		if err == nil {
			if n == 0 {
				return nil, ErrPeerClosed
			}
			return buffer[:n], nil
		}
		if !errors.Is(err, unix.EAGAIN) {
			return nil, err
		}
		if err := WaitFD(ctx, fd, unix.POLLIN); err != nil {
			return nil, err
		}
	}
}

// Write writes data in one call, as one packet on a packet socket.
func Write(ctx context.Context, fd int, data []byte) (int, error) {
	for {
		n, err := unix.Write(fd, data)
		if err == nil {
			return n, nil
		}
		if !errors.Is(err, unix.EAGAIN) {
			return 0, err
		}
		if err := WaitFD(ctx, fd, unix.POLLOUT); err != nil {
			return 0, err
		}
	}
}

// WriteAll writes all of data to a stream socket.
func WriteAll(ctx context.Context, fd int, data []byte) (int, error) {
	written := 0
	for written < len(data) {
		n, err := Write(ctx, fd, data[written:])
		written += n
		if err != nil {
			return written, err
		}
	}
	return written, nil
}

// WaitFD waits until fd is ready for events or ctx ends.
func WaitFD(ctx context.Context, fd int, events int16) error {
	for {
		timeout := 200
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return context.DeadlineExceeded
			}
			// Round up: truncating a sub-millisecond remainder to 0 would
			// return before the deadline with ctx.Err() still nil, and the
			// caller would treat a still-connecting socket as ready.
			if ms := int((remaining + time.Millisecond - 1) / time.Millisecond); ms < timeout {
				timeout = ms
			}
		}
		n, err := unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: events}}, timeout)
		if err != nil && !errors.Is(err, unix.EINTR) {
			return err
		}
		if n > 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}
