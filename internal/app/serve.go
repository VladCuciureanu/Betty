package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vladcuciureanu/betty/internal/domain"
	"github.com/vladcuciureanu/betty/internal/output"
)

// bt serve answers newline-delimited JSON requests on stdin until stdin
// closes; docs/design/bt-serve.md describes the protocol. Each request runs
// the matching command on its own copy of the application, whose renderer
// captures the result and warnings into the response.

const serveHelp = `Requests are JSON lines on stdin; responses and events are JSON lines on
stdout. Every message has "schema_version": 1 and a "type":

  {"schema_version":1,"type":"request","id":"1","method":"get","params":{"device":"headphones","property":"battery"}}
  {"schema_version":1,"type":"response","id":"1","data":{...}}

Methods: list, scan, inspect, get, set, pair, unpair, connect, disconnect,
capabilities, adapter, adapter.set, gatt.services, gatt.characteristics,
gatt.read, gatt.write, gatt.mtu, gatt.descriptors, gatt.read_descriptor,
gatt.write_descriptor, subscribe and cancel. Requests run concurrently;
operations on one device run one at a time. subscribe sends events with the
request's ID until cancel or the end of the session. Pairing never prompts.
The session ends when stdin closes. See docs/design/bt-serve.md.`

// maxServeLine bounds one request line.
const maxServeLine = 1 << 20

// lookupCommand is findCommand, set at init: the command table refers to
// serve, so serve cannot refer to the table directly.
var lookupCommand func(string) *command

func init() { lookupCommand = findCommand }

type serveRequest struct {
	SchemaVersion int             `json:"schema_version"`
	Type          string          `json:"type"`
	ID            string          `json:"id"`
	Method        string          `json:"method"`
	Params        json.RawMessage `json:"params"`
}

type serveParams struct {
	Device          string          `json:"device"`
	Property        string          `json:"property"`
	Value           json.RawMessage `json:"value"`
	All             bool            `json:"all"`
	Service         string          `json:"service"`
	Characteristic  string          `json:"characteristic"`
	Descriptor      string          `json:"descriptor"`
	Data            string          `json:"data"`
	WithoutResponse bool            `json:"without_response"`
	Setting         string          `json:"setting"`
	Devices         bool            `json:"devices"`
	Request         string          `json:"request"`
	TimeoutMS       int             `json:"timeout_ms"`
	IntervalMS      int             `json:"interval_ms"`
}

type serveMessage struct {
	SchemaVersion int           `json:"schema_version"`
	Type          string        `json:"type"`
	ID            string        `json:"id"`
	Data          any           `json:"data,omitempty"`
	Error         *domain.Error `json:"error,omitempty"`
	Warnings      []string      `json:"warnings,omitempty"`
}

type session struct {
	a   *application
	ctx context.Context

	writeMu sync.Mutex
	encoder *json.Encoder

	mu       sync.Mutex
	requests map[string]context.CancelFunc
	queues   map[string]chan struct{}
	running  sync.WaitGroup
}

func (a *application) serve(ctx context.Context, _ []string) error {
	if a.stdin == nil {
		return domain.NewError(domain.CodeUsage, "bt serve needs requests on stdin")
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &session{a: a, ctx: ctx, encoder: json.NewEncoder(a.render.Out), requests: map[string]context.CancelFunc{}, queues: map[string]chan struct{}{}}
	defer func() {
		cancel()
		s.running.Wait()
	}()
	lines := bufio.NewReaderSize(a.stdin, 64<<10)
	for {
		line, err := readServeLine(lines)
		if len(bytes.TrimSpace(line)) > 0 {
			var request serveRequest
			if decodeErr := json.Unmarshal(line, &request); decodeErr != nil || request.SchemaVersion != 1 || request.Type != "request" || request.ID == "" || request.Method == "" {
				s.write(serveMessage{Type: "response", Error: domain.AsError(domain.NewError(domain.CodeUsage, "invalid request line; each line must be a JSON request with schema_version 1, type, id and method"))})
				return nil
			}
			s.start(request)
		}
		if errors.Is(err, io.EOF) || ctx.Err() != nil {
			return nil
		}
		if err != nil {
			s.write(serveMessage{Type: "response", Error: domain.AsError(domain.NewError(domain.CodeUsage, "%v", err))})
			return nil
		}
	}
}

func readServeLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > maxServeLine {
			return nil, errors.New("a request line is longer than 1 MiB")
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, err
		}
	}
}

