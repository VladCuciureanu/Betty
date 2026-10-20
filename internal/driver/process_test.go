package driver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	protocol "github.com/vladcuciureanu/betty/driver"
	"github.com/vladcuciureanu/betty/internal/domain"
)

// The test binary doubles as a fake driver. A copy of it placed next to a
// manifest.json runs as that driver, behaving as BETTY_TEST_DRIVER says for
// its ID, for example "example/slow=hang,example/fast=ok".
const testDriverEnv = "BETTY_TEST_DRIVER"

func TestMain(m *testing.M) {
	if modes := os.Getenv(testDriverEnv); modes != "" {
		runTestDriver(modes)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runTestDriver(modes string) {
	executable, _ := os.Executable()
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(executable), "manifest.json"))
	if err != nil {
		os.Exit(3)
	}
	var manifest protocol.Manifest
	_ = json.Unmarshal(raw, &manifest)
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		os.Stdout.Write(raw)
		return
	}
	mode := ""
	for _, entry := range strings.Split(modes, ",") {
		if id, value, ok := strings.Cut(entry, "="); ok && id == manifest.ID {
			mode = value
		}
	}
	if mode == "sdk" {
		// A conforming driver: the SDK serves every request until EOF.
		_ = protocol.Driver{Manifest: manifest, Inspect: func(*protocol.Session, protocol.InspectRequest) (map[string]protocol.Enrichment, error) {
			return nil, nil
		}}.Run(nil, os.Stdin, os.Stdout)
		return
	}
	in := bufio.NewReader(os.Stdin)
	var request protocol.Request
	if err := json.NewDecoder(in).Decode(&request); err != nil {
		os.Exit(4)
	}
	out := json.NewEncoder(os.Stdout)
	respond := func(id string) {
		enrichments := map[string]protocol.Enrichment{}
		for _, device := range request.Devices {
			enrichments[device.ID] = protocol.Enrichment{Identification: &protocol.Identification{Vendor: manifest.ID, Family: "Test", Confidence: protocol.ConfidenceName}}
		}
		_ = out.Encode(protocol.Response{Protocol: protocol.Version, Type: protocol.TypeResponse, ID: id, Enrichments: enrichments})
	}
	switch mode {
	case "ok":
		respond(request.ID)
		_, _ = io.Copy(io.Discard, in)
	case "hang":
		time.Sleep(time.Hour)
	case "chatty":
		fmt.Fprint(os.Stderr, "first line\nsecond line\r\nno newline")
		respond(request.ID)
	case "linger":
		respond(request.ID)
		time.Sleep(time.Hour)
	case "huge":
		_, _ = os.Stdout.Write([]byte(`{"protocol":1,"type":"response","id":"` + strings.Repeat("a", maxMessageSize) + `"}` + "\n"))
		time.Sleep(time.Hour)
	case "wrong-id":
		respond("not-" + request.ID)
	case "unknown-type":
		_ = out.Encode(protocol.Message{Protocol: protocol.Version, Type: "surprise", ID: request.ID})
	case "silent-exit":
	}
}

// installTestDriver installs a copy of the test binary as a user driver.
func installTestDriver(t *testing.T, root string, manifest protocol.Manifest) {
	t.Helper()
	dir := filepath.Join(root, safeID(manifest.ID))
	writeTestDriver(t, dir, manifest)
}

func writeTestDriver(t *testing.T, dir string, manifest protocol.Manifest) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest.Protocol = protocol.Version
	raw, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, executableName())
	if err := copyExecutable(os.Args[0], path); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTestManager(t *testing.T) *Manager {
	m := NewAt(t.TempDir())
	// Long enough for a cold test binary to start on a slow CI runner; only
	// the "hang" driver should ever reach it.
	m.inspectTimeout = 3 * time.Second
	m.exitGrace = 200 * time.Millisecond
	return m
}

func inspectRequest() protocol.Request {
	return protocol.Request{Protocol: protocol.Version, Type: protocol.TypeRequest, ID: "1", Method: protocol.MethodInspect, Purpose: protocol.PurposeScan, Devices: []protocol.Device{{ID: "device"}}}
}

