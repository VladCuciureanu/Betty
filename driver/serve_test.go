package driver

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

var testManifest = Manifest{ID: "example/test", Name: "Test", Version: "1.0.0", Permissions: []string{PermissionStorage}}

// serve runs d over the given requests and returns every response it wrote.
func serve(t *testing.T, d Driver, requests ...Request) []Response {
	t.Helper()
	var in bytes.Buffer
	encoder := json.NewEncoder(&in)
	for _, request := range requests {
		if err := encoder.Encode(request); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := d.Run(nil, &in, &out); err != nil {
		t.Fatal(err)
	}
	var responses []Response
	decoder := json.NewDecoder(&out)
	for {
		var response Response
		if err := decoder.Decode(&response); errors.Is(err, io.EOF) {
			return responses
		} else if err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
}

func TestServePrintsManifestWithDefaultProtocol(t *testing.T) {
	var out bytes.Buffer
	if err := (Driver{Manifest: testManifest}).Run([]string{"--manifest"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(out.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Protocol != Version || manifest.ID != testManifest.ID {
		t.Fatalf("manifest=%+v", manifest)
	}
}

func TestServeRejectsInvalidManifestID(t *testing.T) {
	err := (Driver{Manifest: Manifest{ID: "No Publisher"}}).Run([]string{"--manifest"}, nil, io.Discard)
	if err == nil {
		t.Fatal("expected invalid ID error")
	}
}

func TestServeDispatchesSequentialRequests(t *testing.T) {
	d := Driver{
		Manifest: testManifest,
		Inspect: func(_ *Session, request InspectRequest) (map[string]Enrichment, error) {
			if request.Purpose != PurposeList {
				t.Errorf("purpose=%q", request.Purpose)
			}
			return map[string]Enrichment{"a": {Identification: &Identification{Vendor: "Example", Family: "Test", Confidence: ConfidenceName}}}, nil
		},
		Get: func(_ *Session, request PropertyRequest) (*Enrichment, error) {
			if request.Device.Name != "Device A" || request.Property != "battery" {
				t.Errorf("request=%+v", request)
			}
			return &Enrichment{Properties: map[string]Property{"battery": {Value: json.RawMessage("80"), Unit: "%"}}}, nil
		},
	}
	devices := []Device{{ID: "a", Name: "Device A"}}
	responses := serve(t, d,
		Request{Protocol: Version, Type: TypeRequest, ID: "1", Method: MethodInspect, Purpose: PurposeList, Devices: devices},
		Request{Protocol: Version, Type: TypeRequest, ID: "2", Method: MethodGet, DeviceID: "a", Property: "battery", Devices: devices},
	)
	if len(responses) != 2 {
		t.Fatalf("responses=%+v", responses)
	}
	if responses[0].ID != "1" || responses[0].Type != TypeResponse || responses[0].Protocol != Version || responses[0].Enrichments["a"].Identification == nil {
		t.Fatalf("inspect response=%+v", responses[0])
	}
	if responses[1].ID != "2" || string(responses[1].Enrichments["a"].Properties["battery"].Value) != "80" {
		t.Fatalf("get response=%+v", responses[1])
	}
}

func TestServeLeavesUnhandledRequestsEmpty(t *testing.T) {
	called := false
	d := Driver{Manifest: testManifest, Get: func(*Session, PropertyRequest) (*Enrichment, error) { called = true; return nil, nil }}
	responses := serve(t, d,
		Request{Protocol: Version, ID: "1", Method: MethodSet, DeviceID: "a", Devices: []Device{{ID: "a"}}},
		Request{Protocol: Version, ID: "2", Method: MethodGet, DeviceID: "missing", Devices: []Device{{ID: "a"}}},
		Request{Protocol: Version, ID: "3", Method: MethodGet, DeviceID: "a", Devices: []Device{{ID: "a"}}},
	)
	for _, response := range responses {
		if response.Error != "" || len(response.Enrichments) != 0 {
			t.Fatalf("response=%+v", response)
		}
	}
	if !called {
		t.Fatal("Get was not called for a known device")
	}
}

func TestServeReportsStructuredErrors(t *testing.T) {
	d := Driver{
		Manifest: testManifest,
		Get: func(*Session, PropertyRequest) (*Enrichment, error) {
			return nil, &Error{Code: CodeDeviceDisconnected, Message: "device is asleep", Details: map[string]string{"device_id": "a"}}
		},
		Set: func(*Session, PropertyRequest) (*Enrichment, error) { return nil, errors.New("plain failure") },
		Inspect: func(*Session, InspectRequest) (map[string]Enrichment, error) {
			panic("bad packet")
		},
	}
	devices := []Device{{ID: "a"}}
	responses := serve(t, d,
		Request{Protocol: Version, ID: "1", Method: MethodGet, DeviceID: "a", Devices: devices},
		Request{Protocol: Version, ID: "2", Method: MethodSet, DeviceID: "a", Devices: devices},
		Request{Protocol: Version, ID: "3", Method: MethodInspect, Devices: devices},
		Request{Protocol: Version, ID: "4", Method: "subscribe", Devices: devices},
		Request{Protocol: Version + 1, ID: "5", Method: MethodInspect},
	)
	want := []ErrorCode{CodeDeviceDisconnected, CodeInternal, CodeInternal, CodeUnsupported, CodeUnsupported}
	if len(responses) != len(want) {
		t.Fatalf("responses=%+v", responses)
	}
	for i, response := range responses {
		if response.Error == "" || ErrorCode(response.ErrorCode) != want[i] {
			t.Fatalf("response %d=%+v, want code %s", i, response, want[i])
		}
	}
	if responses[0].Details == nil || !strings.Contains(responses[2].Error, "bad packet") {
		t.Fatalf("responses=%+v", responses)
	}
}

func TestWrapPreservesHostErrorCode(t *testing.T) {
	host := &RemoteError{Code: string(CodeTimeout), Message: "host: read timed out", Details: "detail"}
	wrapped := Wrap(host, CodeBluetoothUnavailable)
	if wrapped.Code != CodeTimeout || wrapped.Details != "detail" || !errors.Is(wrapped, host) {
		t.Fatalf("wrapped=%+v", wrapped)
	}
	if Wrap(errors.New("plain"), CodeBluetoothUnavailable).Code != CodeBluetoothUnavailable {
		t.Fatal("fallback code was not applied")
	}
	if Wrap(nil, CodeInternal) != nil {
		t.Fatal("Wrap(nil) must be nil")
	}
}

func TestDecodeValueReportsUsage(t *testing.T) {
	var level int
	err := PropertyRequest{Property: "ambient_level", Value: json.RawMessage(`"loud"`)}.DecodeValue(&level)
	if code, _ := CodeOf(err, CodeInternal); code != CodeUsage {
		t.Fatalf("err=%v", err)
	}
}

func TestTypedHostCalls(t *testing.T) {
	host, child := net.Pipe()
	defer host.Close()
	defer child.Close()
	session := NewSession(child, child)
	hostEncoder, hostDecoder := json.NewEncoder(host), json.NewDecoder(host)
	replies := map[string]any{
		HostRFCOMMOpen:  map[string]string{"channel_id": "c1"},
		HostRFCOMMWrite: map[string]int{"written": 2},
		HostRFCOMMRead:  map[string]string{"data": "beef"},
		HostRFCOMMClose: map[string]bool{"closed": true},
		HostStorageGet:  map[string]any{"value": "v", "found": true},
	}
	var calls []Message
	go func() {
		for {
			var call Message
			if err := hostDecoder.Decode(&call); err != nil {
				return
			}
			calls = append(calls, call)
			raw, _ := json.Marshal(replies[call.Method])
			_ = hostEncoder.Encode(Message{Protocol: Version, Type: TypeHostResponse, ID: call.ID, Result: raw})
		}
	}()
	channel, err := session.OpenRFCOMM("device", "uuid")
	if err != nil || channel != "c1" {
		t.Fatalf("channel=%q err=%v", channel, err)
	}
	if written, err := session.WriteRFCOMM(channel, []byte{1, 2}); err != nil || written != 2 {
		t.Fatalf("written=%d err=%v", written, err)
	}
	if data, err := session.ReadRFCOMM(channel, 16, 1500*time.Millisecond); err != nil || !bytes.Equal(data, []byte{0xbe, 0xef}) {
		t.Fatalf("data=%x err=%v", data, err)
	}
	if err := session.CloseRFCOMM(channel); err != nil {
		t.Fatal(err)
	}
	if value, found, err := session.GetState("key"); err != nil || !found || value != "v" {
		t.Fatalf("value=%q found=%t err=%v", value, found, err)
	}
	var read RFCOMMReadParams
	if err := json.Unmarshal(calls[2].Params, &read); err != nil || read.TimeoutMS != 1500 || read.MaxBytes != 16 {
		t.Fatalf("read params=%s err=%v", calls[2].Params, err)
	}
}
