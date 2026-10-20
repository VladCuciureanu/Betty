package main

import (
	"bytes"
	"encoding/json"
	"testing"

	protocol "github.com/vladcuciureanu/betty/driver"
)

func TestSonyCapabilitiesUseGenericPropertyTypes(t *testing.T) {
	capabilities := sonyCapabilities(protocolV2)
	if capabilities["battery"].Kind != "battery" || capabilities["noise_control"].Kind != "enum" || capabilities["sony.speak_to_chat"].Kind != "boolean" {
		t.Fatalf("capabilities=%#v", capabilities)
	}
}

func TestSonyFrameRoundTripAndFragmentation(t *testing.T) {
	payload := []byte{0x23, 0x00, 89, 1, 0x3c, 0x3d, 0x3e}
	encoded := encodeSonyFrame(sonyDataMDR, 1, payload)
	var stream sonyStream
	frames, dropped := stream.push(encoded[:4])
	if dropped != 0 || len(frames) != 0 {
		t.Fatalf("first push frames=%v dropped=%d", frames, dropped)
	}
	frames, dropped = stream.push(encoded[4:])
	if dropped != 0 || len(frames) != 1 {
		t.Fatalf("second push frames=%v dropped=%d", frames, dropped)
	}
	if frames[0].DataType != sonyDataMDR || frames[0].Sequence != 1 || !bytes.Equal(frames[0].Payload, payload) {
		t.Fatalf("frame=%#v", frames[0])
	}
}

func TestSonyFrameRejectsChecksum(t *testing.T) {
	encoded := encodeSonyFrame(sonyDataMDR, 0, []byte{0x22, 0x00})
	encoded[len(encoded)-2] ^= 1
	if frames, dropped := (&sonyStream{}).push(encoded); len(frames) != 0 || dropped != 1 {
		t.Fatalf("frames=%v dropped=%d", frames, dropped)
	}
}

func TestSonyStreamResynchronizesAfterCorruptFrames(t *testing.T) {
	good := encodeSonyFrame(sonyDataMDR, 1, []byte{0x23, 0x00, 89, 1})
	corrupt := encodeSonyFrame(sonyDataMDR, 0, []byte{0x22, 0x00})
	corrupt[len(corrupt)-2] ^= 1
	truncated := encodeSonyFrame(sonyDataMDR, 0, []byte{0x24, 0x00})
	truncated = truncated[:len(truncated)-1] // its end byte was lost
	var data []byte
	data = append(data, 0x00, 0x01) // noise before any frame
	data = append(data, corrupt...)
	data = append(data, truncated...)
	data = append(data, good...)
	var stream sonyStream
	frames, dropped := stream.push(data)
	if len(frames) != 1 || frames[0].Payload[0] != 0x23 {
		t.Fatalf("frames=%#v", frames)
	}
	if dropped != 3 {
		t.Fatalf("dropped=%d, want the noise, the corrupt frame and the truncated one", dropped)
	}
	// The stream keeps working afterwards, including across reads.
	if frames, dropped = stream.push(good[:3]); len(frames) != 0 || dropped != 0 {
		t.Fatalf("frames=%v dropped=%d", frames, dropped)
	}
	if frames, dropped = stream.push(good[3:]); len(frames) != 1 || dropped != 0 {
		t.Fatalf("frames=%v dropped=%d", frames, dropped)
	}
}

func TestSonyStreamBoundsAnUnterminatedFrame(t *testing.T) {
	var stream sonyStream
	stream.push([]byte{0x3e})
	_, dropped := stream.push(bytes.Repeat([]byte{0x01}, maxSonyBuffer))
	if dropped != 1 || len(stream.buffer) != 0 {
		t.Fatalf("dropped=%d buffered=%d", dropped, len(stream.buffer))
	}
}

func TestParseSonyBattery(t *testing.T) {
	single, err := parseSonyBattery(0x00, []byte{0x23, 0x00, 89, 1})
	if err != nil || single.Headset == nil || single.Headset.Level != 89 || !single.Headset.Charging {
		t.Fatalf("single=%#v err=%v", single, err)
	}
	buds, err := parseSonyBattery(0x09, []byte{0x23, 0x09, 88, 0, 76, 1})
	if err != nil || buds.Left.Level != 88 || buds.Right.Level != 76 || !buds.Right.Charging {
		t.Fatalf("buds=%#v err=%v", buds, err)
	}
	caseBattery, err := parseSonyBattery(0x0a, []byte{0x23, 0x0a, 42, 0})
	if err != nil || caseBattery.Case.Level != 42 {
		t.Fatalf("case=%#v err=%v", caseBattery, err)
	}
}

