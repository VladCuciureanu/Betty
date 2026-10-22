package app

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/vladcuciureanu/betty/internal/backend"
	"github.com/vladcuciureanu/betty/internal/domain"
)

// serveSession drives bt serve over pipes, as an app would.
type serveSession struct {
	t     *testing.T
	in    *io.PipeWriter
	lines chan serveReply
	done  chan int
}

type serveReply struct {
	SchemaVersion int             `json:"schema_version"`
	Type          string          `json:"type"`
	ID            string          `json:"id"`
	Data          json.RawMessage `json:"data"`
	Error         *domain.Error   `json:"error"`
	Warnings      []string        `json:"warnings"`
}

func startServe(t *testing.T, h *harness) *serveSession {
	t.Helper()
	return startServeOn(t, h, h.backend)
}

// startServeOn starts a session on a backend other than the harness's.
func startServeOn(t *testing.T, h *harness, platform backend.Backend) *serveSession {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &serveSession{t: t, in: inW, lines: make(chan serveReply, 64), done: make(chan int, 1)}
	go func() {
		code := run([]string{"serve"}, outW, &h.stderr, "test", func(a *application) {
			a.backend, a.ble, a.hid, a.stdin = platform, h.ble, h.hid, inR
		})
		outW.Close()
		s.done <- code
	}()
	go func() {
		scanner := bufio.NewScanner(outR)
		for scanner.Scan() {
			var reply serveReply
			if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil {
				t.Errorf("stdout line is not JSON: %v\n%s", err, scanner.Text())
				continue
			}
			s.lines <- reply
		}
		close(s.lines)
	}()
	t.Cleanup(func() { inW.Close() })
	return s
}

func (s *serveSession) send(line string) {
	s.t.Helper()
	if _, err := io.WriteString(s.in, line+"\n"); err != nil {
		s.t.Fatal(err)
	}
}

func (s *serveSession) next() serveReply {
	s.t.Helper()
	select {
	case reply, ok := <-s.lines:
		if !ok {
			s.t.Fatal("the session ended")
		}
		if reply.SchemaVersion != 1 {
			s.t.Fatalf("reply without schema_version 1: %+v", reply)
		}
		return reply
	case <-time.After(5 * time.Second):
		s.t.Fatal("no reply within 5s")
	}
	return serveReply{}
}

// end closes stdin and returns the exit status.
func (s *serveSession) end() int {
	s.t.Helper()
	s.in.Close()
	select {
	case code := <-s.done:
		return code
	case <-time.After(5 * time.Second):
		s.t.Fatal("the session did not end after stdin closed")
	}
	return -1
}

