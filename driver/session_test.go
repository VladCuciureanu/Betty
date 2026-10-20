package driver

import (
	"encoding/json"
	"errors"
	"net"
	"testing"
)

func TestSessionHostCall(t *testing.T) {
	host, child := net.Pipe()
	defer host.Close()
	defer child.Close()
	session := NewSession(child, child)
	hostEncoder, hostDecoder := json.NewEncoder(host), json.NewDecoder(host)
	go func() {
		_ = hostEncoder.Encode(Request{Protocol: Version, Type: TypeRequest, ID: "request-1", Method: "inspect"})
	}()
	request, err := session.Receive()
	if err != nil || request.ID != "request-1" {
		t.Fatalf("request=%#v err=%v", request, err)
	}
	done := make(chan error, 1)
	var result struct {
		Value string `json:"value"`
	}
	go func() { done <- session.CallHost(HostStorageGet, StorageGetParams{Key: "key"}, &result) }()
	var call Message
	if err := hostDecoder.Decode(&call); err != nil {
		t.Fatal(err)
	}
	if call.Type != TypeHostRequest || call.Method != HostStorageGet {
		t.Fatalf("call=%#v", call)
	}
	raw, _ := json.Marshal(map[string]string{"value": "stored"})
	if err := hostEncoder.Encode(Message{Protocol: Version, Type: TypeHostResponse, ID: call.ID, Result: raw}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if result.Value != "stored" {
		t.Fatalf("result=%#v", result)
	}
}

func TestSessionPreservesStructuredHostError(t *testing.T) {
	host, child := net.Pipe()
	defer host.Close()
	defer child.Close()
	session := NewSession(child, child)
	hostEncoder, hostDecoder := json.NewEncoder(host), json.NewDecoder(host)
	done := make(chan error, 1)
	go func() { done <- session.CallHost(HostL2CAPOpen, L2CAPOpenParams{DeviceID: "device", PSM: 25}, nil) }()
	var call Message
	if err := hostDecoder.Decode(&call); err != nil {
		t.Fatal(err)
	}
	if err := hostEncoder.Encode(Message{Protocol: Version, Type: TypeHostResponse, ID: call.ID, Error: "gone", ErrorCode: "device_disconnected", Details: map[string]string{"device_id": "device"}}); err != nil {
		t.Fatal(err)
	}
	err := <-done
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != "device_disconnected" || remote.Details == nil {
		t.Fatalf("err=%#v", err)
	}
}

func TestGATTWrappersSendTheirParameters(t *testing.T) {
	host, child := net.Pipe()
	defer host.Close()
	defer child.Close()
	session := NewSession(child, child)
	hostEncoder, hostDecoder := json.NewEncoder(host), json.NewDecoder(host)
	answer := func(want string, result any) map[string]any {
		var call Message
		if err := hostDecoder.Decode(&call); err != nil {
			t.Error(err)
			return nil
		}
		if call.Method != want {
			t.Errorf("method=%s, want %s", call.Method, want)
		}
		var params map[string]any
		_ = json.Unmarshal(call.Params, &params)
		raw, _ := json.Marshal(result)
		_ = hostEncoder.Encode(Message{Protocol: Version, Type: TypeHostResponse, ID: call.ID, Result: raw})
		return params
	}
	got := make(chan map[string]any, 2)
	go func() {
		got <- answer(HostGATTSubscribe, map[string]string{"subscription_id": "gatt-1"})
		got <- answer(HostGATTNext, map[string]string{"data": "0a0b"})
	}()
	id, err := session.SubscribeGATT("sensor", "", "2a37")
	if err != nil || id != "gatt-1" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if params := <-got; params["device_id"] != "sensor" || params["characteristic_uuid"] != "2a37" || params["service_uuid"] != nil {
		t.Fatalf("subscribe params=%v", params)
	}
	data, err := session.NextGATT(id, 0)
	if err != nil || string(data) != "\x0a\x0b" {
		t.Fatalf("data=%x err=%v", data, err)
	}
	if params := <-got; params["subscription_id"] != "gatt-1" || params["timeout_ms"] != nil {
		t.Fatalf("next params=%v", params)
	}
}
