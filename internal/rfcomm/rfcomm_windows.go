//go:build windows

package rfcomm

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/vladcuciureanu/betty/internal/domain"
	"github.com/vladcuciureanu/betty/internal/driver"
)

const (
	afBTH          = 32
	sockStream     = 1
	bthProtoRFCOMM = 3
	socketError    = ^uintptr(0)
	solSocket      = 0xffff
	soError        = 0x1007
	soRecvTimeout  = 0x1006
	soSendTimeout  = 0x1005
	fionbio        = 0x8004667e
	wsaWouldBlock  = syscall.Errno(10035)
	wsaTimedOut    = syscall.Errno(10060)
)

var (
	ws2            = syscall.NewLazyDLL("ws2_32.dll")
	wsaStartup     = ws2.NewProc("WSAStartup")
	wsaSocketW     = ws2.NewProc("WSASocketW")
	wsaConnect     = ws2.NewProc("connect")
	wsaRecv        = ws2.NewProc("recv")
	wsaSend        = ws2.NewProc("send")
	wsaCloseSocket = ws2.NewProc("closesocket")
	wsaSetSockOpt  = ws2.NewProc("setsockopt")
	wsaGetSockOpt  = ws2.NewProc("getsockopt")
	wsaIOCtlSocket = ws2.NewProc("ioctlsocket")
	wsaSelect      = ws2.NewProc("select")
	wsaInitOnce    sync.Once
	wsaInitErr     error
)

type windowsGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// sockaddrBTHSize is sizeof(SOCKADDR_BTH). ws2bth.h declares it inside
// #include <pshpack1.h>, so it is packed: addressFamily at 0, btAddr at 2,
// serviceClassId at 10 and port at 26. A Go struct would pad btAddr to 8.
const sockaddrBTHSize = 30

func sockaddrBTH(address uint64, service windowsGUID, port uint32) [sockaddrBTHSize]byte {
	var out [sockaddrBTHSize]byte
	binary.LittleEndian.PutUint16(out[0:2], afBTH)
	binary.LittleEndian.PutUint64(out[2:10], address)
	binary.LittleEndian.PutUint32(out[10:14], service.Data1)
	binary.LittleEndian.PutUint16(out[14:16], service.Data2)
	binary.LittleEndian.PutUint16(out[16:18], service.Data3)
	copy(out[18:26], service.Data4[:])
	binary.LittleEndian.PutUint32(out[26:30], port)
	return out
}

// fdSet mirrors fd_set; natural alignment pads Count before the SOCKET
// array on 64-bit Windows, as the C struct does.
type fdSet struct {
	Count   uint32
	Sockets [64]uintptr
}

type timeval struct {
	Sec  int32
	Usec int32
}

type windowsChannel struct {
	socket  uintptr
	readMu  sync.Mutex
	writeMu sync.Mutex
}

type windowsHost struct {
	mu       sync.Mutex
	channels map[string]*windowsChannel
	next     atomic.Uint64
}

func New() driver.RFCOMMHost {
	return &windowsHost{channels: map[string]*windowsChannel{}}
}

func (h *windowsHost) OpenRFCOMM(ctx context.Context, deviceID, serviceUUID string) (string, error) {
	if err := ensureWinsock(); err != nil {
		return "", err
	}
	address, err := windowsAddress(deviceID)
	if err != nil {
		return "", err
	}
	service, err := parseWindowsGUID(serviceUUID)
	if err != nil {
		return "", err
	}
	// LazyProc.Call returns GetLastError (which WSAGetLastError wraps) as its
	// third value, read on the same OS thread right after the call. A
	// separate WSAGetLastError call could run on another thread.
	socket, _, callErr := wsaSocketW.Call(afBTH, sockStream, bthProtoRFCOMM, 0, 0, 0)
	if socket == socketError {
		return "", winsockError("open RFCOMM socket", callErr)
	}
	ok := false
	defer func() {
		if !ok {
			wsaCloseSocket.Call(socket)
		}
	}()
	if err := setNonblocking(socket, true); err != nil {
		return "", err
	}
	// A zero port tells Winsock to resolve the remote RFCOMM channel from the
	// service UUID through SDP. BT_PORT_ANY is only for server-side bind.
	addressData := sockaddrBTH(address, service, 0)
	result, _, connectErr := wsaConnect.Call(socket, uintptr(unsafe.Pointer(&addressData[0])), sockaddrBTHSize)
	if result == socketError {
		if !errors.Is(connectErr, wsaWouldBlock) {
			return "", winsockError("connect RFCOMM service", connectErr)
		}
		if err := waitConnected(ctx, socket); err != nil {
			return "", err
		}
	}
	if err := setNonblocking(socket, false); err != nil {
		return "", err
	}
	id := "rfcomm-windows-" + strconv.FormatUint(h.next.Add(1), 10)
	h.mu.Lock()
	h.channels[id] = &windowsChannel{socket: socket}
	h.mu.Unlock()
	ok = true
	return id, nil
}