func TestDriverCallFailures(t *testing.T) {
	cases := []struct {
		mode string
		code domain.ErrorCode
		want string
	}{
		{"hang", domain.CodeTimeout, "did not respond"},
		{"huge", domain.CodeInternal, "exceeds"},
		{"wrong-id", domain.CodeInternal, "answered request"},
		{"unknown-type", domain.CodeInternal, "unexpected message type"},
		{"silent-exit", domain.CodeInternal, "exited without responding"},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			m := newTestManager(t)
			manifest := protocol.Manifest{ID: "example/" + tc.mode, Name: tc.mode, Version: "1"}
			installTestDriver(t, m.root, manifest)
			t.Setenv(testDriverEnv, manifest.ID+"="+tc.mode)
			started := time.Now()
			_, err := m.call(context.Background(), manifest, inspectRequest(), m.inspectBudget())
			if err == nil {
				t.Fatal("expected an error")
			}
			if e := domain.AsError(err); e.Code != tc.code || !strings.Contains(e.Message, tc.want) {
				t.Fatalf("err=%v code=%s", err, e.Code)
			}
			if elapsed := time.Since(started); elapsed > 15*time.Second {
				t.Fatalf("call took %s", elapsed)
			}
		})
	}
}

func TestDriverThatIgnoresEOFIsKilledButItsResponseStands(t *testing.T) {
	m := newTestManager(t)
	manifest := protocol.Manifest{ID: "example/linger", Name: "linger", Version: "1"}
	installTestDriver(t, m.root, manifest)
	t.Setenv(testDriverEnv, manifest.ID+"=linger")
	started := time.Now()
	response, err := m.call(context.Background(), manifest, inspectRequest(), 0)
	if err != nil || response.Enrichments["device"].Identification == nil {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	if elapsed := time.Since(started); elapsed > 15*time.Second {
		t.Fatalf("call took %s", elapsed)
	}
}

func TestSlowDriverDoesNotStarveOtherDrivers(t *testing.T) {
	m := newTestManager(t)
	slow := protocol.Manifest{ID: "example/a-slow", Name: "slow", Version: "1"}
	fast := protocol.Manifest{ID: "example/b-fast", Name: "fast", Version: "1"}
	installTestDriver(t, m.root, slow)
	installTestDriver(t, m.root, fast)
	t.Setenv(testDriverEnv, slow.ID+"=hang,"+fast.ID+"=ok")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	devices := m.Inspect(ctx, []domain.Device{{ID: "device"}})
	if devices[0].Identification == nil || devices[0].Identification.Vendor != fast.ID {
		t.Fatalf("devices=%+v", devices)
	}
}

func TestInstallRefusesToShadowAnAvailableDriver(t *testing.T) {
	m := newTestManager(t)
	m.bundled = t.TempDir()
	manifest := protocol.Manifest{ID: "official/test", Name: "Bundled", Version: "1"}
	writeTestDriver(t, filepath.Join(m.bundled, safeID(manifest.ID)), manifest)
	source := writeTestDriver(t, t.TempDir(), manifest)
	t.Setenv(testDriverEnv, manifest.ID+"=ok")
	if _, err := m.Install(context.Background(), source); err == nil || !strings.Contains(err.Error(), "already installed") {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(m.root, safeID(manifest.ID))); !os.IsNotExist(err) {
		t.Fatalf("shadowing driver was installed: %v", err)
	}
}

func TestReadMessage(t *testing.T) {
	reader := bufio.NewReaderSize(strings.NewReader("\n  \n{\"a\":1}\n{\"b\":2}"), 16)
	first, err := readMessage(reader, 64)
	if err != nil || strings.TrimSpace(string(first)) != `{"a":1}` {
		t.Fatalf("first=%q err=%v", first, err)
	}
	second, err := readMessage(reader, 64)
	if err != nil || string(second) != `{"b":2}` {
		t.Fatalf("second=%q err=%v", second, err)
	}
	if _, err := readMessage(reader, 64); err != io.EOF {
		t.Fatalf("err=%v", err)
	}
	if _, err := readMessage(bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", 100)+"\n"), 16), 64); err == nil {
		t.Fatal("expected size error")
	}
}

func TestDriverStderrReachesTheLogLineByLine(t *testing.T) {
	m := newTestManager(t)
	manifest := protocol.Manifest{ID: "example/chatty", Name: "chatty", Version: "1"}
	installTestDriver(t, m.root, manifest)
	t.Setenv(testDriverEnv, manifest.ID+"=chatty")
	var lines []string
	m.OnDriverLog = func(id, line string) { lines = append(lines, id+": "+line) }
	if _, err := m.call(context.Background(), manifest, inspectRequest(), m.inspectBudget()); err != nil {
		t.Fatal(err)
	}
	want := []string{"example/chatty: first line", "example/chatty: second line", "example/chatty: no newline"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("lines=%q", lines)
	}
}

func TestLineLogStopsAtItsLimit(t *testing.T) {
	var lines []string
	log := newLineLog(func(line string) { lines = append(lines, line) })
	chunk := strings.Repeat("x", 1023) + "\n"
	for range maxStderrBytes/len(chunk) + 4 {
		if n, err := log.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("n=%d err=%v", n, err)
		}
	}
	log.flush()
	if len(lines) != maxStderrBytes/len(chunk)+1 || lines[len(lines)-1] != "(further stderr output dropped)" {
		t.Fatalf("%d lines, last %q", len(lines), lines[len(lines)-1])
	}
}

