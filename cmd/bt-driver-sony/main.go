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
		protocol.PermissionDeviceMetadata,
		protocol.PermissionRFCOMM,
	},
	Match: &protocol.Match{
		ServiceUUIDs: []string{"96cc203e-5068-46ad-b32d-e316f5e069ba", "956c7b26-d49a-4ba8-b03f-b17d393cb6e2"},
		NameContains: []string{"WH-", "WF-", "WI-", "MDR-", "LinkBuds", "ULT WEAR"},
	},
}

func main() {
	protocol.Serve(protocol.Driver{Manifest: manifest, Inspect: inspect, Get: get, Set: set})
}

func get(host *protocol.Session, request protocol.PropertyRequest) (*protocol.Enrichment, error) {
	switch request.Property {
	case "battery":
		return getBattery(host, request.Device)
	case "noise_control", "transparency_level":
		if sonyControlValidationEnabled() {
			return controlProperty(host, request, false)
		}
	}
	return nil, nil
}

func set(host *protocol.Session, request protocol.PropertyRequest) (*protocol.Enrichment, error) {
	switch request.Property {
	case "noise_control", "transparency_level":
		if sonyControlValidationEnabled() {
			return controlProperty(host, request, true)
		}
	}
	return nil, nil
}

func sonyControlValidationEnabled() bool {
	return os.Getenv("BETTY_VALIDATE_SONY_CONTROLS") == "1"
}

func controlProperty(host *protocol.Session, request protocol.PropertyRequest, write bool) (*protocol.Enrichment, error) {
	device := request.Device
	model, protocolVersion, ok := identify(device)
	if !ok {
		return nil, nil
	}
	layout, supported := sonyNoiseLayoutForModel(model)
	if protocolVersion != protocolV2 || !supported {
		return nil, protocol.Errorf(protocol.CodeUnsupported, "Sony noise control is not validated for this model and protocol")
	}
	if !device.Connected {
		return nil, protocol.Errorf(protocol.CodeDeviceDisconnected, "Sony noise control requires a connected device")
	}
	session, err := openSonySession(host, device.ID, protocolVersion)
	if err != nil {
		return nil, protocol.Wrap(err, protocol.CodeBluetoothUnavailable)
	}
	defer session.close()
	if err := initializeSonySession(session); err != nil {
		return nil, protocol.Wrap(err, protocol.CodeBluetoothUnavailable)
	}
	state, err := readSonyNoiseStateV2(session, layout)
	if err != nil {
		return nil, protocol.Wrap(err, protocol.CodeBluetoothUnavailable)
	}
	if write {
		if err := applySonyControlInput(&state, request.Property, request.Value, layout); err != nil {
			return nil, protocol.Errorf(protocol.CodeUsage, "%v", err)
		}
		state, err = writeAndVerifySonyNoiseStateV2(session, state, layout)
		if err != nil {
			return nil, protocol.Wrap(err, protocol.CodeBluetoothUnavailable)
		}
	}
	enrichment := sonyControlEnrichment(device, model, protocolVersion, state)
	if write {
		// writeAndVerifySonyNoiseStateV2 read this state back.
		for name, property := range enrichment.Properties {
			property.Verified = true
			enrichment.Properties[name] = property
		}
	}
	return &enrichment, nil
}

func applySonyControlInput(state *sonyNoiseState, property string, raw json.RawMessage, layout sonyNoiseLayout) error {
	switch property {
	case "noise_control":
		var mode string
		if err := json.Unmarshal(raw, &mode); err != nil {
			return errors.New("noise_control requires off, anc, or transparency")
		}
		if mode != "off" && mode != "anc" && mode != "transparency" {
			return errors.New("noise_control requires off, anc, or transparency")
		}
		state.Mode = mode
		if mode == "transparency" && layout == sonyNoiseStandard && state.AmbientLevel == nil {
			level := 1
			state.AmbientLevel = &level
		}
	case "transparency_level":
		if layout != sonyNoiseStandard {
			return errors.New("transparency_level is unsupported for this model")
		}
		var level int
		if err := json.Unmarshal(raw, &level); err != nil || level < 1 || level > 20 {
			return errors.New("transparency_level requires an integer from 1 through 20")
		}
		state.Mode = "transparency"
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
		properties["transparency_level"] = protocol.Property{Value: level, Display: fmt.Sprintf("%d", *state.AmbientLevel)}
	}
	capabilities := sonyCapabilitiesFor(model, version)
	return protocol.Enrichment{
		Identification: sonyIdentification(device, model),
		Properties:     properties,
		Capabilities:   capabilities,
	}
}