func (s *session) write(message serveMessage) {
	message.SchemaVersion = output.SchemaVersion
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.encoder.Encode(message)
}

func (s *session) start(request serveRequest) {
	var params serveParams
	if len(request.Params) > 0 {
		if err := json.Unmarshal(request.Params, &params); err != nil {
			s.write(serveMessage{Type: "response", ID: request.ID, Error: domain.AsError(domain.NewError(domain.CodeUsage, "invalid params: %v", err))})
			return
		}
	}
	if request.Method == "cancel" {
		s.mu.Lock()
		stop, ok := s.requests[params.Request]
		s.mu.Unlock()
		if !ok {
			s.write(serveMessage{Type: "response", ID: request.ID, Error: domain.AsError(domain.NewError(domain.CodeNotFound, "no running request %q", params.Request))})
			return
		}
		stop()
		s.write(serveMessage{Type: "response", ID: request.ID, Data: map[string]bool{"cancelled": true}})
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.mu.Lock()
	if _, busy := s.requests[request.ID]; busy {
		s.mu.Unlock()
		cancel()
		s.write(serveMessage{Type: "response", ID: request.ID, Error: domain.AsError(domain.NewError(domain.CodeUsage, "request ID %q is still in use", request.ID))})
		return
	}
	s.requests[request.ID] = cancel
	s.mu.Unlock()
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		defer func() {
			s.mu.Lock()
			delete(s.requests, request.ID)
			s.mu.Unlock()
			cancel()
		}()
		data, warnings, err := s.run(ctx, request, params)
		response := serveMessage{Type: "response", ID: request.ID, Data: data, Warnings: warnings}
		if err != nil {
			response.Data, response.Error = nil, domain.AsError(err)
		}
		s.write(response)
	}()
}

// sink captures one request's result and warnings. A subscription sends
// each result as an event instead.
type sink struct {
	mu       sync.Mutex
	data     any
	warnings []string
	event    func(any)
}

