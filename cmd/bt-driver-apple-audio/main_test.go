package main

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	protocol "github.com/vladcuciureanu/betty/driver"
)

// inspectDevices runs inspect with a host that refuses every call, so the
// keyring is empty.
func inspectDevices(t *testing.T, purpose string, devices ...protocol.Device) map[string]protocol.Enrichment {
	t.Helper()
	enrichments, err := inspect(protocol.NewSession(strings.NewReader(""), io.Discard), protocol.InspectRequest{Purpose: purpose, Devices: devices})
	if err != nil {
		t.Fatal(err)
	}
	return enrichments
}

func TestInspectRecognizesAndCorrelatesUniqueAirPods(t *testing.T) {
	withAAP(t, "")
	devices := []protocol.Device{{ID: "classic", Name: "AirPods Pro", Connected: true}, {ID: "ble", Name: "uuid", ManufacturerData: []protocol.ManufacturerData{{CompanyID: 0x004c, Data: "07190124202b998f110004446263ff09d489000000061e82f24d93"}}}}
	enrichments := inspectDevices(t, protocol.PurposeScan, devices...)
	classic := enrichments["classic"]
	advertisement := enrichments["ble"]
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
	enrichments := inspectDevices(t, protocol.PurposeScan, protocol.Device{ID: "classic", Name: "AirPods", Connected: true}, protocol.Device{ID: "one", ManufacturerData: payload}, protocol.Device{ID: "two", ManufacturerData: payload})
	if len(enrichments["classic"].PossibleMatches) != 0 {
		t.Fatalf("guessed match: %#v", enrichments["classic"])
	}
}

func TestInspectRecognizesPowerbeatsPro2(t *testing.T) {
	devices := []protocol.Device{
		{ID: "classic", Name: "Powerbeats Pro 2", Connected: true},
		{ID: "ble", Name: "uuid", ManufacturerData: []protocol.ManufacturerData{{CompanyID: 0x004c, Data: "0719011d202b998f110004446263ff09d489000000061e82f24d93"}}},
	}
	enrichments := inspectDevices(t, protocol.PurposeScan, devices...)
	advertisement := enrichments["ble"]
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
	if battery.Headset == nil || battery.Headset.Level != 90 || battery.Left != nil || battery.Right != nil || battery.Case != nil {
		t.Fatalf("battery=%#v", battery)
	}
}

func TestPowerbeatsPro2NameSelectsMostSpecificCapabilities(t *testing.T) {
	capabilities := connectedAppleAudioCapabilities(protocol.Device{Name: "Powerbeats Pro 2"}, nil)
	if _, ok := capabilities["heart_rate"]; !ok {
		t.Fatalf("capabilities=%#v", capabilities)
	}
}

func TestInspectIdentifiesSavedAppleAudioWithoutConnecting(t *testing.T) {
	enrichments := inspectDevices(t, protocol.PurposeList, protocol.Device{ID: "saved", Name: "AirPods Pro", Paired: true})
	identity := enrichments["saved"].Identification
	if identity == nil || identity.Vendor != "Apple" || identity.Family != "AirPods" || identity.Model != "AirPods Pro" || identity.Confidence != "name" {
		t.Fatalf("identity=%#v", identity)
	}
}

