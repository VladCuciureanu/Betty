package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	protocol "github.com/vladcuciureanu/betty/driver"
)

var version = "dev"

var manifest = protocol.Manifest{Protocol: protocol.Version, ID: "official/airpods", Name: "AirPods", Version: version, Permissions: []string{"device-metadata", "advertisements", "l2cap", "driver-storage"}}

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

type airPodsAdvertisement struct {
	device                   protocol.Device
	identification           protocol.Identification
	left, right, caseBattery *int
	modelID                  uint16
	exact                    *exactBattery
}

func inspect(session *protocol.Session, devices []protocol.Device, purpose string) protocol.Response {
	response := protocol.Response{Protocol: protocol.Version, Enrichments: map[string]protocol.Enrichment{}}
	keyring, _ := loadKeyring(session)
	var ads []airPodsAdvertisement
	var connected []protocol.Device
	for _, device := range devices {
		if device.Connected && strings.Contains(strings.ToLower(device.Name), "airpods") {
			connected = append(connected, device)
		}
		if ad, ok := parseDevice(device); ok {
			ads = append(ads, ad)
		}
	}
	if purpose == "battery" {
		for _, device := range connected {
			if keys, err := systemAirPodsKeys(device); err == nil {
				keyring.Upsert(keys)
				_ = saveKeyring(session, keyring)
			}
		}
	}
	for _, ad := range ads {
		if exact, ok := decryptAdvertisement(ad.device, ad.modelID, keyring); ok {
			ad.exact = &exact
		}
		e := protocol.Enrichment{Identification: &ad.identification, Properties: map[string]protocol.Property{}, Capabilities: airPodsCapabilities()}
		if ad.exact != nil {
			raw, _ := json.Marshal(ad.exact)
			e.Properties["battery"] = protocol.Property{Value: raw, Unit: "%", Display: exactBatteryDisplay(*ad.exact)}
		} else {
			battery := map[string]*int{"left": ad.left, "right": ad.right, "case": ad.caseBattery}
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
		enrichment.Capabilities = airPodsCapabilities()
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
		}
	}
	return response
}

func airPodsCapabilities() map[string]protocol.Capability {
	return map[string]protocol.Capability{
		"noise_control": {
			Status:   "planned",
			Readable: true,
			Writable: true,
			Values:   []string{"off", "anc", "transparency", "adaptive"},
			Reason:   "AirPods AAP noise-control messages are not implemented yet",
		},
	}
}

func parseDevice(device protocol.Device) (airPodsAdvertisement, bool) {
	for _, manufacturer := range device.ManufacturerData {
		if manufacturer.CompanyID != 0x004c {
			continue
		}
		data, err := hex.DecodeString(manufacturer.Data)
		if err != nil || len(data) < 8 || data[0] != 0x07 || data[1] != 0x19 {
			continue
		}
		model := uint16(data[3])<<8 | uint16(data[4])
		name, ok := models[model]
		if !ok {
			name = "AirPods"
		}
		left, right := batteryNibble(data[6]>>4), batteryNibble(data[6]&0x0f)
		if data[5]&0x20 != 0 {
			left, right = right, left
		}
		caseBattery := batteryNibble(data[7] & 0x0f)
		return airPodsAdvertisement{device: device, identification: protocol.Identification{Vendor: "Apple", Family: "AirPods", Model: name, Confidence: "model"}, left: left, right: right, caseBattery: caseBattery, modelID: model}, true
	}
	return airPodsAdvertisement{}, false
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
func batteryDisplay(ad airPodsAdvertisement) string {
	part := func(name string, v *int) string {
		if v == nil {
			return name + " —"
		}
		return fmt.Sprintf("%s ~%d%%", name, *v)
	}
	return strings.Join([]string{part("left", ad.left), part("right", ad.right), part("case", ad.caseBattery)}, " · ")
}

var models = map[uint16]string{0x0220: "AirPods (1st generation)", 0x0f20: "AirPods (2nd generation)", 0x1320: "AirPods (3rd generation)", 0x1920: "AirPods (4th generation)", 0x1b20: "AirPods (4th generation, ANC)", 0x0a20: "AirPods Max", 0x1f20: "AirPods Max (USB-C)", 0x0e20: "AirPods Pro", 0x1420: "AirPods Pro (2nd generation)", 0x2420: "AirPods Pro (2nd generation, USB-C)", 0x2720: "AirPods Pro (3rd generation)"}
