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