func (k *sink) Data(data any) error {
	if k.event != nil {
		k.event(data)
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.data = data
	return nil
}

func (k *sink) Warn(message string) {
	if k.event != nil {
		k.event(map[string]string{"warning": message})
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.warnings = append(k.warnings, message)
}

func (s *session) run(ctx context.Context, request serveRequest, params serveParams) (any, []string, error) {
	plan, err := planRequest(request.Method, params)
	if err != nil {
		return nil, nil, err
	}
	captured := &sink{}
	if plan.stream {
		captured.event = func(data any) { s.write(serveMessage{Type: "event", ID: request.ID, Data: data}) }
	}
	a := *s.a
	a.opts.all, a.opts.withoutResponse = params.All, params.WithoutResponse
	if params.IntervalMS > 0 {
		a.opts.interval = time.Duration(params.IntervalMS) * time.Millisecond
	}
	a.render = output.Renderer{Out: io.Discard, Err: s.a.render.Err, Mode: output.JSON, Verbose: s.a.opts.verbose, Sink: captured}
	ctx = context.WithValue(ctx, rendererKey{}, a.render)
	parent, timeout := ctx, time.Duration(0)
	if !plan.stream {
		timeout = a.opts.timeout
		if params.TimeoutMS > 0 {
			timeout = time.Duration(params.TimeoutMS) * time.Millisecond
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if plan.device != "" && !plan.stream {
		release, err := s.queue(ctx, plan.device)
		if err != nil {
			return nil, nil, classifyContextError(err, parent, timeout)
		}
		defer release()
	}
	if plan.devices {
		err = a.watchDevices(ctx, captured.event)
	} else {
		c := lookupCommand(plan.command)
		if err = c.checkArgs(plan.args); err == nil {
			err = validateFlags(a.opts, c, plan.args)
		}
		if err == nil {
			err = c.run(&a, ctx, plan.args)
		}
	}
	if plan.stream && parent.Err() != nil {
		// Cancelling a subscription is how it ends.
		err = nil
	}
	captured.mu.Lock()
	defer captured.mu.Unlock()
	return captured.data, captured.warnings, classifyContextError(err, parent, timeout)
}

// queue waits until no other request is working on the device, so a
// Bluetooth stack never sees two operations on one device at once.
func (s *session) queue(ctx context.Context, device string) (func(), error) {
	key := strings.ToLower(device)
	s.mu.Lock()
	q, ok := s.queues[key]
	if !ok {
		q = make(chan struct{}, 1)
		s.queues[key] = q
	}
	s.mu.Unlock()
	select {
	case q <- struct{}{}:
		return func() { <-q }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// requestPlan is the command a request runs.
type requestPlan struct {
	command string
	args    []string
	// device is the device query the request works on, for its queue.
	device string
	// stream marks a subscription, which runs until cancelled.
	stream bool
	// devices marks a device-events subscription.
	devices bool
}

var gattOperations = map[string]string{
	"gatt.services": "services", "gatt.characteristics": "characteristics", "gatt.read": "read",
	"gatt.write": "write", "gatt.mtu": "mtu", "gatt.descriptors": "descriptors",
	"gatt.read_descriptor": "read-descriptor", "gatt.write_descriptor": "write-descriptor",
}

func planRequest(method string, p serveParams) (requestPlan, error) {
	need := func(fields ...string) error {
		for _, field := range fields {
			if field == "" {
				return domain.NewError(domain.CodeUsage, "%s needs %s", method, strings.Join(required[method], " and "))
			}
		}
		return nil
	}
	characteristic := p.Characteristic
	if p.Service != "" && characteristic != "" {
		characteristic = p.Service + "/" + characteristic
	}
	switch method {
	case "list", "scan", "capabilities":
		return requestPlan{command: method}, nil
	case "inspect", "pair", "unpair", "connect", "disconnect":
		return requestPlan{command: method, args: []string{p.Device}, device: p.Device}, need(p.Device)
	case "get":
		if p.All {
			return requestPlan{command: "get", args: []string{p.Property}}, need(p.Property)
		}
		return requestPlan{command: "get", args: []string{p.Device, p.Property}, device: p.Device}, need(p.Device, p.Property)
	case "set":
		value := jsonText(p.Value)
		return requestPlan{command: "set", args: []string{p.Device, p.Property, value}, device: p.Device}, need(p.Device, p.Property, value)
	case "adapter":
		return requestPlan{command: "adapter"}, nil
	case "adapter.set":
		value := jsonText(p.Value)
		if b, err := booleanValue(p.Value); err == nil {
			value = map[bool]string{true: "on", false: "off"}[b]
		}
		return requestPlan{command: "adapter", args: []string{p.Setting, value}}, need(p.Setting, value)
	case "subscribe":
		switch {
		case p.Devices:
			return requestPlan{stream: true, devices: true}, nil
		case p.Device != "" && characteristic != "":
			return requestPlan{command: "gatt", args: []string{p.Device, "watch", characteristic}, device: p.Device, stream: true}, nil
		case p.Device != "" && p.Property != "":
			return requestPlan{command: "watch", args: []string{p.Device, p.Property}, device: p.Device, stream: true}, nil
		}
		return requestPlan{}, domain.NewError(domain.CodeUsage, "subscribe needs device and property, device and characteristic, or devices: true")
	}
	if operation, ok := gattOperations[method]; ok {
		args := []string{p.Device, operation}
		switch operation {
		case "characteristics":
			if p.Service != "" {
				args = append(args, p.Service)
			}
		case "read", "descriptors":
			args = append(args, characteristic)
		case "write":
			args = append(args, characteristic, p.Data)
		case "read-descriptor":
			args = append(args, characteristic, p.Descriptor)
		case "write-descriptor":
			args = append(args, characteristic, p.Descriptor, p.Data)
		}
		return requestPlan{command: "gatt", args: args, device: p.Device}, need(args...)
	}
	return requestPlan{}, domain.NewError(domain.CodeUsage, "unknown method %q; see bt help serve", method)
}

var required = map[string][]string{
	"inspect": {"device"}, "pair": {"device"}, "unpair": {"device"}, "connect": {"device"}, "disconnect": {"device"},
	"get": {"device", "property"}, "set": {"device", "property", "value"}, "adapter.set": {"setting", "value"},
	"gatt.services": {"device"}, "gatt.characteristics": {"device"}, "gatt.mtu": {"device"},
	"gatt.read": {"device", "characteristic"}, "gatt.descriptors": {"device", "characteristic"},
	"gatt.write": {"device", "characteristic", "data"}, "gatt.read_descriptor": {"device", "characteristic", "descriptor"},
	"gatt.write_descriptor": {"device", "characteristic", "descriptor", "data"},
}

// jsonText turns a JSON value into the text the CLI would take: a string's
// contents, or any other value as written.
func jsonText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return strings.TrimSpace(string(raw))
}

func booleanValue(raw json.RawMessage) (bool, error) {
	var b bool
	err := json.Unmarshal(raw, &b)
	return b, err
}

// deviceEvent is one change to the devices the OS knows.
type deviceEvent struct {
	Event   string          `json:"event"`
	Device  *domain.Device  `json:"device,omitempty"`
	Devices []domain.Device `json:"devices,omitempty"`
}

// watchDevices polls the OS's known devices, which needs no scan, and sends
// a snapshot and then each change, until ctx ends.
func (a *application) watchDevices(ctx context.Context, emit func(any)) error {
	var previous map[string]domain.Device
	for {
		devices, err := a.backend.List(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			emit(map[string]string{"warning": "listing devices failed, retrying: " + err.Error()})
		} else {
			current := map[string]domain.Device{}
			for _, d := range devices {
				current[d.ID] = d
			}
			if previous == nil {
				emit(deviceEvent{Event: "snapshot", Devices: nonNil(devices)})
			} else {
				for _, event := range deviceChanges(previous, current) {
					emit(event)
				}
			}
			previous = current
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(a.opts.interval):
		}
	}
}

// deviceChanges lists what changed between two polls, in device ID order.
func deviceChanges(previous, current map[string]domain.Device) []deviceEvent {
	var events []deviceEvent
	add := func(event string, d domain.Device) { events = append(events, deviceEvent{Event: event, Device: &d}) }
	ids := slices.Sorted(func(yield func(string) bool) {
		for id := range previous {
			if !yield(id) {
				return
			}
		}
		for id := range current {
			if _, seen := previous[id]; !seen && !yield(id) {
				return
			}
		}
	})
	for _, id := range ids {
		before, was := previous[id]
		after, is := current[id]
		switch {
		case !is:
			add("removed", before)
		case !was:
			add("added", after)
		default:
			if !before.Paired && after.Paired {
				add("paired", after)
			}
			if before.Paired && !after.Paired {
				add("unpaired", after)
			}
			if !before.Connected && after.Connected {
				add("connected", after)
			}
			if before.Connected && !after.Connected {
				add("disconnected", after)
			}
			if before.Name != after.Name {
				add("renamed", after)
			}
		}
	}
	return events
}
