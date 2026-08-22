package driver

import (
	"encoding/json"
	"testing"
)

func TestPropertyRequestRoundTripAndUnknownFields(t *testing.T) {
	raw := []byte(`{"protocol":1,"type":"request","id":"7","method":"set","device_id":"opaque","property":"noise_control","value":"anc","devices":[],"future_field":true}`)
	var request Request
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	if request.Method != "set" || request.DeviceID != "opaque" || request.Property != "noise_control" || string(request.Value) != `"anc"` {
		t.Fatalf("request=%+v", request)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(encoded) {
		t.Fatalf("invalid JSON: %s", encoded)
	}
}

func TestDriverResponseCarriesStructuredError(t *testing.T) {
	response := Response{Protocol: Version, Error: "device went away", ErrorCode: "device_disconnected", Details: map[string]any{"device_id": "opaque"}}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Response
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ErrorCode != response.ErrorCode || decoded.Details == nil {
		t.Fatalf("response=%+v", decoded)
	}
}