func TestSonyV2NoiseControlFixtures(t *testing.T) {
	standard, err := parseSonyNoiseStateV2([]byte{0x67, 0x17, 0x01, 0x01, 0x01, 0x00, 0x0c}, sonyNoiseStandard)
	if err != nil || standard.Mode != "ambient" || standard.AmbientLevel == nil || *standard.AmbientLevel != 12 || standard.VoiceFocus {
		t.Fatalf("standard=%+v err=%v", standard, err)
	}
	reserved, err := parseSonyNoiseStateV2([]byte{0x67, 0x17, 0x01, 0x01, 0x01, 0x01, 0x01}, sonyNoiseReservedByte)
	if err != nil || reserved.Mode != "ambient" || reserved.AmbientLevel != nil || !reserved.VoiceFocus {
		t.Fatalf("reserved=%+v err=%v", reserved, err)
	}
	off, err := parseSonyNoiseStateV2([]byte{0x67, 0x17, 0x01, 0x00, 0x00, 0x00, 0x01}, sonyNoiseStandard)
	if err != nil || off.Mode != "off" || off.AmbientLevel == nil || *off.AmbientLevel != 1 {
		t.Fatalf("off=%+v err=%v", off, err)
	}
}

func TestSonyV2NoiseControlWriteFixtures(t *testing.T) {
	level := 12
	standard, err := encodeSonyNoiseStateV2(sonyNoiseState{Mode: "ambient", AmbientLevel: &level, VoiceFocus: true}, sonyNoiseStandard)
	if err != nil || !bytes.Equal(standard, []byte{0x68, 0x17, 0x01, 0x01, 0x01, 0x01, 0x0c}) {
		t.Fatalf("standard=%x err=%v", standard, err)
	}
	reserved, err := encodeSonyNoiseStateV2(sonyNoiseState{Mode: "anc"}, sonyNoiseReservedByte)
	if err != nil || !bytes.Equal(reserved, []byte{0x68, 0x17, 0x01, 0x01, 0x00, 0x02, 0x00, 0x00}) {
		t.Fatalf("reserved=%x err=%v", reserved, err)
	}
	if _, err := encodeSonyNoiseStateV2(sonyNoiseState{Mode: "unknown"}, sonyNoiseStandard); err == nil {
		t.Fatal("expected invalid mode error")
	}
}

func TestSonyV2NoiseControlRejectsMalformedState(t *testing.T) {
	fixtures := [][]byte{
		{0x67, 0x17},
		{0x67, 0x17, 0x01, 0x02, 0, 0, 1},
		{0x67, 0x17, 0x01, 1, 1, 0, 0},
		{0x67, 0x17, 0x01, 1, 1, 0, 21},
	}
	for _, fixture := range fixtures {
		if _, err := parseSonyNoiseStateV2(fixture, sonyNoiseStandard); err == nil {
			t.Fatalf("accepted malformed payload %x", fixture)
		}
	}
}

func TestSonyNoiseLayoutsAreModelScoped(t *testing.T) {
	if layout, ok := sonyNoiseLayoutForModel("WH-CH720N"); !ok || layout != sonyNoiseStandard {
		t.Fatalf("CH720N layout=%d ok=%t", layout, ok)
	}
	if layout, ok := sonyNoiseLayoutForModel("ULT WEAR"); !ok || layout != sonyNoiseReservedByte {
		t.Fatalf("ULT layout=%d ok=%t", layout, ok)
	}
	if _, ok := sonyNoiseLayoutForModel("WH-1000XM5"); ok {
		t.Fatal("unverified model received a control layout")
	}
}

func TestSonyControlValidationGate(t *testing.T) {
	t.Setenv("BETTY_VALIDATE_SONY_CONTROLS", "")
	if sonyCapabilitiesFor("WH-CH720N", protocolV2)["noise_control"].Status != "planned" {
		t.Fatal("controls must remain planned by default")
	}
	t.Setenv("BETTY_VALIDATE_SONY_CONTROLS", "1")
	capabilities := sonyCapabilitiesFor("WH-CH720N", protocolV2)
	if capabilities["noise_control"].Status != "available" || capabilities["ambient_level"].Status != "available" {
		t.Fatalf("capabilities=%#v", capabilities)
	}
	ult := sonyCapabilitiesFor("ULT WEAR", protocolV2)
	if ult["noise_control"].Status != "available" || ult["ambient_level"].Status != "planned" {
		t.Fatalf("ULT capabilities=%#v", ult)
	}
}

