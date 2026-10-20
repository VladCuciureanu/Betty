// Package btsock holds what Betty's Classic transports share: Bluetooth
// address parsing and, on Linux, socket channels and their I/O.
package btsock

import (
	"strconv"
	"strings"

	"github.com/vladcuciureanu/betty/internal/domain"
)

// ParseAddress reads the Bluetooth address in a device ID, after stripping
// prefix, as six bytes in display order. Colons or dashes may separate the
// bytes, or nothing at all.
func ParseAddress(deviceID, prefix string) ([6]byte, error) {
	var out [6]byte
	value := strings.NewReplacer(":", "", "-", "").Replace(strings.TrimPrefix(deviceID, prefix))
	if len(value) != 12 {
		return out, domain.NewError(domain.CodeUsage, "device %q has no Classic Bluetooth address", deviceID)
	}
	for i := range out {
		n, err := strconv.ParseUint(value[2*i:2*i+2], 16, 8)
		if err != nil {
			return out, domain.NewError(domain.CodeUsage, "device %q has an invalid Bluetooth address", deviceID)
		}
		out[i] = byte(n)
	}
	return out, nil
}

// Uint64 returns an address in display order as the 48-bit integer Windows
// uses.
func Uint64(address [6]byte) uint64 {
	var out uint64
	for _, b := range address {
		out = out<<8 | uint64(b)
	}
	return out
}
