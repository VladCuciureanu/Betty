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

// driverProcess is a running driver. Without reuse it serves one request;
// with reuse it serves every request of the command until Close.
type driverProcess struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	encoder *json.Encoder
	reader  *bufio.Reader
	log     *lineLog
	exited  chan error // receives Wait's result once
	served  int
}

func (m *Manager) start(item protocol.Manifest) (*driverProcess, error) {
	// Not CommandContext: a reused process outlives each request's context.
	// exchange kills it when a request's context ends.
	cmd := exec.Command(m.driverPath(item.ID))
	// Without a log, stderr is left nil (the null device) rather than
	// io.Discard, so no copy goroutine runs at all. With one, WaitDelay stops
	// a pipe inherited by a grandchild from keeping Wait blocked.
	cmd.WaitDelay = m.exitBudget()
	p := &driverProcess{cmd: cmd, exited: make(chan error, 1)}
	if m.OnDriverLog != nil {
		p.log = newLineLog(func(line string) { m.OnDriverLog(item.ID, line) })
		cmd.Stderr = p.log
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, domain.NewError(domain.CodeInternal, "start driver %s: %v", item.ID, err)
	}
	go func() { p.exited <- cmd.Wait() }()
	p.stdin, p.encoder, p.reader = stdin, json.NewEncoder(stdin), bufio.NewReader(stdout)
	return p, nil
}

// stop asks the driver to exit by closing its input, kills it if it doesn't
// exit within grace, and returns its exit error.
func (p *driverProcess) stop(grace time.Duration) error {
	_ = p.stdin.Close()
	var err error
	select {
	case err = <-p.exited:
	case <-time.After(grace):
		_ = p.cmd.Process.Kill()
		<-p.exited
	}
	if p.log != nil {
		p.log.flush()
	}
	return err
}

func (p *driverProcess) kill() {
	_ = p.cmd.Process.Kill()
	_ = p.stop(0)
}

// SetReuse makes the manager keep each driver's process for later requests
// until Close, instead of starting one per request. Every official driver
// serves requests until its input ends, as the protocol requires.
func (m *Manager) SetReuse(reuse bool) { m.reuse = reuse }

// Close stops every driver process kept for reuse.
func (m *Manager) Close() {
	m.procsMu.Lock()
	procs := m.procs
	m.procs = nil
	m.procsMu.Unlock()
	for _, p := range procs {
		_ = p.stop(m.exitBudget())
	}
}

func (m *Manager) exchange(ctx context.Context, item protocol.Manifest, request protocol.Request) (protocol.Response, error) {
	if !m.reuse {
		p, err := m.start(item)
		if err != nil {
			return protocol.Response{}, err
		}
		response, err := m.serve(ctx, p, item, request)
		if err != nil {
			p.kill()
			return response, err
		}
		// The response is complete. Ask the driver to exit, and kill it if it
		// doesn't exit promptly; its answer still stands.
		if exitErr := p.stop(m.exitBudget()); exitErr != nil && ctx.Err() == nil && !isKilled(exitErr) {
			return response, driverFailure(item, "exited with an error after responding: %v", exitErr)
		}
		return checkResponse(item, response)
	}
	for attempt := 0; ; attempt++ {
		m.procsMu.Lock()
		p := m.procs[item.ID]
		m.procsMu.Unlock()
		fresh := p == nil
		if fresh {
			var err error
			if p, err = m.start(item); err != nil {
				return protocol.Response{}, err
			}
		}
		response, err := m.serve(ctx, p, item, request)
		if err != nil {
			p.kill()
			m.forget(item.ID, p)
			// A reused driver that has exited since its last request gets one
			// fresh start.
			if !fresh && attempt == 0 && ctx.Err() == nil && errors.Is(err, errDriverGone) {
				continue
			}
			return response, err
		}
		m.procsMu.Lock()
		if m.procs == nil {
			m.procs = map[string]*driverProcess{}
		}
		m.procs[item.ID] = p
		m.procsMu.Unlock()
		return checkResponse(item, response)
	}
}

func (m *Manager) forget(id string, p *driverProcess) {
	m.procsMu.Lock()
	defer m.procsMu.Unlock()
	if m.procs[id] == p {
		delete(m.procs, id)
	}
}

// errDriverGone marks a driver that exited, or closed its output, before it
// answered a request.
var errDriverGone = errors.New("exited without responding")

// gone reports a driver that is no longer serving requests.
func gone(item protocol.Manifest, format string, args ...any) error {
	err := domain.NewError(domain.CodeInternal, "driver %s: %s", item.ID, fmt.Sprintf(format, args...))
	err.Cause = errDriverGone
	return err
}

// serve sends one request to a running driver and returns its response,
// answering host calls on the way. It kills the process if ctx ends first.
func (m *Manager) serve(ctx context.Context, p *driverProcess, item protocol.Manifest, request protocol.Request) (protocol.Response, error) {
	var response protocol.Response
	stop := context.AfterFunc(ctx, func() { _ = p.cmd.Process.Kill() })
	defer stop()
	session := newHostSession(request.Devices)
	defer m.closeSession(session)
	if err := p.encoder.Encode(request); err != nil {
		return response, gone(item, "write request: %v", err)
	}
	p.served++
	for {
		raw, err := readMessage(p.reader, maxMessageSize)
		if errors.Is(err, io.EOF) {
			return response, gone(item, "exited without responding")
		}
		if err != nil {
			return response, driverFailure(item, "%v", err)
		}
		var header protocol.Message
		if err := json.Unmarshal(raw, &header); err != nil {
			return response, driverFailure(item, "invalid message: %v", err)
		}
		if header.Type == protocol.TypeHostRequest {
			if err := p.encoder.Encode(m.hostReply(ctx, item, header, session)); err != nil {
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
		return response, nil
	}
}

// isKilled reports whether a driver's exit error is Betty's own kill after
// the exit grace period.
func isKilled(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && !exit.Exited()
}

func checkResponse(item protocol.Manifest, response protocol.Response) (protocol.Response, error) {
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

func (m *Manager) hostReply(ctx context.Context, item protocol.Manifest, request protocol.Message, session *hostSession) protocol.Message {
	result, err := m.handleHost(ctx, item, request, session)
	reply := protocol.Message{Protocol: protocol.Version, Type: protocol.TypeHostResponse, ID: request.ID, Result: result}
	if err != nil {
		hostErr := domain.AsError(err)
		reply.Error = hostErr.Message
		reply.ErrorCode = string(hostErr.Code)
		reply.Details = hostErr.Details
	}
	return reply
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
