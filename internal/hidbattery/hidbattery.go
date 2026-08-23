// Package hidbattery collects battery observations from the operating system's
// HID stack and correlates them with Betty devices by stable transport identity.
package hidbattery

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/vladcuciureanu/betty/internal/domain"
)

type Observation struct {
	Address    string `json:"address"`
	Percentage int    `json:"percentage"`
	Source     string `json:"source"`
}

type Provider interface {
	Batteries(context.Context) ([]Observation, error)
}

func New() Provider { return newPlatformProvider() }

// Apply attaches observations only through an exact normalized endpoint
// address. Display names are deliberately never used for correlation.
func Apply(devices []domain.Device, observations []Observation) []domain.Device {
	byAddress := map[string]Observation{}
	ambiguous := map[string]bool{}
	for _, observation := range observations {
		address := normalizeAddress(observation.Address)
		if address == "" || observation.Percentage < 0 || observation.Percentage > 100 {
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
		raw, _ := json.Marshal(observation.Percentage)
		devices[i].Properties["battery"] = domain.Property{Value: raw, Unit: "%", Display: strings.TrimSpace(string(raw)) + "%"}
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
