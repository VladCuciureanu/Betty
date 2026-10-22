// Package hidbattery collects battery observations from the operating system,
// mostly its HID stack, and correlates them with Betty devices by stable
// transport identity.
package hidbattery

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	protocol "github.com/vladcuciureanu/betty/driver"
	"github.com/vladcuciureanu/betty/internal/domain"
)

type Observation struct {
	Address string `json:"address"`
	// Percentage is a single battery's level, used when no part is set.
	Percentage int `json:"percentage"`
	// Left, Right and Case are the levels of independently reported parts,
	// such as earbuds and their case. Unknown parts are nil.
	Left   *int   `json:"left,omitempty"`
	Right  *int   `json:"right,omitempty"`
	Case   *int   `json:"case,omitempty"`
	Source string `json:"source"`
}

// value is the battery property for the observation, and false when a
// level is out of range.
func (o Observation) value() (protocol.BatteryValue, bool) {
	valid := func(level int) bool { return level >= 0 && level <= 100 }
	if o.Left == nil && o.Right == nil && o.Case == nil {
		if !valid(o.Percentage) {
			return protocol.BatteryValue{}, false
		}
		return protocol.SingleBattery(protocol.BatteryComponent{Level: o.Percentage}), true
	}
	var value protocol.BatteryValue
	for _, part := range []struct {
		level *int
		into  **protocol.BatteryComponent
	}{{o.Left, &value.Left}, {o.Right, &value.Right}, {o.Case, &value.Case}} {
		if part.level == nil {
			continue
		}
		if !valid(*part.level) {
			return protocol.BatteryValue{}, false
		}
		*part.into = &protocol.BatteryComponent{Level: *part.level}
	}
	return value, true
}

type Provider interface {
	Batteries(context.Context) ([]Observation, error)
}

func New() Provider { return newPlatformProvider() }

// helperArgument, as the only argument, makes bt run as the battery helper
// that Windows needs (see provider_windows.go).
const helperArgument = "__hid-battery-helper"

// helperEnabled is set once ServeHelper has run, so that only bt itself,
// never a test binary, is started as the helper.
var helperEnabled bool

// ServeHelper must be called first in bt's main. When args ask for the
// battery helper it runs it, writing to stdout, and returns true, and bt
// should exit. Otherwise it lets providers start bt as the helper.
func ServeHelper(args []string, stdout io.Writer) bool {
	if len(args) == 1 && args[0] == helperArgument {
		runHelper(stdout)
		return true
	}
	helperEnabled = true
	return false
}

// Apply attaches observations only through an exact normalized endpoint
// address. Display names are deliberately never used for correlation.
func Apply(devices []domain.Device, observations []Observation) []domain.Device {
	byAddress := map[string]Observation{}
	ambiguous := map[string]bool{}
	for _, observation := range observations {
		address := normalizeAddress(observation.Address)
		if _, valid := observation.value(); address == "" || !valid {
			continue
		}
		if _, exists := byAddress[address]; exists {
			ambiguous[address] = true
		}
		byAddress[address] = observation
	}
	for i := range devices {
		if _, exists := devices[i].Properties["battery"]; exists {
			continue
		}
		var observation Observation
		matched := false
		for _, endpoint := range devices[i].Endpoints {
			address := normalizeAddress(endpoint.Address)
			candidate, exists := byAddress[address]
			if !exists || ambiguous[address] {
				continue
			}
			if matched && normalizeAddress(observation.Address) != address {
				matched = false
				break
			}
			observation, matched = candidate, true
		}
		if !matched {
			continue
		}
		if devices[i].Properties == nil {
			devices[i].Properties = map[string]domain.Property{}
		}
		if devices[i].Capabilities == nil {
			devices[i].Capabilities = map[string]domain.DeviceCapability{}
		}
		value, _ := observation.value()
		raw, _ := json.Marshal(value)
		devices[i].Properties["battery"] = domain.Property{Value: raw, Unit: "%", Display: value.Display()}
		devices[i].Capabilities["battery"] = domain.DeviceCapability{Status: "available", Kind: "battery", Readable: true}
	}
	return devices
}

func normalizeAddress(address string) string {
	address = strings.ToLower(strings.TrimSpace(address))
	address = strings.ReplaceAll(address, ":", "")
	address = strings.ReplaceAll(address, "-", "")
	if len(address) != 12 {
		return ""
	}
	for _, value := range address {
		if !strings.ContainsRune("0123456789abcdef", value) {
			return ""
		}
	}
	return address
}