func TestApplySonyControlInput(t *testing.T) {
	level := 4
	state := sonyNoiseState{Mode: "anc", AmbientLevel: &level}
	if err := applySonyControlInput(&state, "noise_control", json.RawMessage(`"ambient"`), sonyNoiseStandard); err != nil || state.Mode != "ambient" {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if err := applySonyControlInput(&state, "ambient_level", json.RawMessage(`12`), sonyNoiseStandard); err != nil || state.AmbientLevel == nil || *state.AmbientLevel != 12 {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if err := applySonyControlInput(&state, "ambient_level", json.RawMessage(`12`), sonyNoiseReservedByte); err == nil {
		t.Fatal("ULT layout accepted unsupported ambient level")
	}
}

func TestIdentifySonyByProtocolAndName(t *testing.T) {
	model, version, ok := identify(protocol.Device{Name: "Headphones", Services: []string{"956C7B26-D49A-4BA8-B03F-B17D393CB6E2"}})
	if !ok || model != "Headphones" || version != protocolV2 {
		t.Fatalf("model=%q version=%d ok=%t", model, version, ok)
	}
	model, version, ok = identify(protocol.Device{Name: "Sony WH-1000XM5"})
	if !ok || model != "WH-1000XM5" || version != protocolUnknown {
		t.Fatalf("model=%q version=%d ok=%t", model, version, ok)
	}
}

func TestUnknownDeviceIsIgnored(t *testing.T) {
	enrichments, err := inspect(nil, protocol.InspectRequest{Purpose: protocol.PurposeScan, Devices: []protocol.Device{{ID: "other", Name: "Keyboard"}}})
	if err != nil || len(enrichments) != 0 {
		t.Fatalf("enrichments=%#v err=%v", enrichments, err)
	}
}

func TestSonyBatteryGetReportsDisconnectedDevice(t *testing.T) {
	enrichment, err := getBattery(nil, protocol.Device{ID: "sony", Name: "WH-1000XM5"})
	if code, _ := protocol.CodeOf(err, protocol.CodeInternal); enrichment != nil || code != protocol.CodeDeviceDisconnected {
		t.Fatalf("enrichment=%+v err=%v", enrichment, err)
	}
}

func TestSonyDriverServesOverProtocol(t *testing.T) {
	driver := protocol.Driver{Manifest: manifest, Inspect: inspect, Get: get, Set: set}
	var in, out bytes.Buffer
	devices := []protocol.Device{{ID: "sony", Name: "WH-1000XM5", Services: []string{sonyServiceV2}}, {ID: "other", Name: "Keyboard"}}
	encoder := json.NewEncoder(&in)
	_ = encoder.Encode(protocol.Request{Protocol: protocol.Version, Type: protocol.TypeRequest, ID: "1", Method: protocol.MethodInspect, Purpose: protocol.PurposeList, Devices: devices})
	_ = encoder.Encode(protocol.Request{Protocol: protocol.Version, Type: protocol.TypeRequest, ID: "2", Method: protocol.MethodGet, DeviceID: "sony", Property: "battery", Devices: devices})
	_ = encoder.Encode(protocol.Request{Protocol: protocol.Version, Type: protocol.TypeRequest, ID: "3", Method: protocol.MethodSet, DeviceID: "sony", Property: "equalizer", Value: json.RawMessage(`"bass"`), Devices: devices})
	if err := driver.Run(nil, &in, &out); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&out)
	var responses [3]protocol.Response
	for i := range responses {
		if err := decoder.Decode(&responses[i]); err != nil {
			t.Fatal(err)
		}
	}
	inspected, ok := responses[0].Enrichments["sony"]
	if !ok || len(responses[0].Enrichments) != 1 || inspected.Identification.Confidence != protocol.ConfidenceProtocol {
		t.Fatalf("inspect=%+v", responses[0])
	}
	if responses[1].ErrorCode != string(protocol.CodeDeviceDisconnected) {
		t.Fatalf("get=%+v", responses[1])
	}
	if responses[2].Error != "" || len(responses[2].Enrichments) != 0 {
		t.Fatalf("set of an unhandled property must be left to other drivers: %+v", responses[2])
	}
}
