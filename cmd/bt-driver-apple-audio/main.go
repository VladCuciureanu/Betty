package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	protocol "github.com/vladcuciureanu/betty/driver"
)

var version = "dev"

var manifest = protocol.Manifest{Protocol: protocol.Version, ID: "official/apple-audio", LegacyIDs: []string{"official/airpods"}, Name: "Apple Audio", Version: version, Permissions: []string{"device-metadata", "advertisements", "l2cap", "driver-storage"}}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(manifest)
		return
	}
	session := protocol.NewSession(os.Stdin, os.Stdout)
	request, err := session.Receive()
	if err != nil {
		_ = session.Respond(protocol.Response{Error: err.Error()})
		return
	}
	if request.Protocol != protocol.Version {
		_ = session.Respond(protocol.Response{ID: request.ID, Error: "unsupported driver protocol"})
		return
	}
	if request.Method != "inspect" && request.Method != "get" && request.Method != "set" {
		_ = session.Respond(protocol.Response{ID: request.ID, Error: "unsupported method"})
		return
	}
	purpose := request.Purpose
	if request.Method == "get" || request.Method == "set" {
		if request.Property == "noise_control" && appleAudioControlValidationEnabled() {
			response := controlProperty(session, request)
			response.ID = request.ID
			_ = session.Respond(response)
			return
		}
		if request.Property != "battery" || request.Method == "set" {
			_ = session.Respond(protocol.Response{ID: request.ID, Enrichments: map[string]protocol.Enrichment{}})
			return
		}
		purpose = "battery"
	}
	response, readErrors := inspectWithErrors(session, request.Devices, purpose)
	if request.Method == "get" {
		response.Enrichments = targetEnrichment(response.Enrichments, request.DeviceID)
		if enrichment, ok := response.Enrichments[request.DeviceID]; ok {
			if _, hasBattery := enrichment.Properties["battery"]; !hasBattery && readErrors[request.DeviceID] != nil {
				response.Error = readErrors[request.DeviceID].Error()
				response.ErrorCode = "bluetooth_unavailable"
				var remote *protocol.RemoteError
				if errors.As(readErrors[request.DeviceID], &remote) && remote.Code != "" {
					response.ErrorCode = remote.Code
					response.Details = remote.Details
				}
			}
		}
	}
	response.ID = request.ID
	_ = session.Respond(response)
}

func appleAudioControlValidationEnabled() bool {
	return os.Getenv("BETTY_VALIDATE_APPLE_AUDIO_CONTROLS") == "1"
}