func TestConcurrentInstallsInstallOnce(t *testing.T) {
	m := newTestManager(t)
	manifest := protocol.Manifest{ID: "example/racy", Name: "Racy", Version: "1"}
	source := writeTestDriver(t, t.TempDir(), manifest)
	t.Setenv(testDriverEnv, manifest.ID+"=ok")
	errs := make(chan error, 4)
	for range cap(errs) {
		go func() {
			_, err := m.Install(context.Background(), source)
			errs <- err
		}()
	}
	installed := 0
	for range cap(errs) {
		if err := <-errs; err == nil {
			installed++
		} else if !strings.Contains(err.Error(), "already installed") {
			t.Fatal(err)
		}
	}
	if installed != 1 {
		t.Fatalf("%d installs succeeded, want 1", installed)
	}
}

func TestUpgradeReplacesTheDriverAndKeepsItsState(t *testing.T) {
	m := newTestManager(t)
	old := protocol.Manifest{ID: "example/upgraded", Name: "Upgraded", Version: "1"}
	t.Setenv(testDriverEnv, old.ID+"=ok")
	if _, err := m.Install(context.Background(), writeTestDriver(t, t.TempDir(), old)); err != nil {
		t.Fatal(err)
	}
	if err := m.storageSet(old, "keys/kept", "secret"); err != nil {
		t.Fatal(err)
	}
	newer := old
	newer.Version = "2"
	manifest, err := m.Upgrade(context.Background(), writeTestDriver(t, t.TempDir(), newer))
	if err != nil || manifest.Version != "2" {
		t.Fatalf("manifest=%+v err=%v", manifest, err)
	}
	if items := mustList(m); len(items) != 1 || items[0].Version != "2" {
		t.Fatalf("installed=%+v", items)
	}
	if value, found, err := m.storageGet(old, "keys/kept"); err != nil || !found || value != "secret" {
		t.Fatalf("state after upgrade: %q %t %v", value, found, err)
	}
}

func TestUpgradeNeedsAUserInstalledDriver(t *testing.T) {
	m := newTestManager(t)
	m.bundled = t.TempDir()
	bundled := protocol.Manifest{ID: "official/bundled", Name: "Bundled", Version: "1"}
	writeTestDriver(t, filepath.Join(m.bundled, safeID(bundled.ID)), bundled)
	missing := protocol.Manifest{ID: "example/missing", Name: "Missing", Version: "1"}
	t.Setenv(testDriverEnv, bundled.ID+"=ok,"+missing.ID+"=ok")
	if _, err := m.Upgrade(context.Background(), writeTestDriver(t, t.TempDir(), bundled)); domain.AsError(err).Code != domain.CodeUnsupported {
		t.Fatalf("upgrading a bundled driver: %v", err)
	}
	if _, err := m.Upgrade(context.Background(), writeTestDriver(t, t.TempDir(), missing)); domain.AsError(err).Code != domain.CodeNotFound {
		t.Fatalf("upgrading a driver that isn't installed: %v", err)
	}
}
