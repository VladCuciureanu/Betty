package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"runtime"
	"slices"
	"strings"

	protocol "github.com/vladcuciureanu/betty/driver"
)

var version = "dev"

var manifest = protocol.Manifest{
	Protocol: protocol.Version,
	ID:       "official/apple-audio",
	Name:     "Apple Audio",
	Version:  version,
	Permissions: []string{
		protocol.PermissionDeviceMetadata,
		"advertisements",
		protocol.PermissionL2CAP,
		protocol.PermissionStorage,
	},
	// Apple's company ID marks Continuity advertisements; names find saved
	// Classic endpoints.
	Match: &protocol.Match{CompanyIDs: []uint16{0x004c}, NameContains: []string{"AirPods", "Beats"}},
}

func main() {
	protocol.Serve(protocol.Driver{Manifest: manifest, Inspect: inspect, Get: get, Set: set})
}

func get(host *protocol.Session, request protocol.PropertyRequest) (*protocol.Enrichment, error) {
	switch request.Property {
	case "battery":
		return getBattery(host, request)
	case "noise_control":
		if aapUnavailable == "" && appleAudioControlValidationEnabled() {
			return controlProperty(host, request, false)
		}
	}
	return nil, nil
}

func set(host *protocol.Session, request protocol.PropertyRequest) (*protocol.Enrichment, error) {
	if request.Property == "noise_control" && aapUnavailable == "" && appleAudioControlValidationEnabled() {
		return controlProperty(host, request, true)
	}
	return nil, nil
}

// aapUnavailable explains why this platform can't open the AAP channel, and is
// empty where it can. Windows exposes Classic L2CAP only to kernel-mode profile
// drivers, and no signed driver for this channel exists.
var aapUnavailable = map[string]string{
	"windows": "Windows exposes Apple's AAP control channel only to kernel-mode drivers, and no signed driver for it exists",
}[runtime.GOOS]

func appleAudioControlValidationEnabled() bool {
	return os.Getenv("BETTY_VALIDATE_APPLE_AUDIO_CONTROLS") == "1"
}

func controlProperty(host *protocol.Session, request protocol.PropertyRequest, write bool) (*protocol.Enrichment, error) {
	device := request.Device
	// A nearby Continuity advertisement is not proof that it belongs to this
	// Classic endpoint. Control the explicitly selected L2CAP device and use
	// only its own name to choose the conservative set of accepted modes.
	profile, ok := connectedAppleAudioProfile(device, nil)
	if !ok || !profile.ANC {
		return nil, nil
	}
	if !device.Connected {
		return nil, protocol.Errorf(protocol.CodeDeviceDisconnected, "Apple audio noise control requires a connected device")
	}
	var requested *string
	if write {
		var mode string
		if err := json.Unmarshal(request.Value, &mode); err != nil || !slices.Contains(appleAudioCapabilities(profile)["noise_control"].Values, mode) {
			return nil, protocol.Errorf(protocol.CodeUsage, "noise_control value is unsupported by this Apple audio model")
		}
		requested = &mode
	}
	mode, err := readAAPNoiseControl(host, device.ID, requested)
	if err != nil {
		return nil, protocol.Wrap(err, protocol.CodeBluetoothUnavailable)
	}
	raw, _ := json.Marshal(mode)
	return &protocol.Enrichment{
		Identification: nameIdentification(profile),
		Capabilities:   connectedAppleAudioCapabilities(device, nil),
		Properties:     map[string]protocol.Property{"noise_control": {Value: raw, Display: mode}},
	}, nil
}

// getBattery reads exact battery state from the requested device over AAP. It
// first refreshes the keyring from the system's pairing records so that
// encrypted advertisements decrypt to exact levels, and it stores the keys the
// device reports. When the AAP read fails, a battery level taken from a
// correlated advertisement is returned instead of the error.
func getBattery(host *protocol.Session, request protocol.PropertyRequest) (*protocol.Enrichment, error) {
	found := classifyDevices(request.Devices)
	keyring, loadErr := loadKeyring(host)
	// A keyring that fails to load is never saved over: the save would erase
	// every key in it. It is still used for this request, and stays empty.
	save := func() {
		if loadErr == nil {
			_ = saveKeyring(host, keyring)
		}
	}
	if loadErr != nil {
		fmt.Fprintf(os.Stderr, "bt-driver-apple-audio: not updating the unreadable keyring: %v\n", loadErr)
	}
	for _, device := range found.connected {
		if keys, err := systemAppleAudioKeys(device); err == nil {
			keyring.Upsert(keys)
			save()
		}
	}
	device := request.Device
	enrichment, ok := enrich(found, keyring)[device.ID]
	if !ok {
		return nil, nil
	}
	if !device.Connected || !isAppleAudioName(device.Name) || aapUnavailable != "" {
		return &enrichment, nil
	}
	battery, keys, err := readAAP(host, device.ID, true)
	if err != nil {
		if _, hasBattery := enrichment.Properties["battery"]; hasBattery {
			return &enrichment, nil
		}
		return nil, protocol.Wrap(err, protocol.CodeBluetoothUnavailable)
	}
	if keys != nil {
		keys.DeviceID = device.ID
		if found.correlated() {
			keys.ModelID = found.ads[0].modelID
		}
		keyring.Upsert(*keys)
		save()
	}
	// A correlated endpoint shares its properties map with the advertisement.
	properties := maps.Clone(enrichment.Properties)
	if properties == nil {
		properties = map[string]protocol.Property{}
	}
	raw, _ := json.Marshal(battery)
	properties["battery"] = protocol.Property{Value: raw, Unit: "%", Display: exactBatteryDisplay(battery)}
	enrichment.Properties = properties
	return &enrichment, nil
}