func controlProperty(session *protocol.Session, request protocol.Request) protocol.Response {
	response := protocol.Response{Protocol: protocol.Version, Enrichments: map[string]protocol.Enrichment{}}
	for _, device := range request.Devices {
		if device.ID != request.DeviceID {
			continue
		}
		// A nearby Continuity advertisement is not proof that it belongs to this
		// Classic endpoint. Control the explicitly selected L2CAP device and use
		// only its own name to choose the conservative set of accepted modes.
		profile, ok := connectedAppleAudioProfile(device, nil)
		if !ok || !profile.ANC {
			return response
		}
		if !device.Connected {
			response.Error, response.ErrorCode = "Apple audio noise control requires a connected device", "device_disconnected"
			return response
		}
		var requested *string
		if request.Method == "set" {
			var mode string
			if err := json.Unmarshal(request.Value, &mode); err != nil || !containsString(appleAudioCapabilities(profile)["noise_control"].Values, mode) {
				response.Error, response.ErrorCode = "noise_control value is unsupported by this Apple audio model", "usage"
				return response
			}
			requested = &mode
		}
		mode, err := readAAPNoiseControl(session, device.ID, requested)
		if err != nil {
			response.Error, response.ErrorCode = err.Error(), "bluetooth_unavailable"
			var remote *protocol.RemoteError
			if errors.As(err, &remote) && remote.Code != "" {
				response.ErrorCode, response.Details = remote.Code, remote.Details
			}
			return response
		}
		raw, _ := json.Marshal(mode)
		response.Enrichments[device.ID] = protocol.Enrichment{Identification: &protocol.Identification{Vendor: profile.Vendor, Family: profile.Family, Model: profile.Name, Confidence: "name"}, Capabilities: connectedAppleAudioCapabilities(device, nil), Properties: map[string]protocol.Property{"noise_control": {Value: raw, Display: mode}}}
		return response
	}
	return response
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

type appleAudioAdvertisement struct {
	device                   protocol.Device
	identification           protocol.Identification
	left, right, caseBattery *int
	modelID                  uint16
	profile                  appleAudioProfile
	exact                    *exactBattery
}

func inspect(session *protocol.Session, devices []protocol.Device, purpose string) protocol.Response {
	response, _ := inspectWithErrors(session, devices, purpose)
	return response
}

func inspectWithErrors(session *protocol.Session, devices []protocol.Device, purpose string) (protocol.Response, map[string]error) {
	response := protocol.Response{Protocol: protocol.Version, Enrichments: map[string]protocol.Enrichment{}}
	readErrors := map[string]error{}
	keyring, _ := loadKeyring(session)
	var ads []appleAudioAdvertisement
	var connected []protocol.Device
	for _, device := range devices {
		if device.Connected && isAppleAudioName(device.Name) {
			connected = append(connected, device)
		}
		if ad, ok := parseDevice(device); ok {
			ads = append(ads, ad)
		}
	}
	if purpose == "battery" {
		for _, device := range connected {
			if keys, err := systemAppleAudioKeys(device); err == nil {
				keyring.Upsert(keys)
				_ = saveKeyring(session, keyring)
			}
		}
	}
	for _, ad := range ads {
		if exact, ok := decryptAdvertisement(ad.device, ad.modelID, keyring); ok {
			ad.exact = &exact
		}
		e := protocol.Enrichment{Identification: &ad.identification, Properties: map[string]protocol.Property{}, Capabilities: appleAudioCapabilities(ad.profile)}
		if ad.exact != nil {
			raw, _ := json.Marshal(ad.exact)
			e.Properties["battery"] = protocol.Property{Value: raw, Unit: "%", Display: exactBatteryDisplay(*ad.exact)}
		} else {
			battery := passiveBattery(ad)
			raw, _ := json.Marshal(battery)
			e.Properties["battery"] = protocol.Property{Value: raw, Unit: "%", Display: batteryDisplay(ad), Estimated: true, Resolution: 10}
		}
		if len(ads) == 1 && len(connected) == 1 {
			e.PossibleMatches = []string{connected[0].ID}
			other := response.Enrichments[connected[0].ID]
			other.PossibleMatches = []string{ad.device.ID}
			other.Properties = e.Properties
			response.Enrichments[connected[0].ID] = other
		}
		response.Enrichments[ad.device.ID] = e
	}
	for _, device := range connected {
		enrichment := response.Enrichments[device.ID]
		enrichment.Capabilities = connectedAppleAudioCapabilities(device, ads)
		response.Enrichments[device.ID] = enrichment
		if purpose != "battery" {
			continue
		}
		if battery, keys, err := readAAP(session, device.ID, true); err == nil {
			if keys != nil {
				keys.DeviceID = device.ID
				if len(ads) == 1 && len(connected) == 1 {
					keys.ModelID = ads[0].modelID
				}
				keyring.Upsert(*keys)
				_ = saveKeyring(session, keyring)
			}
			enrichment := response.Enrichments[device.ID]
			if enrichment.Properties == nil {
				enrichment.Properties = map[string]protocol.Property{}
			}
			raw, _ := json.Marshal(battery)
			enrichment.Properties["battery"] = protocol.Property{Value: raw, Unit: "%", Display: exactBatteryDisplay(battery)}
			response.Enrichments[device.ID] = enrichment
		} else {
			readErrors[device.ID] = err
		}
	}
	return response, readErrors
}

func appleAudioCapabilities(profile appleAudioProfile) map[string]protocol.Capability {
	capabilities := map[string]protocol.Capability{
		"battery": {Status: "available", Kind: "battery", Readable: true},
	}
	if profile.ANC {
		values := []string{"off", "anc", "transparency"}
		if profile.Adaptive {
			values = append(values, "adaptive")
		}
		capabilities["noise_control"] = protocol.Capability{Status: "planned", Kind: "enum", Readable: true, Writable: true, Values: values, Reason: "Apple AAP noise control awaits model-specific hardware validation"}
	}
	if profile.HeartRate {
		capabilities["heart_rate"] = protocol.Capability{Status: "planned", Kind: "number", Readable: true, Reason: "Powerbeats Pro 2 exposes the standard BLE Heart Rate Service only while fitness-equipment pairing mode is active"}
	}
	return capabilities
}

func targetEnrichment(enrichments map[string]protocol.Enrichment, deviceID string) map[string]protocol.Enrichment {
	target := map[string]protocol.Enrichment{}
	if enrichment, ok := enrichments[deviceID]; ok {
		target[deviceID] = enrichment
	}
	return target
}

func parseDevice(device protocol.Device) (appleAudioAdvertisement, bool) {
	for _, manufacturer := range device.ManufacturerData {
		if manufacturer.CompanyID != 0x004c {
			continue
		}
		data, err := hex.DecodeString(manufacturer.Data)
		if err != nil || len(data) < 8 || data[0] != 0x07 || data[1] != 0x19 {
			continue
		}
		model := uint16(data[3])<<8 | uint16(data[4])
		profile, ok := appleAudioModels[model]
		if !ok {
			continue
		}
		left, right := batteryNibble(data[6]>>4), batteryNibble(data[6]&0x0f)
		if data[5]&0x20 != 0 {
			left, right = right, left
		}
		caseBattery := batteryNibble(data[7] & 0x0f)
		return appleAudioAdvertisement{device: device, identification: protocol.Identification{Vendor: profile.Vendor, Family: profile.Family, Model: profile.Name, Confidence: "model"}, left: left, right: right, caseBattery: caseBattery, modelID: model, profile: profile}, true
	}
	return appleAudioAdvertisement{}, false
}
func batteryNibble(value byte) *int {
	var level int
	switch {
	case value <= 9:
		level = int(value) * 10
	case value == 10:
		level = 100
	default:
		return nil
	}
	return &level
}
func batteryDisplay(ad appleAudioAdvertisement) string {
	part := func(name string, v *int) string {
		if v == nil {
			return name + " —"
		}
		return fmt.Sprintf("%s ~%d%%", name, *v)
	}
	if !ad.profile.Dual {
		headset := ad.left
		if headset == nil {
			headset = ad.right
		}
		return part("headset", headset)
	}
	parts := []string{part("left", ad.left), part("right", ad.right)}
	if ad.profile.Case {
		parts = append(parts, part("case", ad.caseBattery))
	}
	return strings.Join(parts, " · ")
}

type appleAudioProfile struct {
	Name      string
	Vendor    string
	Family    string
	Dual      bool
	Case      bool
	ANC       bool
	Adaptive  bool
	HeartRate bool
}

func airPods(name string, dual, caseBattery, anc, adaptive bool) appleAudioProfile {
	return appleAudioProfile{Name: name, Vendor: "Apple", Family: "AirPods", Dual: dual, Case: caseBattery, ANC: anc, Adaptive: adaptive}
}

func beats(name string, dual, caseBattery, anc bool) appleAudioProfile {
	return appleAudioProfile{Name: name, Vendor: "Beats", Family: "Beats", Dual: dual, Case: caseBattery, ANC: anc}
}

var appleAudioModels = map[uint16]appleAudioProfile{
	0x0220: airPods("AirPods (1st generation)", true, true, false, false),
	0x0f20: airPods("AirPods (2nd generation)", true, true, false, false),
	0x1320: airPods("AirPods (3rd generation)", true, true, false, false),
	0x1920: airPods("AirPods (4th generation)", true, true, false, false),
	0x1b20: airPods("AirPods (4th generation, ANC)", true, true, true, true),
	0x0a20: airPods("AirPods Max", false, false, true, false),
	0x1f20: airPods("AirPods Max (USB-C)", false, false, true, false),
	0x2d20: airPods("AirPods Max (2nd generation)", false, false, true, true),
	0x0e20: airPods("AirPods Pro", true, true, true, false),
	0x1420: airPods("AirPods Pro (2nd generation)", true, true, true, true),
	0x2420: airPods("AirPods Pro (2nd generation, USB-C)", true, true, true, true),
	0x2720: airPods("AirPods Pro (3rd generation)", true, true, true, true),
	0x0b20: beats("Powerbeats Pro", true, true, false),
	0x1d20: func() appleAudioProfile {
		p := beats("Powerbeats Pro 2", true, true, true)
		p.HeartRate = true
		return p
	}(),
	0x0320: beats("Powerbeats 3", false, false, false),
	0x0d20: beats("Powerbeats 4", false, false, false),
	0x1220: beats("Beats Fit Pro", true, true, true),
	0x0620: beats("Beats Solo 3", false, false, false),
	0x0c20: beats("Beats Solo Pro", false, false, true),
	0x2520: beats("Beats Solo 4", false, false, false),
	0x2620: beats("Beats Solo Buds", true, false, false),
	0x0920: beats("Beats Studio 3", false, false, true),
	0x1120: beats("Beats Studio Buds", true, true, true),
	0x1620: beats("Beats Studio Buds+", true, true, true),
	0x1720: beats("Beats Studio Pro", false, false, true),
	0x0520: beats("BeatsX", false, false, false),
	0x1020: beats("Beats Flex", false, false, false),
	0x2f20: beats("Powerbeats Fit", true, true, true),
}

func isAppleAudioName(name string) bool {
	name = strings.ToLower(name)
	return strings.Contains(name, "airpods") || strings.Contains(name, "beats") || strings.Contains(name, "powerbeats")
}

func passiveBattery(ad appleAudioAdvertisement) map[string]*int {
	if ad.profile.Dual {
		battery := map[string]*int{"left": ad.left, "right": ad.right}
		if ad.profile.Case {
			battery["case"] = ad.caseBattery
		}
		return battery
	}
	headset := ad.left
	if headset == nil {
		headset = ad.right
	}
	return map[string]*int{"headset": headset}
}

func connectedAppleAudioCapabilities(device protocol.Device, ads []appleAudioAdvertisement) map[string]protocol.Capability {
	profile, _ := connectedAppleAudioProfile(device, ads)
	capabilities := appleAudioCapabilities(profile)
	if capability, ok := capabilities["noise_control"]; ok && appleAudioControlValidationEnabled() && device.Connected {
		capability.Status = "available"
		capability.Reason = ""
		capabilities["noise_control"] = capability
	}
	return capabilities
}

func connectedAppleAudioProfile(device protocol.Device, ads []appleAudioAdvertisement) (appleAudioProfile, bool) {
	if len(ads) == 1 {
		return ads[0].profile, true
	}
	name := strings.ToLower(device.Name)
	for _, profile := range appleAudioModels {
		if strings.EqualFold(device.Name, profile.Name) {
			return profile, true
		}
	}
	// Saved endpoints often expose a generation-free display name. These
	// profiles intentionally advertise only modes common to that family; a BLE
	// advertisement is kept as possible_matches and is not used as identity.
	switch name {
	case "airpods pro":
		return airPods("AirPods Pro", true, true, true, false), true
	case "airpods max":
		return airPods("AirPods Max", false, false, true, false), true
	}
	var best appleAudioProfile
	for _, profile := range appleAudioModels {
		if strings.Contains(name, strings.ToLower(profile.Name)) && len(profile.Name) > len(best.Name) {
			best = profile
		}
	}
	return best, best.Name != ""
}
