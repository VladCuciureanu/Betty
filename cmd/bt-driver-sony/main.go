package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	protocol "github.com/vladcuciureanu/betty/driver"
)

var version = "dev"

var manifest = protocol.Manifest{
	Protocol: protocol.Version,
	ID:       "official/sony-headphones",
	Name:     "Sony Headphones",
	Version:  version,
	Permissions: []string{
		"device-metadata",
		"rfcomm",
	},
}

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
		if request.Property == "battery" && request.Method == "get" {
			response := getBattery(session, request.Devices, request.DeviceID)
			response.ID = request.ID
			_ = session.Respond(response)
			return
		}
		if !sonyControlValidationEnabled() || (request.Property != "noise_control" && request.Property != "ambient_level") {
			_ = session.Respond(protocol.Response{ID: request.ID, Enrichments: map[string]protocol.Enrichment{}})
			return
		}
		response := controlProperty(session, request)
		response.ID = request.ID
		_ = session.Respond(response)
		return
	}
	response := inspect(session, request.Devices, purpose)
	response.ID = request.ID
	_ = session.Respond(response)
}

func sonyControlValidationEnabled() bool {
	return os.Getenv("BETTY_VALIDATE_SONY_CONTROLS") == "1"
}

func controlProperty(host *protocol.Session, request protocol.Request) protocol.Response {
	response := protocol.Response{Protocol: protocol.Version, Enrichments: map[string]protocol.Enrichment{}}
	for _, device := range request.Devices {
		if device.ID != request.DeviceID {
			continue
		}
		model, protocolVersion, ok := identify(device)
		if !ok {
			return response
		}
		layout, supported := sonyNoiseLayoutForModel(model)
		if protocolVersion != protocolV2 || !supported {
			response.Error = "Sony noise control is not validated for this model and protocol"
			response.ErrorCode = "unsupported"
			return response
		}
		if !device.Connected {
			response.Error = "Sony noise control requires a connected device"
			response.ErrorCode = "device_disconnected"
			return response
		}
		session, err := openSonySession(host, device.ID, protocolVersion)
		if err != nil {
			return sonyControlError(response, err)
		}
		defer session.close()
		if err := initializeSonySession(session); err != nil {
			return sonyControlError(response, err)
		}
		state, err := readSonyNoiseStateV2(session, layout)
		if err != nil {
			return sonyControlError(response, err)
		}
		if request.Method == "set" {
			if err := applySonyControlInput(&state, request.Property, request.Value, layout); err != nil {
				response.Error = err.Error()
				response.ErrorCode = "usage"
				return response
			}
			state, err = writeAndVerifySonyNoiseStateV2(session, state, layout)
			if err != nil {
				return sonyControlError(response, err)
			}
		}
		response.Enrichments[device.ID] = sonyControlEnrichment(device, model, protocolVersion, state)
		return response
	}
	return response
}

func sonyControlError(response protocol.Response, err error) protocol.Response {
	response.Error = err.Error()
	response.ErrorCode = "bluetooth_unavailable"
	var remote *protocol.RemoteError
	if errors.As(err, &remote) && remote.Code != "" {
		response.ErrorCode = remote.Code
		response.Details = remote.Details
	}
	return response
}

func applySonyControlInput(state *sonyNoiseState, property string, raw json.RawMessage, layout sonyNoiseLayout) error {
	switch property {
	case "noise_control":
		var mode string
		if err := json.Unmarshal(raw, &mode); err != nil {
			return errors.New("noise_control requires off, anc, or ambient")
		}
		if mode != "off" && mode != "anc" && mode != "ambient" {
			return errors.New("noise_control requires off, anc, or ambient")
		}
		state.Mode = mode
		if mode == "ambient" && layout == sonyNoiseStandard && state.AmbientLevel == nil {
			level := 1
			state.AmbientLevel = &level
		}
	case "ambient_level":
		if layout != sonyNoiseStandard {
			return errors.New("ambient_level is unsupported for this model")
		}
		var level int
		if err := json.Unmarshal(raw, &level); err != nil || level < 1 || level > 20 {
			return errors.New("ambient_level requires an integer from 1 through 20")
		}
		state.Mode = "ambient"
		state.AmbientLevel = &level
	default:
		return fmt.Errorf("unsupported Sony property %q", property)
	}
	return nil
}

func sonyControlEnrichment(device protocol.Device, model string, version sonyProtocol, state sonyNoiseState) protocol.Enrichment {
	mode, _ := json.Marshal(state.Mode)
	properties := map[string]protocol.Property{
		"noise_control": {Value: mode, Display: state.Mode},
	}
	if state.AmbientLevel != nil {
		level, _ := json.Marshal(*state.AmbientLevel)
		properties["ambient_level"] = protocol.Property{Value: level, Display: fmt.Sprintf("%d", *state.AmbientLevel)}
	}
	capabilities := sonyCapabilitiesFor(model, version)
	return protocol.Enrichment{
		Identification: &protocol.Identification{Vendor: "Sony", Family: "Headphones", Model: model, Confidence: identificationConfidence(device)},
		Properties:     properties,
		Capabilities:   capabilities,
	}
}

