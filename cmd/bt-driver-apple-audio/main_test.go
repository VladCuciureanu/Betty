package main

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"io"
	"strings"
	"testing"

	protocol "github.com/vladcuciureanu/betty/driver"
)

func TestInspectRecognizesAndCorrelatesUniqueAirPods(t *testing.T) {
	devices := []protocol.Device{{ID: "classic", Name: "AirPods Pro", Connected: true}, {ID: "ble", Name: "uuid", ManufacturerData: []protocol.ManufacturerData{{CompanyID: 0x004c, Data: "07190124202b998f110004446263ff09d489000000061e82f24d93"}}}}
	response := inspect(protocol.NewSession(strings.NewReader(""), io.Discard), devices, "scan")
	classic := response.Enrichments["classic"]
	advertisement := response.Enrichments["ble"]
	if advertisement.Identification == nil || advertisement.Identification.Model != "AirPods Pro (2nd generation, USB-C)" {
		t.Fatalf("identification=%#v", advertisement.Identification)
	}
	if len(classic.PossibleMatches) != 1 || classic.PossibleMatches[0] != "ble" {
		t.Fatalf("classic=%#v", classic)
	}
	if advertisement.Properties["battery"].Display != "left ~90% · right ~90% · case —" || !advertisement.Properties["battery"].Estimated {
		t.Fatalf("battery=%q", advertisement.Properties["battery"].Display)
	}
	noiseControl := classic.Capabilities["noise_control"]
	if noiseControl.Status != "planned" || !noiseControl.Readable || !noiseControl.Writable || len(noiseControl.Values) != 4 {
		t.Fatalf("noise_control=%#v", noiseControl)
	}
}

func TestInspectDoesNotGuessWithMultipleAdvertisements(t *testing.T) {
	payload := []protocol.ManufacturerData{{CompanyID: 0x004c, Data: "07190124202b998f110004446263ff09d489000000061e82f24d93"}}
	response := inspect(protocol.NewSession(strings.NewReader(""), io.Discard), []protocol.Device{{ID: "classic", Name: "AirPods", Connected: true}, {ID: "one", ManufacturerData: payload}, {ID: "two", ManufacturerData: payload}}, "scan")
	if len(response.Enrichments["classic"].PossibleMatches) != 0 {
		t.Fatalf("guessed match: %#v", response.Enrichments["classic"])
	}
}

func TestInspectRecognizesPowerbeatsPro2(t *testing.T) {
	devices := []protocol.Device{
		{ID: "classic", Name: "Powerbeats Pro 2", Connected: true},
		{ID: "ble", Name: "uuid", ManufacturerData: []protocol.ManufacturerData{{CompanyID: 0x004c, Data: "0719011d202b998f110004446263ff09d489000000061e82f24d93"}}},
	}
	response := inspect(protocol.NewSession(strings.NewReader(""), io.Discard), devices, "scan")
	advertisement := response.Enrichments["ble"]
	if advertisement.Identification == nil || advertisement.Identification.Vendor != "Beats" || advertisement.Identification.Family != "Beats" || advertisement.Identification.Model != "Powerbeats Pro 2" {
		t.Fatalf("identification=%#v", advertisement.Identification)
	}
	if _, ok := advertisement.Capabilities["heart_rate"]; !ok {
		t.Fatalf("capabilities=%#v", advertisement.Capabilities)
	}
	if got := advertisement.Properties["battery"].Display; got != "left ~90% · right ~90% · case —" {
		t.Fatalf("battery=%q", got)
	}
}

func TestParseDeviceRejectsUnknownAppleAudioModel(t *testing.T) {
	device := protocol.Device{ManufacturerData: []protocol.ManufacturerData{{CompanyID: 0x004c, Data: "07190199202b998f110004446263ff09d489000000061e82f24d93"}}}
	if _, ok := parseDevice(device); ok {
		t.Fatal("unknown Apple Continuity model was incorrectly claimed")
	}
}

