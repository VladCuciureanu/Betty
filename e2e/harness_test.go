//go:build e2e

package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vladcuciureanu/betty/internal/domain"
)

// Environment:
//
//	BETTY_E2E_TRANSPORT     "virtual" (Linux only), or a Bumble transport for a
//	                        dedicated controller, such as usb:0. Required.
//	BETTY_E2E_PYTHON        Python with e2e/peer installed. Default: python3,
//	                        or python on Windows.
//	BETTY_E2E_HOST_ADDRESS  Over the air only: this computer's Bluetooth
//	                        address, for tests where the device connects to it.
const commandTimeout = "30s"

var (
	binDir    string
	btPath    string
	transport string
	python    string
	runs      atomic.Int32
)

func TestMain(m *testing.M) {
	transport = os.Getenv("BETTY_E2E_TRANSPORT")
	if transport == "" {
		fmt.Fprintln(os.Stderr, "e2e: set BETTY_E2E_TRANSPORT; see e2e/README.md")
		os.Exit(2)
	}
	python = os.Getenv("BETTY_E2E_PYTHON")
	if python == "" {
		python = "python3"
		if runtime.GOOS == "windows" {
			python = "python"
		}
	}
	dir, err := os.MkdirTemp("", "betty-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binDir = dir
	btPath = filepath.Join(binDir, executable("bt"))
	for _, target := range []string{"bt", "bt-driver-sony"} {
		build := exec.Command("go", "build", "-o", filepath.Join(binDir, executable(target)), "./cmd/"+target)
		build.Dir = ".."
		if output, err := build.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "e2e: build %s: %v\n%s", target, err, output)
			os.Exit(1)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(binDir)
	os.Exit(code)
}

func executable(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func virtual() bool { return transport == "virtual" }

func requireVirtual(t *testing.T) {
	t.Helper()
	if !virtual() {
		t.Skip("needs several scripted devices, which only --transport virtual provides")
	}
}

// peer is a running betty_peer process.
type peer struct {
	t       *testing.T
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	events  chan peerEvent
	host    string
	devices []peerDevice
}

type peerDevice struct {
	Index   int    `json:"index"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Address string `json:"address"`
}

type peerEvent struct {
	Event   string       `json:"event"`
	Device  int          `json:"device"`
	Cmd     string       `json:"cmd"`
	Message string       `json:"message"`
	Value   string       `json:"value"`
	Host    string       `json:"host"`
	Devices []peerDevice `json:"devices"`
}

// startPeer runs scripted devices, each given as "kind:name". Names get a
// per-run suffix so a stack's cached names from earlier tests never match.
func startPeer(t *testing.T, specs ...string) *peer {
	t.Helper()
	run := runs.Add(1)
	args := []string{"-m", "betty_peer", "--transport", transport, "--seed", fmt.Sprint(run)}
	if address := os.Getenv("BETTY_E2E_HOST_ADDRESS"); address != "" && !virtual() {
		args = append(args, "--host-address", address)
	}
	for _, spec := range specs {
		args = append(args, "--device", fmt.Sprintf("%s %d", spec, run))
	}
	cmd := exec.Command(python, args...)
	cmd.Stderr = &prefixWriter{t: t, prefix: "peer: "}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the peer (is e2e/peer installed for %s?): %v", python, err)
	}
	p := &peer{t: t, cmd: cmd, stdin: stdin, events: make(chan peerEvent, 64)}
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			var event peerEvent
			if json.Unmarshal(scanner.Bytes(), &event) == nil {
				p.events <- event
			}
		}
		close(p.events)
	}()
	t.Cleanup(p.stop)
	ready := p.wait("ready", 30*time.Second)
	p.host, p.devices = ready.Host, ready.Devices
	if virtual() {
		waitForAdapter(t)
	}
	return p
}

func (p *peer) device(i int) peerDevice { return p.devices[i] }

func (p *peer) stop() {
	_, _ = io.WriteString(p.stdin, `{"cmd":"quit"}`+"\n")
	_ = p.stdin.Close()
	done := make(chan struct{})
	go func() { _ = p.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = p.cmd.Process.Kill()
		<-done
	}
}

// do sends a command and waits for the peer to finish it.
func (p *peer) do(command map[string]any) {
	p.t.Helper()
	line, _ := json.Marshal(command)
	if _, err := p.stdin.Write(append(line, '\n')); err != nil {
		p.t.Fatalf("peer command %s: %v", line, err)
	}
	for {
		event := p.next(30 * time.Second)
		switch event.Event {
		case "done":
			return
		case "error":
			p.t.Fatalf("peer command %s failed: %s", line, event.Message)
		}
	}
}

// wait returns the next event with the given name, skipping others.
func (p *peer) wait(name string, timeout time.Duration) peerEvent {
	p.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		event := p.next(time.Until(deadline))
		if event.Event == name {
			return event
		}
	}
}

func (p *peer) next(timeout time.Duration) peerEvent {
	p.t.Helper()
	select {
	case event, ok := <-p.events:
		if !ok {
			p.t.Fatal("the peer exited")
		}
		return event
	case <-time.After(timeout):
		p.t.Fatal("timed out waiting for the peer")
		return peerEvent{}
	}
}

// waitForAdapter waits until BlueZ has adopted the virtual controller that
// the peer just created, which takes a moment after the peer starts.
func waitForAdapter(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		output, err := exec.Command("bluetoothctl", "show").Output()
		if err == nil && bytes.Contains(output, []byte("Powered: yes")) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("BlueZ did not power the virtual controller: %v\n%s", err, output)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// result is one bt invocation.
type result struct {
	args   []string
	code   int
	stdout []byte
	stderr []byte
}

func bt(t *testing.T, env []string, args ...string) result {
	t.Helper()
	full := append([]string{"--json", "--non-interactive", "--timeout", commandTimeout}, args...)
	cmd := exec.Command(btPath, full...)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	r := result{args: args, stdout: stdout.Bytes(), stderr: stderr.Bytes()}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		r.code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("bt %v: %v", args, err)
	}
	return r
}

// ok runs bt, requires success, and decodes the result's data into v.
func ok(t *testing.T, v any, args ...string) {
	t.Helper()
	r := bt(t, nil, args...)
	if r.code != 0 {
		t.Fatalf("bt %v: exit %d\n%s", args, r.code, r.stderr)
	}
	decodeData(t, r, v)
}

func decodeData(t *testing.T, r result, v any) {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(r.stdout, &envelope); err != nil {
		t.Fatalf("bt %v: stdout is not one envelope: %v\n%s", r.args, err, r.stdout)
	}
	if v != nil {
		if err := json.Unmarshal(envelope.Data, v); err != nil {
			t.Fatalf("bt %v: %v\n%s", r.args, err, envelope.Data)
		}
	}
}

// fails runs bt and requires it to fail with code.
func fails(t *testing.T, code domain.ErrorCode, args ...string) {
	t.Helper()
	r := bt(t, nil, args...)
	var envelope struct {
		Error domain.Error `json:"error"`
	}
	_ = json.Unmarshal(lastLine(r.stderr), &envelope)
	if r.code == 0 || envelope.Error.Code != code {
		t.Fatalf("bt %v: exit %d, want %s\nstdout: %s\nstderr: %s", r.args, r.code, code, r.stdout, r.stderr)
	}
}

func lastLine(b []byte) []byte {
	lines := bytes.Split(bytes.TrimSpace(b), []byte("\n"))
	return lines[len(lines)-1]
}

// stream runs a long-lived bt command, such as watch, and returns its
// output lines. Cancel ctx to stop it.
func stream(t *testing.T, ctx context.Context, args ...string) (<-chan []byte, func() result) {
	t.Helper()
	full := append([]string{"--json"}, args...)
	cmd := exec.CommandContext(ctx, btPath, full...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan []byte, 64)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- append([]byte(nil), scanner.Bytes()...)
		}
		close(lines)
	}()
	wait := func() result {
		err := cmd.Wait()
		r := result{args: args, stderr: stderr.Bytes()}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			r.code = exit.ExitCode()
		}
		return r
	}
	return lines, wait
}

// nextLine returns the next line of a stream that contains want.
func nextLine(t *testing.T, lines <-chan []byte, want string, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("the stream ended before printing %q", want)
			}
			if strings.Contains(string(line), want) {
				return line
			}
		case <-deadline:
			t.Fatalf("timed out waiting for a line with %q", want)
		}
	}
}

type prefixWriter struct {
	t      *testing.T
	prefix string
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line != "" && !strings.Contains(line, "Unsupported command") && !strings.Contains(line, "secure_connections_host_support") {
			w.t.Log(w.prefix + line)
		}
	}
	return len(p), nil
}
