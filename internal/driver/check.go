package driver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	protocol "github.com/vladcuciureanu/betty/driver"
)

// CheckResult is the outcome of one conformance check.
type CheckResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

// checkRequestTimeout bounds each scripted request.
const checkRequestTimeout = 5 * time.Second

// Check runs the driver at executable against scripted requests and a host
// that refuses every host call, and reports which rules of the protocol
// specification it follows. It never touches Bluetooth or driver storage.
func Check(ctx context.Context, executable string) ([]CheckResult, error) {
	var results []CheckResult
	record := func(name string, err error) bool {
		result := CheckResult{Name: name, Passed: err == nil}
		if err != nil {
			result.Detail = err.Error()
		}
		results = append(results, result)
		return err == nil
	}

	manifest, err := readManifest(ctx, executable)
	if !record("prints a valid manifest for --manifest", errors.Join(err, validateManifestIf(err, manifest))) {
		return results, nil
	}

	cmd := exec.Command(executable)
	cmd.WaitDelay = time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start driver: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer func() {
		_ = cmd.Process.Kill()
		<-exited
	}()
	session := &checkSession{manifest: manifest, encoder: json.NewEncoder(stdin), reader: bufio.NewReader(stdout)}

	devices := []protocol.Device{
		{ID: "check-ble", Name: "Conformance BLE device", Services: []string{"0000180f-0000-1000-8000-00805f9b34fb"}, Endpoints: []protocol.Endpoint{{ID: "AA:BB:CC:DD:EE:01", Transport: "ble", Address: "AA:BB:CC:DD:EE:01"}}},
		{ID: "check-classic", Name: "Conformance Classic device", Paired: true, Endpoints: []protocol.Endpoint{{ID: "AA:BB:CC:DD:EE:02", Transport: "classic", Address: "AA:BB:CC:DD:EE:02"}}},
	}
	inspect := protocol.Request{Protocol: protocol.Version, Type: protocol.TypeRequest, ID: "check-1", Method: protocol.MethodInspect, Purpose: protocol.PurposeScan, Devices: devices}
	response, err := session.exchange(ctx, inspect)
	if !record("answers inspect with a response carrying the request's ID", err) {
		return append(results, session.hostResults()...), nil
	}
	record("enriches only devices in the request", onlyKnownDevices(response, devices))

	unknown := protocol.Request{Protocol: protocol.Version, Type: protocol.TypeRequest, ID: "check-2", Method: "betty.check.unknown-method", Devices: devices}
	response, err = session.exchange(ctx, unknown)
	if err == nil && response.ErrorCode != string(protocol.CodeUnsupported) {
		err = fmt.Errorf("error_code is %q, want %q", response.ErrorCode, protocol.CodeUnsupported)
	}
	record("keeps reading after a response, and answers an unknown method with unsupported", err)

	get := protocol.Request{Protocol: protocol.Version, Type: protocol.TypeRequest, ID: "check-3", Method: protocol.MethodGet, DeviceID: "check-ble", Property: "betty.check.unknown-property", Devices: devices}
	response, err = session.exchange(ctx, get)
	if err == nil && response.Error == "" {
		if _, claimed := response.Enrichments["check-ble"].Properties["betty.check.unknown-property"]; claimed {
			err = errors.New("returned a value for a property it cannot know")
		}
	}
	record("answers get for a property it doesn't own without claiming it", err)

	_ = stdin.Close()
	select {
	case <-exited:
		exited <- nil // for the deferred cleanup
		record("exits when its input ends", nil)
	case <-time.After(defaultExitGrace):
		record("exits when its input ends", fmt.Errorf("still running %s after its input closed", defaultExitGrace))
	}
	return append(results, session.hostResults()...), nil
}

func validateManifestIf(err error, manifest protocol.Manifest) error {
	if err != nil {
		return nil
	}
	return validateManifest(manifest)
}

func onlyKnownDevices(response protocol.Response, devices []protocol.Device) error {
	for id := range response.Enrichments {
		known := false
		for _, device := range devices {
			known = known || device.ID == id
		}
		if !known {
			return fmt.Errorf("enriched %q, which was not in the request", id)
		}
	}
	return nil
}

// checkSession plays Betty's side of one driver process.
type checkSession struct {
	manifest protocol.Manifest
	encoder  *json.Encoder
	reader   *bufio.Reader
	// undeclared lists host calls made without the matching permission.
	undeclared []string
	// broken is set once an exchange fails: a read may still be pending on
	// the driver's output, so no further request is sent.
	broken error
}

// exchange sends request and returns the driver's response, refusing every
// host call on the way.
func (s *checkSession) exchange(ctx context.Context, request protocol.Request) (protocol.Response, error) {
	var response protocol.Response
	if s.broken != nil {
		return response, fmt.Errorf("not checked: an earlier check failed (%v)", s.broken)
	}
	response, err := s.send(ctx, request)
	if err != nil {
		s.broken = err
	}
	return response, err
}

func (s *checkSession) send(ctx context.Context, request protocol.Request) (protocol.Response, error) {
	var response protocol.Response
	if err := s.encoder.Encode(request); err != nil {
		return response, fmt.Errorf("write request: %w", err)
	}
	type read struct {
		raw []byte
		err error
	}
	deadline := time.After(checkRequestTimeout)
	for {
		next := make(chan read, 1)
		go func() {
			raw, err := readMessage(s.reader, maxMessageSize)
			next <- read{raw, err}
		}()
		var message read
		select {
		case <-ctx.Done():
			return response, ctx.Err()
		case <-deadline:
			return response, fmt.Errorf("no response within %s", checkRequestTimeout)
		case message = <-next:
		}
		if errors.Is(message.err, io.EOF) {
			return response, errors.New("exited without responding")
		}
		if message.err != nil {
			return response, message.err
		}
		var header protocol.Message
		if err := json.Unmarshal(message.raw, &header); err != nil {
			return response, fmt.Errorf("invalid message: %v", err)
		}
		if header.Type == protocol.TypeHostRequest {
			s.refuse(header)
			continue
		}
		if header.Type != protocol.TypeResponse {
			return response, fmt.Errorf("sent message type %q, want %q", header.Type, protocol.TypeResponse)
		}
		if header.ID != request.ID {
			return response, fmt.Errorf("answered request %q instead of %q", header.ID, request.ID)
		}
		if header.Protocol != protocol.Version {
			return response, fmt.Errorf("answered with protocol %d, want %d", header.Protocol, protocol.Version)
		}
		if err := json.Unmarshal(message.raw, &response); err != nil {
			return response, fmt.Errorf("invalid response: %v", err)
		}
		return response, nil
	}
}

func (s *checkSession) refuse(call protocol.Message) {
	namespace, _, _ := strings.Cut(call.Method, ".")
	if permission, known := hostPermissions[namespace]; known && !hasPermission(s.manifest, permission) {
		s.undeclared = append(s.undeclared, call.Method)
	}
	_ = s.encoder.Encode(protocol.Message{Protocol: protocol.Version, Type: protocol.TypeHostResponse, ID: call.ID,
		Error: "the conformance check refuses host calls", ErrorCode: string(protocol.CodePermissionDenied)})
}

func (s *checkSession) hostResults() []CheckResult {
	result := CheckResult{Name: "makes host calls only within its declared permissions", Passed: len(s.undeclared) == 0}
	if !result.Passed {
		result.Detail = "called " + strings.Join(s.undeclared, ", ") + " without the permission"
	}
	return []CheckResult{result}
}