func (h *windowsHost) ReadRFCOMM(ctx context.Context, channelID string, max int) ([]byte, error) {
	if max <= 0 {
		return nil, domain.NewError(domain.CodeUsage, "RFCOMM read size must be positive")
	}
	channel, err := h.channel(channelID)
	if err != nil {
		return nil, err
	}
	channel.readMu.Lock()
	defer channel.readMu.Unlock()
	buffer := make([]byte, max)
	for {
		if err := ctx.Err(); err != nil {
			return nil, contextError(err)
		}
		if err := setSocketTimeout(channel.socket, soRecvTimeout, pollTimeout(ctx)); err != nil {
			return nil, err
		}
		result, _, callErr := wsaRecv.Call(channel.socket, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
		if result != socketError {
			if result == 0 {
				return nil, domain.NewError(domain.CodeDisconnected, "RFCOMM channel closed by the device")
			}
			return buffer[:int(result)], nil
		}
		if errors.Is(callErr, wsaTimedOut) || errors.Is(callErr, wsaWouldBlock) {
			continue
		}
		return nil, winsockError("read RFCOMM channel", callErr)
	}
}

func (h *windowsHost) WriteRFCOMM(ctx context.Context, channelID string, data []byte) (int, error) {
	channel, err := h.channel(channelID)
	if err != nil {
		return 0, err
	}
	channel.writeMu.Lock()
	defer channel.writeMu.Unlock()
	written := 0
	for written < len(data) {
		if err := ctx.Err(); err != nil {
			return written, contextError(err)
		}
		if err := setSocketTimeout(channel.socket, soSendTimeout, pollTimeout(ctx)); err != nil {
			return written, err
		}
		result, _, callErr := wsaSend.Call(channel.socket, uintptr(unsafe.Pointer(&data[written])), uintptr(len(data)-written), 0)
		if result != socketError {
			written += int(result)
			continue
		}
		if errors.Is(callErr, wsaTimedOut) || errors.Is(callErr, wsaWouldBlock) {
			continue
		}
		return written, winsockError("write RFCOMM channel", callErr)
	}
	return written, nil
}

func (h *windowsHost) CloseRFCOMM(_ context.Context, channelID string) error {
	h.mu.Lock()
	channel, ok := h.channels[channelID]
	if ok {
		delete(h.channels, channelID)
	}
	h.mu.Unlock()
	if !ok {
		return domain.NewError(domain.CodeNotFound, "RFCOMM channel %q is not open", channelID)
	}
	result, _, callErr := wsaCloseSocket.Call(channel.socket)
	if result == socketError {
		return winsockError("close RFCOMM channel", callErr)
	}
	return nil
}

func (h *windowsHost) channel(id string) (*windowsChannel, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	channel, ok := h.channels[id]
	if !ok {
		return nil, domain.NewError(domain.CodeNotFound, "RFCOMM channel %q is not open", id)
	}
	return channel, nil
}

func ensureWinsock() error {
	wsaInitOnce.Do(func() {
		var data [512]byte
		result, _, _ := wsaStartup.Call(0x0202, uintptr(unsafe.Pointer(&data[0])))
		if result != 0 {
			wsaInitErr = winsockError("initialize Winsock", syscall.Errno(result))
		}
	})
	return wsaInitErr
}

// waitConnected waits for a non-blocking connect to finish. Winsock signals
// success through writefds but failure only through exceptfds, so watching
// writefds alone turns a refused connect into a wait for the deadline.
func waitConnected(ctx context.Context, socket uintptr) error {
	for {
		if err := ctx.Err(); err != nil {
			return contextError(err)
		}
		writable := fdSet{Count: 1}
		writable.Sockets[0] = socket
		failed := fdSet{Count: 1}
		failed.Sockets[0] = socket
		wait := pollTimeout(ctx)
		tv := timeval{Sec: int32(wait / time.Second), Usec: int32((wait % time.Second) / time.Microsecond)}
		result, _, callErr := wsaSelect.Call(0, 0, uintptr(unsafe.Pointer(&writable)), uintptr(unsafe.Pointer(&failed)), uintptr(unsafe.Pointer(&tv)))
		if result == socketError {
			return winsockError("wait for RFCOMM connection", callErr)
		}
		if result == 0 {
			continue
		}
		var socketErr int32
		length := int32(unsafe.Sizeof(socketErr))
		result, _, callErr = wsaGetSockOpt.Call(socket, solSocket, soError, uintptr(unsafe.Pointer(&socketErr)), uintptr(unsafe.Pointer(&length)))
		if result == socketError {
			return winsockError("inspect RFCOMM connection", callErr)
		}
		if socketErr != 0 {
			return winsockError("connect RFCOMM service", syscall.Errno(socketErr))
		}
		if failed.Count != 0 {
			return winsockError("connect RFCOMM service", errors.New("connection failed"))
		}
		return nil
	}
}

func setNonblocking(socket uintptr, enabled bool) error {
	var value uint32
	if enabled {
		value = 1
	}
	result, _, callErr := wsaIOCtlSocket.Call(socket, fionbio, uintptr(unsafe.Pointer(&value)))
	if result == socketError {
		return winsockError("configure RFCOMM socket", callErr)
	}
	return nil
}

func setSocketTimeout(socket uintptr, option uintptr, timeout time.Duration) error {
	milliseconds := int32(timeout / time.Millisecond)
	if milliseconds < 1 {
		milliseconds = 1
	}
	result, _, callErr := wsaSetSockOpt.Call(socket, solSocket, option, uintptr(unsafe.Pointer(&milliseconds)), unsafe.Sizeof(milliseconds))
	if result == socketError {
		return winsockError("configure RFCOMM timeout", callErr)
	}
	return nil
}

func pollTimeout(ctx context.Context) time.Duration {
	wait := 250 * time.Millisecond
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < wait {
			return maxDuration(remaining, time.Millisecond)
		}
	}
	return wait
}

func contextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.NewError(domain.CodeTimeout, "RFCOMM operation timed out")
	}
	return err
}

func windowsAddress(deviceID string) (uint64, error) {
	value := strings.TrimPrefix(deviceID, "windows:")
	value = strings.NewReplacer(":", "", "-", "").Replace(value)
	if len(value) != 12 {
		return 0, domain.NewError(domain.CodeUsage, "device %q has no Windows Classic address", deviceID)
	}
	address, err := strconv.ParseUint(value, 16, 48)
	if err != nil {
		return 0, domain.NewError(domain.CodeUsage, "device %q has an invalid Bluetooth address", deviceID)
	}
	return address, nil
}

func parseWindowsGUID(value string) (windowsGUID, error) {
	original := value
	value = strings.ReplaceAll(strings.TrimSpace(strings.Trim(value, "{}")), "-", "")
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 16 {
		return windowsGUID{}, domain.NewError(domain.CodeUsage, "invalid 128-bit service UUID %q", original)
	}
	guid := windowsGUID{
		Data1: binary.BigEndian.Uint32(decoded[0:4]),
		Data2: binary.BigEndian.Uint16(decoded[4:6]),
		Data3: binary.BigEndian.Uint16(decoded[6:8]),
	}
	copy(guid.Data4[:], decoded[8:])
	return guid, nil
}

func winsockError(operation string, err error) error {
	if errors.Is(err, syscall.Errno(10013)) {
		return domain.NewError(domain.CodeAuthentication, "%s: Windows could not authenticate the Bluetooth service", operation)
	}
	if errors.Is(err, syscall.Errno(10050)) || errors.Is(err, syscall.Errno(10051)) || errors.Is(err, syscall.Errno(10064)) {
		return domain.NewError(domain.CodeUnavailable, "%s: Bluetooth device or adapter is unavailable", operation)
	}
	if errors.Is(err, wsaTimedOut) {
		return domain.NewError(domain.CodeTimeout, "%s timed out", operation)
	}
	if errors.Is(err, syscall.Errno(10061)) || errors.Is(err, syscall.Errno(10053)) || errors.Is(err, syscall.Errno(10054)) {
		return domain.NewError(domain.CodeDisconnected, "%s: the device refused or closed the connection", operation)
	}
	return domain.NewError(domain.CodeUnavailable, "%s: %v", operation, err)
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
