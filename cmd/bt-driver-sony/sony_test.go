package main

import (
	"bytes"
	"testing"

	protocol "github.com/vladcuciureanu/betty/driver"
)

func TestSonyFrameRoundTripAndFragmentation(t *testing.T) {
	payload := []byte{0x23, 0x00, 89, 1, 0x3c, 0x3d, 0x3e}
	encoded := encodeSonyFrame(sonyDataMDR, 1, payload)
	var stream sonyStream
	frames, err := stream.push(encoded[:4])
	if err != nil || len(frames) != 0 {
		t.Fatalf("first push frames=%v err=%v", frames, err)
	}
	frames, err = stream.push(encoded[4:])
	if err != nil || len(frames) != 1 {
		t.Fatalf("second push frames=%v err=%v", frames, err)
	}
	if frames[0].DataType != sonyDataMDR || frames[0].Sequence != 1 || !bytes.Equal(frames[0].Payload, payload) {
		t.Fatalf("frame=%#v", frames[0])
	}
}

func TestSonyFrameRejectsChecksum(t *testing.T) {
	encoded := encodeSonyFrame(sonyDataMDR, 0, []byte{0x22, 0x00})
	encoded[len(encoded)-2] ^= 1
	if _, err := (&sonyStream{}).push(encoded); err == nil {
		t.Fatal("expected checksum error")
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
	response := inspect(nil, []protocol.Device{{ID: "other", Name: "Keyboard"}}, "scan")
	if len(response.Enrichments) != 0 {
		t.Fatalf("enrichments=%#v", response.Enrichments)
	}
}