func TestServeAnswersRequestsUntilStdinCloses(t *testing.T) {
	h := newHarness()
	h.backend.known = []domain.Device{bleDevice("AA:BB:CC:DD:EE:01", "Sensor")}
	h.ble.battery = []byte{64}
	s := startServe(t, h)
	s.send(`{"schema_version":1,"type":"request","id":"1","method":"get","params":{"device":"Sensor","property":"battery"}}`)
	reply := s.next()
	if reply.Type != "response" || reply.ID != "1" || reply.Error != nil || !strings.Contains(string(reply.Data), `"level":64`) {
		t.Fatalf("reply=%+v data=%s", reply, reply.Data)
	}
	if code := s.end(); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

func TestServeErrorsDoNotEndTheSession(t *testing.T) {
	h := newHarness()
	h.backend.known = []domain.Device{bleDevice("AA:BB:CC:DD:EE:01", "Sensor")}
	s := startServe(t, h)
	s.send(`{"schema_version":1,"type":"request","id":"a","method":"inspect","params":{"device":"Nope"}}`)
	if reply := s.next(); reply.ID != "a" || reply.Error == nil || reply.Error.Code != domain.CodeNotFound {
		t.Fatalf("reply=%+v", reply)
	}
	s.send(`{"schema_version":1,"type":"request","id":"b","method":"teleport"}`)
	if reply := s.next(); reply.ID != "b" || reply.Error == nil || reply.Error.Code != domain.CodeUsage {
		t.Fatalf("reply=%+v", reply)
	}
	s.send(`{"schema_version":1,"type":"request","id":"c","method":"list"}`)
	if reply := s.next(); reply.ID != "c" || reply.Error != nil || !strings.Contains(string(reply.Data), "Sensor") {
		t.Fatalf("reply=%+v", reply)
	}
	s.end()
}

func TestServeRunsRequestsConcurrentlyAndCancels(t *testing.T) {
	h := newHarness()
	speaker := classicDevice("os:1", "Speaker", "AA:BB:CC:DD:EE:02")
	speaker.Paired = false
	backend := &blockingBackend{fakeBackend{known: []domain.Device{speaker}}}
	s := startServeOn(t, h, backend)
	s.send(`{"schema_version":1,"type":"request","id":"slow","method":"pair","params":{"device":"Speaker","timeout_ms":20000}}`)
	s.send(`{"schema_version":1,"type":"request","id":"slow","method":"list"}`)
	if reply := s.next(); reply.ID != "slow" || reply.Error == nil || !strings.Contains(reply.Error.Message, "still in use") {
		t.Fatalf("a duplicate ID must be refused: %+v", reply)
	}
	s.send(`{"schema_version":1,"type":"request","id":"quick","method":"list"}`)
	if reply := s.next(); reply.ID != "quick" || reply.Error != nil {
		t.Fatalf("the list waited for the pairing: %+v", reply)
	}
	s.send(`{"schema_version":1,"type":"request","id":"stop","method":"cancel","params":{"request":"slow"}}`)
	replies := map[string]serveReply{}
	for range 2 {
		reply := s.next()
		replies[reply.ID] = reply
	}
	if replies["stop"].Error != nil || replies["slow"].Error == nil || replies["slow"].Error.Code != domain.CodeCanceled {
		t.Fatalf("replies=%+v", replies)
	}
	s.end()
}

func TestServeEndsOnAMalformedLine(t *testing.T) {
	h := newHarness()
	s := startServe(t, h)
	s.send(`not json`)
	if reply := s.next(); reply.ID != "" || reply.Error == nil || reply.Error.Code != domain.CodeUsage {
		t.Fatalf("reply=%+v", reply)
	}
	select {
	case code := <-s.done:
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the session kept running after a malformed line")
	}
}

func TestServePutsWarningsInTheResponse(t *testing.T) {
	h := newHarness()
	h.backend.scanned = []domain.Device{classicDevice("os:1", "Speaker", "AA:BB:CC:DD:EE:02")}
	h.ble.scanErr = errors.New("radio busy")
	s := startServe(t, h)
	s.send(`{"schema_version":1,"type":"request","id":"1","method":"scan","params":{"timeout_ms":2000}}`)
	reply := s.next()
	if reply.Error != nil || len(reply.Warnings) != 1 || !strings.Contains(reply.Warnings[0], "BLE scan failed") {
		t.Fatalf("reply=%+v", reply)
	}
	s.end()
}

func TestServeSubscribesToAProperty(t *testing.T) {
	h := newHarness()
	h.backend.known = []domain.Device{bleDevice("AA:BB:CC:DD:EE:01", "Sensor")}
	h.ble.battery = []byte{64}
	s := startServe(t, h)
	s.send(`{"schema_version":1,"type":"request","id":"w","method":"subscribe","params":{"device":"Sensor","property":"battery","interval_ms":20}}`)
	event := s.next()
	if event.Type != "event" || event.ID != "w" || !strings.Contains(string(event.Data), `"event":"snapshot"`) {
		t.Fatalf("event=%+v data=%s", event, event.Data)
	}
	s.send(`{"schema_version":1,"type":"request","id":"c","method":"cancel","params":{"request":"w"}}`)
	replies := map[string]serveReply{}
	for len(replies) < 2 {
		reply := s.next()
		if reply.Type == "response" {
			replies[reply.ID] = reply
		}
	}
	if replies["w"].Error != nil || replies["c"].Error != nil {
		t.Fatalf("a cancelled subscription must end successfully: %+v", replies)
	}
	s.end()
}

func TestServeSubscribesToDevices(t *testing.T) {
	h := newHarness()
	h.backend.known = []domain.Device{classicDevice("os:1", "Speaker", "AA:BB:CC:DD:EE:02")}
	s := startServe(t, h)
	s.send(`{"schema_version":1,"type":"request","id":"d","method":"subscribe","params":{"devices":true,"interval_ms":20}}`)
	if event := s.next(); event.Type != "event" || !strings.Contains(string(event.Data), `"event":"snapshot"`) || !strings.Contains(string(event.Data), "Speaker") {
		t.Fatalf("event=%+v data=%s", event, event.Data)
	}
	s.end()
}

func TestDeviceChangesNameEachChangeInIDOrder(t *testing.T) {
	speaker := classicDevice("os:1", "Speaker", "AA:BB:CC:DD:EE:02")
	connected := speaker
	connected.Connected, connected.Name = true, "Kitchen"
	keyboard := classicDevice("os:2", "Keyboard", "AA:BB:CC:DD:EE:03")
	mouse := classicDevice("os:3", "Mouse", "AA:BB:CC:DD:EE:04")
	events := deviceChanges(
		map[string]domain.Device{speaker.ID: speaker, keyboard.ID: keyboard},
		map[string]domain.Device{connected.ID: connected, mouse.ID: mouse},
	)
	var got []string
	for _, e := range events {
		got = append(got, e.Event+" "+e.Device.ID)
	}
	want := []string{"connected os:1", "renamed os:1", "removed os:2", "added os:3"}
	if strings.Join(got, ", ") != strings.Join(want, ", ") {
		t.Fatalf("events=%v", got)
	}
}