func TestSingleBeatsBatteryUsesHeadsetShape(t *testing.T) {
	device := protocol.Device{ManufacturerData: []protocol.ManufacturerData{{CompanyID: 0x004c, Data: "07190117202b998f110004446263ff09d489000000061e82f24d93"}}}
	ad, ok := parseDevice(device)
	if !ok {
		t.Fatal("Beats Studio Pro was not recognized")
	}
	if got := batteryDisplay(ad); got != "headset ~90%" {
		t.Fatalf("battery=%q", got)
	}
	battery := passiveBattery(ad)
	if battery["headset"] == nil || len(battery) != 1 {
		t.Fatalf("battery=%#v", battery)
	}
}

func TestPowerbeatsPro2NameSelectsMostSpecificCapabilities(t *testing.T) {
	capabilities := connectedAppleAudioCapabilities(protocol.Device{Name: "Powerbeats Pro 2"}, nil)
	if _, ok := capabilities["heart_rate"]; !ok {
		t.Fatalf("capabilities=%#v", capabilities)
	}
}

func TestAppleAudioManifestMigratesAirPodsDriver(t *testing.T) {
	if manifest.ID != "official/apple-audio" || len(manifest.LegacyIDs) != 1 || manifest.LegacyIDs[0] != "official/airpods" {
		t.Fatalf("manifest=%#v", manifest)
	}
}

func TestParseExactBattery(t *testing.T) {
	packet := mustHex("040004000400030401590201020158020108012a0101")
	battery, err := parseExactBattery(packet)
	if err != nil {
		t.Fatal(err)
	}
	if battery.Left == nil || battery.Left.Level != 89 || battery.Left.Charging {
		t.Fatalf("left=%#v", battery.Left)
	}
	if battery.Right == nil || battery.Right.Level != 88 || battery.Right.Charging {
		t.Fatalf("right=%#v", battery.Right)
	}
	if battery.Case == nil || battery.Case.Level != 42 || !battery.Case.Charging {
		t.Fatalf("case=%#v", battery.Case)
	}
	if got := exactBatteryDisplay(battery); got != "left 89% · right 88% · case 42% charging" {
		t.Fatalf("display=%q", got)
	}
}

func TestParseExactBatteryRejectsMalformedPacket(t *testing.T) {
	if _, err := parseExactBattery(mustHex("04000400040003040159")); err == nil {
		t.Fatal("expected malformed packet error")
	}
}

func TestEncryptedAdvertisementExactBattery(t *testing.T) {
	key := mustHex("00112233445566778899aabbccddeeff")
	plain := make([]byte, 16)
	copy(plain, []byte{0x04, 0xd9, 0x58, 0xaa}) // 89% charging, 88%, case 42% charging
	block, _ := aes.NewCipher(key)
	encrypted := make([]byte, 16)
	cipher.NewCBCEncrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(encrypted, plain)
	payload := append(mustHex("0719012420200000000000"), encrypted...)
	device := protocol.Device{ManufacturerData: []protocol.ManufacturerData{{CompanyID: 0x004c, Data: hex.EncodeToString(payload)}}}
	battery, ok := decryptAdvertisement(device, 0x2420, appleAudioKeyring{Keys: []appleAudioKeys{{ModelID: 0x2420, ENC: hex.EncodeToString(key)}}})
	if !ok || battery.Left == nil || battery.Left.Level != 89 || !battery.Left.Charging || battery.Right.Level != 88 || battery.Case.Level != 42 {
		t.Fatalf("battery=%#v ok=%t", battery, ok)
	}
}

func TestParsePrivateKeys(t *testing.T) {
	packet := append(mustHex("04000400310002"), mustHex("01001000")...)
	packet = append(packet, mustHex("00112233445566778899aabbccddeeff")...)
	packet = append(packet, mustHex("04001000")...)
	packet = append(packet, mustHex("ffeeddccbbaa99887766554433221100")...)
	keys, err := parsePrivateKeys(packet)
	if err != nil || keys.IRK != "00112233445566778899aabbccddeeff" || keys.ENC != "ffeeddccbbaa99887766554433221100" {
		t.Fatalf("keys=%#v err=%v", keys, err)
	}
}
