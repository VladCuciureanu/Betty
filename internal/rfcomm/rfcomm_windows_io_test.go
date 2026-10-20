//go:build windows

package rfcomm

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
	"unsafe"

	"github.com/vladcuciureanu/betty/internal/domain"
)

// loopbackChannel opens a non-blocking Winsock TCP socket to a local
// listener, the same way OpenRFCOMM leaves an RFCOMM socket, and registers it
// as a channel. It returns the channel ID and the listener's end.
func loopbackChannel(t *testing.T, h *windowsHost) (string, net.Conn) {
	t.Helper()
	if err := ensureWinsock(); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	const afINET, ipprotoTCP = 2, 6
	socket, _, callErr := wsaSocketW.Call(afINET, sockStream, ipprotoTCP, 0, 0, 0)
	if socket == invalidSocket {
		t.Fatal(callErr)
	}
	// sockaddr_in: family, port (big-endian), IPv4 address, padding.
	var address [16]byte
	binary.LittleEndian.PutUint16(address[0:2], afINET)
	binary.BigEndian.PutUint16(address[2:4], uint16(listener.Addr().(*net.TCPAddr).Port))
	copy(address[4:8], []byte{127, 0, 0, 1})
	if result, _, callErr := wsaConnect.Call(socket, uintptr(unsafe.Pointer(&address[0])), uintptr(len(address))); failed(result) {
		t.Fatal(callErr)
	}
	if err := setNonblocking(socket, true); err != nil {
		t.Fatal(err)
	}
	peer, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { peer.Close() })
	id := "test-channel"
	h.mu.Lock()
	h.channels[id] = &windowsChannel{socket: socket}
	h.mu.Unlock()
	return id, peer
}

func TestAReadTimeoutLeavesTheChannelUsable(t *testing.T) {
	h := New().(*windowsHost)
	id, peer := loopbackChannel(t, h)
	defer h.CloseRFCOMM(context.Background(), id)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	_, err := h.ReadRFCOMM(ctx, id, 16)
	cancel()
	if domain.AsError(err).Code != domain.CodeTimeout {
		t.Fatalf("a read with no data returned %v, want a timeout", err)
	}
	if _, err := peer.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	data, err := h.ReadRFCOMM(ctx, id, 16)
	if err != nil || string(data) != "hi" {
		t.Fatalf("the read after a timeout returned %q, %v", data, err)
	}
	if n, err := h.WriteRFCOMM(ctx, id, []byte("ok")); err != nil || n != 2 {
		t.Fatalf("write=%d err=%v", n, err)
	}
	buffer := make([]byte, 2)
	_ = peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := peer.Read(buffer); err != nil || string(buffer) != "ok" {
		t.Fatalf("peer read %q, %v", buffer, err)
	}
}

func TestCloseWakesAWaitingRead(t *testing.T) {
	h := New().(*windowsHost)
	id, _ := loopbackChannel(t, h)
	result := make(chan error, 1)
	go func() {
		_, err := h.ReadRFCOMM(context.Background(), id, 16)
		result <- err
	}()
	time.Sleep(100 * time.Millisecond)
	if err := h.CloseRFCOMM(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if domain.AsError(err).Code != domain.CodeNotFound {
			t.Fatalf("the interrupted read returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not wake the read")
	}
}