func getBattery(session *protocol.Session, devices []protocol.Device, deviceID string) protocol.Response {
	response := protocol.Response{Protocol: protocol.Version, Enrichments: map[string]protocol.Enrichment{}}
	for _, device := range devices {
		if device.ID != deviceID {
			continue
		}
		model, protocolVersion, ok := identify(device)
		if !ok {
			return response
		}
		if !device.Connected {
			response.Error = "Sony battery requires a connected device"
			response.ErrorCode = "device_disconnected"
			return response
		}
		battery, err := readSonyBattery(session, device.ID, protocolVersion)
		if err != nil {
			response.Error = err.Error()
			response.ErrorCode = "bluetooth_unavailable"
			var remote *protocol.RemoteError
			if errors.As(err, &remote) && remote.Code != "" {
				response.ErrorCode = remote.Code
				response.Details = remote.Details
			}
			return response
		}
		raw, _ := json.Marshal(battery)
		response.Enrichments[device.ID] = protocol.Enrichment{
			Identification: &protocol.Identification{Vendor: "Sony", Family: "Headphones", Model: model, Confidence: identificationConfidence(device)},
			Capabilities:   sonyCapabilities(protocolVersion),
			Properties:     map[string]protocol.Property{"battery": {Value: raw, Unit: "%", Display: battery.display()}},
		}
		return response
	}
	return response
}

func inspect(session *protocol.Session, devices []protocol.Device, purpose string) protocol.Response {
	response := protocol.Response{Protocol: protocol.Version, Enrichments: map[string]protocol.Enrichment{}}
	for _, device := range devices {
		model, protocolVersion, ok := identify(device)
		if !ok {
			continue
		}
		enrichment := protocol.Enrichment{
			Identification: &protocol.Identification{Vendor: "Sony", Family: "Headphones", Model: model, Confidence: identificationConfidence(device)},
			Capabilities:   sonyCapabilitiesFor(model, protocolVersion),
		}
		if purpose == "battery" && device.Connected {
			if battery, err := readSonyBattery(session, device.ID, protocolVersion); err == nil {
				raw, _ := json.Marshal(battery)
				enrichment.Properties = map[string]protocol.Property{
					"battery": {Value: raw, Unit: "%", Display: battery.display()},
				}
			}
		}
		response.Enrichments[device.ID] = enrichment
	}
	return response
}

func identify(device protocol.Device) (string, sonyProtocol, bool) {
	protocolVersion := protocolUnknown
	for _, service := range device.Services {
		switch normalizeUUID(service) {
		case sonyServiceV1:
			protocolVersion = protocolV1
		case sonyServiceV2:
			protocolVersion = protocolV2
		}
	}
	upper := strings.ToUpper(strings.TrimSpace(device.Name))
	model := knownSonyModel(upper)
	if protocolVersion != protocolUnknown {
		if model == "" {
			model = strings.TrimSpace(device.Name)
		}
		if model == "" {
			model = "Sony headphones"
		}
		return model, protocolVersion, true
	}
	if model == "" {
		return "", protocolUnknown, false
	}
	return model, protocolUnknown, true
}

func identificationConfidence(device protocol.Device) string {
	for _, service := range device.Services {
		normalized := normalizeUUID(service)
		if normalized == sonyServiceV1 || normalized == sonyServiceV2 {
			return "protocol"
		}
	}
	return "name"
}

func normalizeUUID(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
}

func knownSonyModel(name string) string {
	name = strings.TrimSpace(strings.TrimPrefix(name, "SONY "))
	prefixes := []string{"WH-", "WF-", "WI-", "MDR-", "LINKBUDS", "ULT WEAR"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			return strings.TrimSpace(name)
		}
	}
	return ""
}

func sonyCapabilities(protocolVersion sonyProtocol) map[string]protocol.Capability {
	reason := "Sony control messages are not enabled until current-state reads and hardware-verified writes are implemented"
	minimum, maximum, step := 1.0, 20.0, 1.0
	capabilities := map[string]protocol.Capability{
		"battery":       {Status: "available", Kind: "battery", Readable: true},
		"noise_control": {Status: "planned", Kind: "enum", Readable: true, Writable: true, Values: []string{"off", "anc", "ambient"}, Reason: reason},
		"ambient_level": {Status: "planned", Kind: "integer", Readable: true, Writable: true, Minimum: &minimum, Maximum: &maximum, Step: &step, Reason: reason},
		"equalizer":     {Status: "planned", Kind: "enum", Readable: true, Writable: true, Reason: reason},
	}
	if protocolVersion == protocolV2 || protocolVersion == protocolUnknown {
		capabilities["dsee"] = protocol.Capability{Status: "planned", Kind: "boolean", Readable: true, Writable: true, Values: []string{"off", "on"}, Reason: reason}
		capabilities["sony.speak_to_chat"] = protocol.Capability{Status: "planned", Kind: "boolean", Readable: true, Writable: true, Values: []string{"off", "on"}, Reason: reason}
	}
	return capabilities
}

func sonyCapabilitiesFor(model string, protocolVersion sonyProtocol) map[string]protocol.Capability {
	capabilities := sonyCapabilities(protocolVersion)
	layout, supported := sonyNoiseLayoutForModel(model)
	if !sonyControlValidationEnabled() || protocolVersion != protocolV2 || !supported {
		return capabilities
	}
	noise := capabilities["noise_control"]
	noise.Status, noise.Reason = "available", ""
	capabilities["noise_control"] = noise
	if layout == sonyNoiseStandard {
		ambient := capabilities["ambient_level"]
		ambient.Status, ambient.Reason = "available", ""
		capabilities["ambient_level"] = ambient
	}
	return capabilities
}
