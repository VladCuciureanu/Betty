package driver

import (
	"strings"

	protocol "github.com/vladcuciureanu/betty/driver"
	"github.com/vladcuciureanu/betty/internal/domain"
)

// matches reports whether a driver's match hints accept device. A driver
// without hints accepts every device.
func matches(match *protocol.Match, device domain.Device) bool {
	if match == nil {
		return true
	}
	for _, data := range device.ManufacturerData {
		for _, id := range match.CompanyIDs {
			if data.CompanyID == id {
				return true
			}
		}
	}
	uuids := append([]string(nil), device.Services...)
	for _, data := range device.ServiceData {
		uuids = append(uuids, data.UUID)
	}
	for _, uuid := range uuids {
		for _, want := range match.ServiceUUIDs {
			if fullUUID(uuid) == fullUUID(want) {
				return true
			}
		}
	}
	name := strings.ToLower(device.Name)
	for _, part := range match.NameContains {
		if part != "" && strings.Contains(name, strings.ToLower(part)) {
			return true
		}
	}
	return false
}

// matchesAny reports whether a driver's hints accept any of devices.
func matchesAny(match *protocol.Match, devices []domain.Device) bool {
	for _, device := range devices {
		if matches(match, device) {
			return true
		}
	}
	return match == nil
}

// fullUUID normalizes a 16-bit, 32-bit or 128-bit UUID to 32 lowercase hex
// digits.
func fullUUID(value string) string {
	value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
	switch len(value) {
	case 4:
		return "0000" + value + "00001000800000805f9b34fb"
	case 8:
		return value + "00001000800000805f9b34fb"
	}
	return value
}

// validUUID reports whether value is a 16-bit, 32-bit or 128-bit UUID.
func validUUID(value string) bool {
	full := fullUUID(value)
	if len(full) != 32 {
		return false
	}
	for _, r := range full {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}