func getBattery(host *protocol.Session, device protocol.Device) (*protocol.Enrichment, error) {
	model, protocolVersion, ok := identify(device)
	if !ok {
		return nil, nil
	}
	if !device.Connected {
		return nil, protocol.Errorf(protocol.CodeDeviceDisconnected, "Sony battery requires a connected device")
	}
	battery, err := readSonyBattery(host, device.ID, protocolVersion)
	if err != nil {
		return nil, protocol.Wrap(err, protocol.CodeBluetoothUnavailable)
	}
	raw, _ := json.Marshal(battery)
	return &protocol.Enrichment{
		Identification: sonyIdentification(device, model),
		Capabilities:   sonyCapabilitiesFor(model, protocolVersion),
		Properties:     map[string]protocol.Property{"battery": {Value: raw, Unit: "%", Display: battery.Display()}},
	}, nil
}

// inspect identifies Sony devices without opening a connection; battery and
// controls are read on demand through get.
func inspect(_ *protocol.Session, request protocol.InspectRequest) (map[string]protocol.Enrichment, error) {
	enrichments := map[string]protocol.Enrichment{}
	for _, device := range request.Devices {
		model, protocolVersion, ok := identify(device)
		if !ok {
			continue
		}
		enrichments[device.ID] = protocol.Enrichment{
			Identification: sonyIdentification(device, model),
			Capabilities:   sonyCapabilitiesFor(model, protocolVersion),
		}
	}
	return enrichments, nil
}

func sonyIdentification(device protocol.Device, model string) *protocol.Identification {
	return &protocol.Identification{Vendor: "Sony", Family: "Headphones", Model: model, Confidence: identificationConfidence(device)}
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
			return protocol.ConfidenceProtocol
		}
	}
	return protocol.ConfidenceName
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
		"battery":            batteryCapability(protocolVersion),
		"noise_control":      {Status: protocol.StatusPlanned, Kind: protocol.KindEnum, Readable: true, Writable: true, Values: []string{"off", "anc", "transparency"}, Reason: reason},
		"transparency_level": {Status: protocol.StatusPlanned, Kind: protocol.KindInteger, Readable: true, Writable: true, Minimum: &minimum, Maximum: &maximum, Step: &step, Reason: reason},
		"equalizer":          {Status: protocol.StatusPlanned, Kind: protocol.KindEnum, Readable: true, Writable: true, Reason: reason},
	}
	if protocolVersion == protocolV2 || protocolVersion == protocolUnknown {
		capabilities["dsee"] = protocol.Capability{Status: protocol.StatusPlanned, Kind: protocol.KindBoolean, Readable: true, Writable: true, Values: []string{"off", "on"}, Reason: reason}
		capabilities["sony.speak_to_chat"] = protocol.Capability{Status: protocol.StatusPlanned, Kind: protocol.KindBoolean, Readable: true, Writable: true, Values: []string{"off", "on"}, Reason: reason}
	}
	// Everything goes over the RFCOMM session, which needs the headphones
	// connected.
	for name, capability := range capabilities {
		capability.RequiresConnection = true
		capabilities[name] = capability
	}
	return capabilities
}

// batteryCapability is available only for protocol v2, the one protocol
// whose battery messages are implemented.
func batteryCapability(protocolVersion sonyProtocol) protocol.Capability {
	battery := protocol.Capability{Status: protocol.StatusAvailable, Kind: protocol.KindBattery, Readable: true}
	switch protocolVersion {
	case protocolV1:
		battery.Status, battery.Reason = protocol.StatusPlanned, "Sony protocol v1 battery messages are not verified"
	case protocolUnknown:
		battery.Status, battery.Reason = protocol.StatusPlanned, "the device does not advertise a Sony control protocol service"
	}
	return battery
}

func sonyCapabilitiesFor(model string, protocolVersion sonyProtocol) map[string]protocol.Capability {
	capabilities := sonyCapabilities(protocolVersion)
	layout, supported := sonyNoiseLayoutForModel(model)
	if !sonyControlValidationEnabled() || protocolVersion != protocolV2 || !supported {
		return capabilities
	}
	noise := capabilities["noise_control"]
	noise.Status, noise.Reason = protocol.StatusAvailable, ""
	capabilities["noise_control"] = noise
	if layout == sonyNoiseStandard {
		ambient := capabilities["transparency_level"]
		ambient.Status, ambient.Reason = protocol.StatusAvailable, ""
		capabilities["transparency_level"] = ambient
	}
	return capabilities
}
