package driver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	protocol "github.com/vladcuciureanu/betty/driver"
	"github.com/vladcuciureanu/betty/internal/domain"
)

// Limits on a driver process. Drivers are trusted code, but a buggy one must
// not be able to hang Betty, exhaust its memory, or outlive the command.
const (
	// maxMessageSize bounds one protocol message read from a driver.
	maxMessageSize = 4 << 20
	// maxManifestSize bounds the output of "driver --manifest".
	maxManifestSize = 64 << 10
	// defaultInspectTimeout bounds each driver's inspect, so that one slow
	// driver cannot consume the whole command's budget.
	defaultInspectTimeout = 5 * time.Second
	// defaultManifestTimeout bounds "driver --manifest".
	defaultManifestTimeout = 5 * time.Second
	// defaultExitGrace is how long a driver may take to exit after its
	// response before Betty kills it.
	defaultExitGrace = 2 * time.Second
	// channelCloseTimeout bounds closing a channel a driver left open.
	channelCloseTimeout = 2 * time.Second
)

func (m *Manager) inspectBudget() time.Duration {
	if m.inspectTimeout > 0 {
		return m.inspectTimeout
	}
	return defaultInspectTimeout
}

func (m *Manager) exitBudget() time.Duration {
	if m.exitGrace > 0 {
		return m.exitGrace
	}
	return defaultExitGrace
}

// call runs one request against a driver process. A positive timeout bounds
// this driver alone; the caller's context still bounds the whole command.
func (m *Manager) call(ctx context.Context, item protocol.Manifest, request protocol.Request, timeout time.Duration) (protocol.Response, error) {
	callCtx, cancel := ctx, context.CancelFunc(func() {})
	if timeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	response, err := m.exchange(callCtx, item, request)
	if err == nil {
		return response, nil
	}
	if ctx.Err() != nil {
		// The command itself was cancelled or ran out of time.
		return response, ctx.Err()
	}
	if callCtx.Err() != nil {
		return response, domain.NewError(domain.CodeTimeout, "driver %s did not respond within %s", item.ID, timeout)
	}
	return response, err
}

func (m *Manager) exchange(ctx context.Context, item protocol.Manifest, request protocol.Request) (protocol.Response, error) {
	var response protocol.Response
	cmd := exec.CommandContext(ctx, m.driverPath(item.ID))
	// Stderr is left nil (the null device) rather than io.Discard, so no copy
	// goroutine can keep Wait blocked on a pipe inherited by a grandchild.
	cmd.WaitDelay = m.exitBudget()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return response, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return response, err
	}
	if err := cmd.Start(); err != nil {
		return response, domain.NewError(domain.CodeInternal, "start driver %s: %v", item.ID, err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	channels := map[string]string{}
	defer m.closeChannels(channels)
	allowedDevices := map[string]bool{}
	for _, device := range request.Devices {
		allowedDevices[device.ID] = true
	}
	encoder := json.NewEncoder(stdin)
	reader := bufio.NewReader(stdout)
	if err := encoder.Encode(request); err != nil {
		return response, driverFailure(item, "write request: %v", err)
	}
	for {
		raw, err := readMessage(reader, maxMessageSize)
		if errors.Is(err, io.EOF) {
			return response, driverFailure(item, "exited without responding")
		}
		if err != nil {
			return response, driverFailure(item, "%v", err)
		}
		var header protocol.Message
		if err := json.Unmarshal(raw, &header); err != nil {
			return response, driverFailure(item, "invalid message: %v", err)
		}
		if header.Type == protocol.TypeHostRequest {
			if err := encoder.Encode(m.hostReply(ctx, item, header, allowedDevices, channels)); err != nil {
				return response, driverFailure(item, "write host response: %v", err)
			}
			continue
		}
		if header.Type != protocol.TypeResponse {
			return response, driverFailure(item, "sent unexpected message type %q", header.Type)
		}
		if header.ID != request.ID {
			return response, driverFailure(item, "answered request %q instead of %q", header.ID, request.ID)
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			return response, driverFailure(item, "invalid response: %v", err)
		}
		break
	}

	// The response is complete. Ask the driver to exit by closing its input,
	// and kill it if it doesn't exit promptly; its answer still stands.
	_ = stdin.Close()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		waited = true
		if err != nil && ctx.Err() == nil {
			return response, driverFailure(item, "exited with an error after responding: %v", err)
		}
		if err != nil {
			return response, ctx.Err()
		}
	case <-time.After(m.exitBudget()):
		_ = cmd.Process.Kill()
		<-exited
		waited = true
	}

	if response.Protocol != protocol.Version {
		return response, driverFailure(item, "returned protocol %d", response.Protocol)
	}
	if response.Error != "" {
		code := domain.CodeInternal
		if response.ErrorCode != "" {
			code = driverErrorCode(response.ErrorCode)
		}
		err := domain.NewError(code, "driver %s: %s", item.ID, response.Error)
		err.Details = response.Details
		return response, err
	}
	return response, nil
}