type appleAudioAdvertisement struct {
	device                   protocol.Device
	identification           protocol.Identification
	left, right, caseBattery *int
	modelID                  uint16
	profile                  appleAudioProfile
	exact                    *exactBattery
}

// appleAudioDevices holds the devices in one request that this driver may
// recognize: Classic endpoints by name and BLE advertisements by model ID.
type appleAudioDevices struct {
	named     []protocol.Device
	connected []protocol.Device
	ads       []appleAudioAdvertisement
}

// correlated reports whether exactly one advertisement and one connected
// endpoint are present, so that each can be offered as the other's possible
// match.
func (d appleAudioDevices) correlated() bool {
	return len(d.ads) == 1 && len(d.connected) == 1
}

func classifyDevices(devices []protocol.Device) appleAudioDevices {
	var found appleAudioDevices
	for _, device := range devices {
		if isAppleAudioName(device.Name) {
			found.named = append(found.named, device)
			if device.Connected {
				found.connected = append(found.connected, device)
			}
		}
		if ad, ok := parseDevice(device); ok {
			found.ads = append(found.ads, ad)
		}
	}
	return found
}

// inspect identifies Apple audio devices and reports advertised battery levels
// without opening a connection; exact battery is read on demand through get.
func inspect(host *protocol.Session, request protocol.InspectRequest) (map[string]protocol.Enrichment, error) {
	keyring, _ := loadKeyring(host)
	return enrich(classifyDevices(request.Devices), keyring), nil
}

func enrich(found appleAudioDevices, keyring appleAudioKeyring) map[string]protocol.Enrichment {
	enrichments := map[string]protocol.Enrichment{}
	for _, ad := range found.ads {
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
		if found.correlated() {
			e.PossibleMatches = []string{found.connected[0].ID}
			other := enrichments[found.connected[0].ID]
			other.PossibleMatches = []string{ad.device.ID}
			other.Properties = e.Properties
			enrichments[found.connected[0].ID] = other
		}
		enrichments[ad.device.ID] = e
	}
	for _, device := range found.named {
		enrichment := enrichments[device.ID]
		// A saved endpoint name can provide a conservative semantic identity.
		// Nearby advertisements remain possible matches, not identity proof.
		if profile, ok := connectedAppleAudioProfile(device, nil); ok {
			enrichment.Identification = nameIdentification(profile)
		}
		enrichment.Capabilities = connectedAppleAudioCapabilities(device, found.ads)
		enrichments[device.ID] = enrichment
	}
	return enrichments
}

func nameIdentification(profile appleAudioProfile) *protocol.Identification {
	return &protocol.Identification{Vendor: profile.Vendor, Family: profile.Family, Model: profile.Name, Confidence: protocol.ConfidenceName}
}

func appleAudioCapabilities(profile appleAudioProfile) map[string]protocol.Capability {
	capabilities := map[string]protocol.Capability{
		"battery": {Status: protocol.StatusAvailable, Kind: protocol.KindBattery, Readable: true},
	}
	if profile.ANC {
		values := []string{"off", "anc", "transparency"}
		if profile.Adaptive {
			values = append(values, "adaptive")
		}
		capability := protocol.Capability{Status: protocol.StatusPlanned, Kind: protocol.KindEnum, Readable: true, Writable: true, Values: values, Reason: "Apple AAP noise control awaits model-specific hardware validation"}
		if aapUnavailable != "" {
			capability.Status, capability.Reason = protocol.StatusUnsupported, aapUnavailable
		}
		capabilities["noise_control"] = capability
	}
	if profile.HeartRate {
		capabilities["heart_rate"] = protocol.Capability{Status: protocol.StatusPlanned, Kind: protocol.KindNumber, Readable: true, Reason: "Powerbeats Pro 2 exposes the standard BLE Heart Rate Service only while fitness-equipment pairing mode is active"}
	}
	return capabilities
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
		return appleAudioAdvertisement{device: device, identification: protocol.Identification{Vendor: profile.Vendor, Family: profile.Family, Model: profile.Name, Confidence: protocol.ConfidenceModel}, left: left, right: right, caseBattery: caseBattery, modelID: model, profile: profile}, true
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

func passiveBattery(ad appleAudioAdvertisement) protocol.BatteryValue {
	component := func(level *int) *protocol.BatteryComponent {
		if level == nil {
			return nil
		}
		return &protocol.BatteryComponent{Level: *level}
	}
	if ad.profile.Dual {
		battery := protocol.BatteryValue{Left: component(ad.left), Right: component(ad.right)}
		if ad.profile.Case {
			battery.Case = component(ad.caseBattery)
		}
		return battery
	}
	headset := ad.left
	if headset == nil {
		headset = ad.right
	}
	return protocol.BatteryValue{Headset: component(headset)}
}

func connectedAppleAudioCapabilities(device protocol.Device, ads []appleAudioAdvertisement) map[string]protocol.Capability {
	profile, _ := connectedAppleAudioProfile(device, ads)
	capabilities := appleAudioCapabilities(profile)
	if capability, ok := capabilities["noise_control"]; ok && aapUnavailable == "" && appleAudioControlValidationEnabled() && device.Connected {
		capability.Status = protocol.StatusAvailable
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