func TestAppleAudioCapabilitiesUseGenericPropertyTypes(t *testing.T) {
	capabilities := appleAudioCapabilities(appleAudioModels[0x1420])
	if capabilities["battery"].Kind != "battery" || capabilities["noise_control"].Kind != "enum" {
		t.Fatalf("capabilities=%#v", capabilities)
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

func TestAAPNoiseControlCodec(t *testing.T) {
	for _, mode := range []string{"off", "anc", "transparency", "adaptive"} {
		packet, err := encodeAAPNoiseControl(mode)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := parseAAPNoiseControl(packet); !ok || got != mode {
			t.Fatalf("parseAAPNoiseControl(%x) = %q, %t", packet, got, ok)
		}
	}
}

func TestAAPNoiseControlCodecRejectsMalformedPackets(t *testing.T) {
	for _, packet := range [][]byte{mustHex("0400040009000d02"), mustHex("0400040009000d02000001"), mustHex("0400040009000dff000000")} {
		if mode, ok := parseAAPNoiseControl(packet); ok {
			t.Fatalf("accepted malformed packet %x as %q", packet, mode)
		}
	}
	if _, err := encodeAAPNoiseControl("spatial"); err == nil {
		t.Fatal("accepted unsupported mode")
	}
}

// encryptedAdvertisement builds an AirPods Pro Proximity Pairing
// advertisement whose unencrypted estimates are 90%, 90%, and case 40%, and
// whose encrypted payload holds plain under key.
func encryptedAdvertisement(key, plain []byte, address string) protocol.Device {
	block, _ := aes.NewCipher(key)
	encrypted := make([]byte, 16)
	cipher.NewCBCEncrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(encrypted, plain)
	payload := append(mustHex("0719012420209904000000"), encrypted...)
	device := protocol.Device{ManufacturerData: []protocol.ManufacturerData{{CompanyID: 0x004c, Data: hex.EncodeToString(payload)}}}
	if address != "" {
		device.Endpoints = []protocol.Endpoint{{ID: address, Transport: "ble", Address: address}}
	}
	return device
}

// exactPayload is 89% charging, 88%, case 42% charging.
var exactPayload = append([]byte{0x04, 0xd9, 0x58, 0xaa}, make([]byte, 12)...)

func TestEncryptedAdvertisementExactBattery(t *testing.T) {
	key := mustHex("00112233445566778899aabbccddeeff")
	device := encryptedAdvertisement(key, exactPayload, "")
	battery, ok := decryptAdvertisement(device, 0x2420, appleAudioKeyring{Keys: []appleAudioKeys{{ModelID: 0x2420, ENC: hex.EncodeToString(key)}}})
	if !ok || battery.Left == nil || battery.Left.Level != 89 || !battery.Left.Charging || battery.Right.Level != 88 || battery.Case.Level != 42 {
		t.Fatalf("battery=%#v ok=%t", battery, ok)
	}
}

func TestWrongKeysAreRejected(t *testing.T) {
	key := mustHex("00112233445566778899aabbccddeeff")
	device := encryptedAdvertisement(key, exactPayload, "")
	data, _ := hex.DecodeString(device.ManufacturerData[0].Data)
	// Find wrong keys that pass the format check alone, which accepted a
	// wrong key about 2 times in 256, and require every one to be rejected.
	formatOnly := 0
	for i := 0; formatOnly < 25; i++ {
		wrong := make([]byte, 16)
		wrong[0], wrong[1], wrong[15] = byte(i), byte(i>>8), 0x5a
		plain, err := decryptPrivatePayload(data[11:27], hex.EncodeToString(wrong))
		if err != nil || (plain[0] != 0x04 && plain[0] != 0x14) {
			continue
		}
		formatOnly++
		keyring := appleAudioKeyring{Keys: []appleAudioKeys{{ENC: hex.EncodeToString(wrong)}}}
		if battery, ok := decryptAdvertisement(device, 0x2420, keyring); ok {
			t.Fatalf("wrong key %x was accepted: %#v", wrong, battery)
		}
	}
}

func TestDecryptedLevelsMustAgreeWithTheEstimate(t *testing.T) {
	key := mustHex("00112233445566778899aabbccddeeff")
	far := append([]byte{0x04, 0x14, 0x15, 0x2a}, make([]byte, 12)...) // 20%, 21%, case 42%
	device := encryptedAdvertisement(key, far, "")
	if _, ok := decryptAdvertisement(device, 0x2420, appleAudioKeyring{Keys: []appleAudioKeys{{ENC: hex.EncodeToString(key)}}}); ok {
		t.Fatal("levels far from the 90% estimates were accepted")
	}
}

// privateAddress returns the resolvable private address that irk generates
// from prand, most significant byte first.
func privateAddress(irk []byte, prand [3]byte) string {
	block, _ := aes.NewCipher(irk)
	var in, out [16]byte
	copy(in[13:], prand[:])
	block.Encrypt(out[:], in[:])
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", prand[0], prand[1], prand[2], out[13], out[14], out[15])
}

func TestKeysMustResolveTheAdvertisersAddress(t *testing.T) {
	enc := mustHex("00112233445566778899aabbccddeeff")
	irk := mustHex("0f0e0d0c0b0a09080706050403020100")
	other := mustHex("a0a1a2a3a4a5a6a7a8a9aaabacadaeaf")
	address := privateAddress(irk, [3]byte{0x5a, 0x12, 0x34}) // top bits 01: resolvable
	device := encryptedAdvertisement(enc, exactPayload, address)
	for name, test := range map[string]struct {
		irk  []byte
		want bool
	}{"its own IRK": {irk, true}, "its IRK, reversed": {reversed(irk), true}, "another device's IRK": {other, false}} {
		keyring := appleAudioKeyring{Keys: []appleAudioKeys{{IRK: hex.EncodeToString(test.irk), ENC: hex.EncodeToString(enc)}}}
		if _, ok := decryptAdvertisement(device, 0x2420, keyring); ok != test.want {
			t.Fatalf("%s: accepted=%t, want %t", name, ok, test.want)
		}
	}
}

func TestAnUnreadableKeyringIsNotOverwritten(t *testing.T) {
	withAAP(t, "")
	host, child := net.Pipe()
	done := make(chan error, 1)
	go func() {
		defer child.Close()
		done <- protocol.Driver{Manifest: manifest, Inspect: inspect, Get: get, Set: set}.Run(nil, child, child)
	}()
	betty := &fakeBetty{
		t:       t,
		encoder: json.NewEncoder(host),
		decoder: json.NewDecoder(host),
		storage: map[string]string{keyringStorageKey: `{"version":2,"keys":[{"irk":"keep","enc":"keep"}]}`},
		packets: []string{
			"040004000400030401590201020158020108012a0101",
			"04000400310002" + "01001000" + "00112233445566778899aabbccddeeff" + "04001000" + "ffeeddccbbaa99887766554433221100",
		},
	}
	devices := []protocol.Device{{ID: "classic", Name: "AirPods Pro", Connected: true}}
	got := betty.request(protocol.Request{ID: "1", Method: protocol.MethodGet, DeviceID: "classic", Property: "battery", Devices: devices})
	if _, ok := got.Enrichments["classic"].Properties["battery"]; got.Error != "" || !ok {
		t.Fatalf("the battery read must still work: %+v", got)
	}
	if stored := betty.storage[keyringStorageKey]; stored != `{"version":2,"keys":[{"irk":"keep","enc":"keep"}]}` {
		t.Fatalf("the unreadable keyring was overwritten with %s", stored)
	}
	host.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
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

// fakeBetty plays Betty's side of the driver protocol over a pipe. It sends
// requests and answers the driver's host calls with a minimal L2CAP device and
// in-memory driver storage.
type fakeBetty struct {
	t        *testing.T
	encoder  *json.Encoder
	decoder  *json.Decoder
	storage  map[string]string
	packets  []string
	opened   bool
	deniedID string
	calls    []string
}

func (b *fakeBetty) request(request protocol.Request) protocol.Response {
	b.t.Helper()
	request.Protocol, request.Type = protocol.Version, protocol.TypeRequest
	if err := b.encoder.Encode(request); err != nil {
		b.t.Fatal(err)
	}
	for {
		var raw json.RawMessage
		var message protocol.Message
		if err := b.decoder.Decode(&raw); err != nil {
			b.t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &message); err != nil {
			b.t.Fatal(err)
		}
		if message.Type == protocol.TypeResponse {
			var response protocol.Response
			if err := json.Unmarshal(raw, &response); err != nil {
				b.t.Fatal(err)
			}
			return response
		}
		b.calls = append(b.calls, message.Method)
		reply := protocol.Message{Protocol: protocol.Version, Type: protocol.TypeHostResponse, ID: message.ID}
		result, code := b.hostCall(message)
		if code != "" {
			reply.Error, reply.ErrorCode = message.Method+" failed", code
		} else {
			reply.Result, _ = json.Marshal(result)
		}
		if err := b.encoder.Encode(reply); err != nil {
			b.t.Fatal(err)
		}
	}
}

func (b *fakeBetty) hostCall(call protocol.Message) (any, string) {
	switch call.Method {
	case protocol.HostStorageGet:
		var params protocol.StorageGetParams
		_ = json.Unmarshal(call.Params, &params)
		value, found := b.storage[params.Key]
		return map[string]any{"value": value, "found": found}, ""
	case protocol.HostStorageSet:
		var params protocol.StorageSetParams
		_ = json.Unmarshal(call.Params, &params)
		b.storage[params.Key] = params.Value
		return map[string]any{}, ""
	case protocol.HostL2CAPOpen:
		b.opened = true
		var params protocol.L2CAPOpenParams
		_ = json.Unmarshal(call.Params, &params)
		if params.DeviceID == b.deniedID || params.PSM != airPodsPSM {
			return nil, string(protocol.CodePermissionDenied)
		}
		return map[string]string{"channel_id": "c1"}, ""
	case protocol.HostL2CAPWrite:
		var params protocol.L2CAPWriteParams
		_ = json.Unmarshal(call.Params, &params)
		return map[string]int{"written": len(params.Data) / 2}, ""
	case protocol.HostL2CAPRead:
		if len(b.packets) == 0 {
			return nil, string(protocol.CodeTimeout)
		}
		packet := b.packets[0]
		b.packets = b.packets[1:]
		return map[string]string{"data": packet}, ""
	case protocol.HostL2CAPClose:
		return map[string]bool{"closed": true}, ""
	}
	return nil, string(protocol.CodeUnsupported)
}

func TestAppleAudioDriverServesOverProtocol(t *testing.T) {
	withAAP(t, "")
	t.Setenv("BETTY_VALIDATE_APPLE_AUDIO_CONTROLS", "")
	host, child := net.Pipe()
	done := make(chan error, 1)
	go func() {
		defer child.Close()
		done <- protocol.Driver{Manifest: manifest, Inspect: inspect, Get: get, Set: set}.Run(nil, child, child)
	}()
	betty := &fakeBetty{
		t:       t,
		encoder: json.NewEncoder(host),
		decoder: json.NewDecoder(host),
		storage: map[string]string{},
		packets: []string{
			"040004000400030401590201020158020108012a0101",
			"04000400310002" + "01001000" + "00112233445566778899aabbccddeeff" + "04001000" + "ffeeddccbbaa99887766554433221100",
		},
		deniedID: "max",
	}
	devices := []protocol.Device{
		{ID: "classic", Name: "AirPods Pro", Connected: true},
		{ID: "ble", Name: "uuid", ManufacturerData: []protocol.ManufacturerData{{CompanyID: 0x004c, Data: "07190124202b998f110004446263ff09d489000000061e82f24d93"}}},
	}

	inspected := betty.request(protocol.Request{ID: "1", Method: protocol.MethodInspect, Purpose: protocol.PurposeList, Devices: devices})
	if inspected.Error != "" || len(inspected.Enrichments) != 2 || len(inspected.Enrichments["classic"].PossibleMatches) != 1 {
		t.Fatalf("inspect=%+v", inspected)
	}
	if battery := inspected.Enrichments["ble"].Properties["battery"]; !battery.Estimated {
		t.Fatalf("inspect must not read exact battery: %+v", battery)
	}
	for _, call := range betty.calls {
		if call != protocol.HostStorageGet {
			t.Fatalf("inspect made host call %q", call)
		}
	}

	got := betty.request(protocol.Request{ID: "2", Method: protocol.MethodGet, DeviceID: "classic", Property: "battery", Devices: devices})
	battery, ok := got.Enrichments["classic"].Properties["battery"]
	if got.Error != "" || len(got.Enrichments) != 1 || !ok || battery.Estimated || battery.Display != "left 89% · right 88% · case 42% charging" {
		t.Fatalf("get=%+v", got)
	}
	var keyring appleAudioKeyring
	if err := json.Unmarshal([]byte(betty.storage[keyringStorageKey]), &keyring); err != nil || len(keyring.Keys) != 1 {
		t.Fatalf("keyring=%q err=%v", betty.storage[keyringStorageKey], err)
	}
	if keys := keyring.Keys[0]; keys.DeviceID != "classic" || keys.ModelID != 0x2420 || keys.IRK != "00112233445566778899aabbccddeeff" {
		t.Fatalf("keys=%+v", keys)
	}

	denied := []protocol.Device{{ID: "max", Name: "AirPods Max", Connected: true}}
	failed := betty.request(protocol.Request{ID: "3", Method: protocol.MethodGet, DeviceID: "max", Property: "battery", Devices: denied})
	if failed.ErrorCode != string(protocol.CodePermissionDenied) || len(failed.Enrichments) != 0 {
		t.Fatalf("host error code must reach Betty: %+v", failed)
	}

	for i, property := range []string{"battery", "noise_control"} {
		set := betty.request(protocol.Request{ID: fmt.Sprint(4 + i), Method: protocol.MethodSet, DeviceID: "classic", Property: property, Value: json.RawMessage(`"anc"`), Devices: devices})
		if set.Error != "" || len(set.Enrichments) != 0 {
			t.Fatalf("set %s must be left to other drivers: %+v", property, set)
		}
	}

	host.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestNoiseControlWriteWaitsForTheRequestedMode(t *testing.T) {
	withAAP(t, "")
	t.Setenv("BETTY_VALIDATE_APPLE_AUDIO_CONTROLS", "1")
	host, child := net.Pipe()
	done := make(chan error, 1)
	go func() {
		defer child.Close()
		done <- protocol.Driver{Manifest: manifest, Inspect: inspect, Get: get, Set: set}.Run(nil, child, child)
	}()
	betty := &fakeBetty{
		t:       t,
		encoder: json.NewEncoder(host),
		decoder: json.NewDecoder(host),
		storage: map[string]string{},
		// The device reports its old mode, off, before the requested one.
		packets: []string{"0400040009000d01000000", "0400040009000d02000000"},
	}
	devices := []protocol.Device{{ID: "classic", Name: "AirPods Pro", Connected: true}}
	got := betty.request(protocol.Request{ID: "1", Method: protocol.MethodSet, DeviceID: "classic", Property: "noise_control", Value: json.RawMessage(`"anc"`), Devices: devices})
	if property := got.Enrichments["classic"].Properties["noise_control"]; got.Error != "" || string(property.Value) != `"anc"` || !property.Verified {
		t.Fatalf("set=%+v", got)
	}
	host.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// withAAP sets the reason this platform can't open the AAP channel for one
// test; an empty reason makes the channel available.
func withAAP(t *testing.T, unavailable string) {
	t.Helper()
	previous := aapUnavailable
	aapUnavailable = unavailable
	t.Cleanup(func() { aapUnavailable = previous })
}

func TestWithoutAAPNoiseControlIsUnsupportedAndBatteryComesFromAdvertisements(t *testing.T) {
	withAAP(t, "no AAP here")
	t.Setenv("BETTY_VALIDATE_APPLE_AUDIO_CONTROLS", "1")
	host, child := net.Pipe()
	done := make(chan error, 1)
	go func() {
		defer child.Close()
		done <- protocol.Driver{Manifest: manifest, Inspect: inspect, Get: get, Set: set}.Run(nil, child, child)
	}()
	betty := &fakeBetty{t: t, encoder: json.NewEncoder(host), decoder: json.NewDecoder(host), storage: map[string]string{}}
	devices := []protocol.Device{
		{ID: "classic", Name: "AirPods Pro", Connected: true},
		{ID: "ble", ManufacturerData: []protocol.ManufacturerData{{CompanyID: 0x004c, Data: "0719010e20209904000000"}}},
	}
	got := betty.request(protocol.Request{ID: "1", Method: protocol.MethodGet, DeviceID: "classic", Property: "battery", Devices: devices})
	enrichment := got.Enrichments["classic"]
	if battery, ok := enrichment.Properties["battery"]; got.Error != "" || !ok || !battery.Estimated {
		t.Fatalf("get=%+v", got)
	}
	if capability := enrichment.Capabilities["noise_control"]; capability.Status != protocol.StatusUnsupported || capability.Reason != "no AAP here" {
		t.Fatalf("noise_control=%+v", capability)
	}
	if got := betty.request(protocol.Request{ID: "2", Method: protocol.MethodSet, DeviceID: "classic", Property: "noise_control", Value: json.RawMessage(`"anc"`), Devices: devices}); len(got.Enrichments) != 0 {
		t.Fatalf("set=%+v", got)
	}
	if betty.opened {
		t.Fatal("the driver opened an AAP channel")
	}
	host.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
