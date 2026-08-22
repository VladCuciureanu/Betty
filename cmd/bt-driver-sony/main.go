package main

import (
	"encoding/json"
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
	if request.Method != "inspect" {
		_ = session.Respond(protocol.Response{ID: request.ID, Error: "unsupported method"})
		return
	}
	response := inspect(session, request.Devices, request.Purpose)
	response.ID = request.ID
	_ = session.Respond(response)
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
			Capabilities:   sonyCapabilities(protocolVersion),
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
	reason := "Sony control reads and writes are not exposed by Betty's capability command grammar yet"
	capabilities := map[string]protocol.Capability{
		"noise_control": {Status: "planned", Readable: true, Writable: true, Values: []string{"off", "anc", "ambient"}, Reason: reason},
		"ambient_level": {Status: "planned", Readable: true, Writable: true, Reason: reason},
		"equalizer":     {Status: "planned", Readable: true, Writable: true, Reason: reason},
	}
	if protocolVersion == protocolV2 || protocolVersion == protocolUnknown {
		capabilities["dsee"] = protocol.Capability{Status: "planned", Readable: true, Writable: true, Values: []string{"off", "on"}, Reason: reason}
		capabilities["speak_to_chat"] = protocol.Capability{Status: "planned", Readable: true, Writable: true, Values: []string{"off", "on"}, Reason: reason}
	}
	return capabilities
}