func (m *Manager) hostReply(ctx context.Context, item protocol.Manifest, request protocol.Message, allowedDevices map[string]bool, channels map[string]string) protocol.Message {
	result, err := m.handleHost(ctx, item, request, allowedDevices, channels)
	reply := protocol.Message{Protocol: protocol.Version, Type: protocol.TypeHostResponse, ID: request.ID, Result: result}
	if err != nil {
		hostErr := domain.AsError(err)
		reply.Error = hostErr.Message
		reply.ErrorCode = string(hostErr.Code)
		reply.Details = hostErr.Details
	}
	return reply
}

// closeChannels closes every channel a driver left open. It uses its own
// deadline because the request's context may already be done.
func (m *Manager) closeChannels(channels map[string]string) {
	for channel, transport := range channels {
		ctx, cancel := context.WithTimeout(context.Background(), channelCloseTimeout)
		switch transport {
		case protocol.PermissionL2CAP:
			_ = m.host.CloseL2CAP(ctx, channel)
		case protocol.PermissionRFCOMM:
			_ = m.rfcomm.CloseRFCOMM(ctx, channel)
		}
		cancel()
	}
}

func driverFailure(item protocol.Manifest, format string, args ...any) error {
	return domain.NewError(domain.CodeInternal, "driver %s: %s", item.ID, fmt.Sprintf(format, args...))
}

// readMessage reads one newline-terminated protocol message of at most limit
// bytes, skipping blank lines. It returns io.EOF when input ends cleanly.
func readMessage(r *bufio.Reader, limit int) ([]byte, error) {
	var message []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(message)+len(chunk) > limit {
			return nil, fmt.Errorf("message exceeds %d bytes", limit)
		}
		message = append(message, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if len(bytes.TrimSpace(message)) > 0 {
			return message, nil
		}
		if err != nil {
			return nil, io.EOF
		}
		message = message[:0]
	}
}

// readManifest runs "driver --manifest" with a deadline and an output limit.
func readManifest(ctx context.Context, path string) (protocol.Manifest, error) {
	var manifest protocol.Manifest
	ctx, cancel := context.WithTimeout(ctx, defaultManifestTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--manifest")
	cmd.WaitDelay = time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return manifest, err
	}
	if err := cmd.Start(); err != nil {
		return manifest, fmt.Errorf("read driver manifest: %w", err)
	}
	output, readErr := io.ReadAll(io.LimitReader(stdout, maxManifestSize+1))
	if len(output) > maxManifestSize {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return manifest, fmt.Errorf("driver manifest exceeds %d bytes", maxManifestSize)
	}
	if err := cmd.Wait(); err != nil {
		return manifest, fmt.Errorf("read driver manifest: %w", err)
	}
	if readErr != nil {
		return manifest, fmt.Errorf("read driver manifest: %w", readErr)
	}
	if err := json.Unmarshal(output, &manifest); err != nil {
		return manifest, fmt.Errorf("invalid driver manifest: %w", err)
	}
	return manifest, nil
}
