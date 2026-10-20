package main

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	protocol "github.com/vladcuciureanu/betty/driver"
)

const (
	airPodsPSM     = 0x1001
	aapReadTimeout = 1500 * time.Millisecond
)

var (
	aapHandshake            = mustHex("00000400010002000000000000000000")
	aapSetSpecificFeatures  = mustHex("040004004d00d700000000000000")
	aapRequestNotifications = mustHex("040004000f00ffffffffff")
	aapHandshakeACK         = mustHex("01000400")
	aapFeaturesACK          = mustHex("040004002b00")
	aapBatteryHeader        = mustHex("040004000400")
	aapRequestPrivateKeys   = mustHex("0400040030000500")
	aapPrivateKeysHeader    = mustHex("040004003100")
	aapNoiseControlHeader   = mustHex("0400040009000d")
)

var aapNoiseControlValues = map[string]byte{
	"off":          0x01,
	"anc":          0x02,
	"transparency": 0x03,
	"adaptive":     0x04,
}

type exactBattery = protocol.BatteryValue
type batteryComponent = protocol.BatteryComponent

func readAAP(host *protocol.Session, deviceID string, requestKeys bool) (exactBattery, *appleAudioKeys, error) {
	channelID, err := host.OpenL2CAP(deviceID, airPodsPSM)
	if err != nil {
		return exactBattery{}, nil, err
	}
	defer host.CloseL2CAP(channelID)
	if err := writeAAP(host, channelID, aapHandshake); err != nil {
		return exactBattery{}, nil, err
	}
	notificationsRequested := false
	keysRequested := false
	var battery *exactBattery
	var acquiredKeys *appleAudioKeys
	for attempts := 0; attempts < 12; attempts++ {
		packet, err := host.ReadL2CAP(channelID, 4096, aapReadTimeout)
		if err != nil {
			if battery != nil {
				return *battery, nil, nil
			}
			return exactBattery{}, nil, err
		}
		switch {
		case startsWith(packet, aapHandshakeACK):
			if err := writeAAP(host, channelID, aapSetSpecificFeatures); err != nil {
				return exactBattery{}, nil, err
			}
		case startsWith(packet, aapFeaturesACK):
			if err := writeAAP(host, channelID, aapRequestNotifications); err != nil {
				return exactBattery{}, nil, err
			}
			notificationsRequested = true
			if requestKeys {
				if err := writeAAP(host, channelID, aapRequestPrivateKeys); err != nil {
					return exactBattery{}, nil, err
				}
				keysRequested = true
			}
		case startsWith(packet, aapBatteryHeader):
			parsed, err := parseExactBattery(packet)
			if err != nil {
				return exactBattery{}, nil, err
			}
			battery = &parsed
			if !requestKeys {
				return parsed, nil, nil
			}
			if acquiredKeys != nil {
				return parsed, acquiredKeys, nil
			}
			if !keysRequested {
				if err := writeAAP(host, channelID, aapRequestPrivateKeys); err != nil {
					return exactBattery{}, nil, err
				}
				keysRequested = true
			}
		case startsWith(packet, aapPrivateKeysHeader):
			keys, err := parsePrivateKeys(packet)
			if err == nil {
				acquiredKeys = &keys
				if battery != nil {
					return *battery, acquiredKeys, nil
				}
			}
		default:
			if !notificationsRequested {
				if err := writeAAP(host, channelID, aapRequestNotifications); err != nil {
					return exactBattery{}, nil, err
				}
				notificationsRequested = true
			}
			if requestKeys && !keysRequested {
				if err := writeAAP(host, channelID, aapRequestPrivateKeys); err != nil {
					return exactBattery{}, nil, err
				}
				keysRequested = true
			}
		}
	}
	if battery != nil {
		return *battery, acquiredKeys, nil
	}
	return exactBattery{}, nil, fmt.Errorf("Apple audio device did not report battery state")
}

// readAAPNoiseControl obtains the current state from the device notification
// stream. A write is successful only after the device reports the requested
// state back; completion of the L2CAP write itself is not treated as success.
func readAAPNoiseControl(host *protocol.Session, deviceID string, requested *string) (string, error) {
	channelID, err := host.OpenL2CAP(deviceID, airPodsPSM)
	if err != nil {
		return "", err
	}
	defer host.CloseL2CAP(channelID)
	if err := writeAAP(host, channelID, aapHandshake); err != nil {
		return "", err
	}
	notificationsRequested := false
	controlWritten := false
	for attempts := 0; attempts < 16; attempts++ {
		packet, err := host.ReadL2CAP(channelID, 4096, aapReadTimeout)
		if err != nil {
			return "", err
		}
		switch {
		case startsWith(packet, aapHandshakeACK):
			if err := writeAAP(host, channelID, aapSetSpecificFeatures); err != nil {
				return "", err
			}
		case startsWith(packet, aapFeaturesACK):
			if err := writeAAP(host, channelID, aapRequestNotifications); err != nil {
				return "", err
			}
			notificationsRequested = true
			if requested != nil {
				packet, err := encodeAAPNoiseControl(*requested)
				if err != nil {
					return "", err
				}
				if err := writeAAP(host, channelID, packet); err != nil {
					return "", err
				}
				controlWritten = true
			}
		default:
			if mode, ok := parseAAPNoiseControl(packet); ok {
				if requested == nil || mode == *requested {
					return mode, nil
				}
				if controlWritten {
					return "", fmt.Errorf("Apple audio device reported noise_control %q after requesting %q", mode, *requested)
				}
			}
			if !notificationsRequested {
				if err := writeAAP(host, channelID, aapRequestNotifications); err != nil {
					return "", err
				}
				notificationsRequested = true
			}
		}
	}
	return "", fmt.Errorf("Apple audio device did not report noise-control state")
}

func encodeAAPNoiseControl(mode string) ([]byte, error) {
	value, ok := aapNoiseControlValues[mode]
	if !ok {
		return nil, fmt.Errorf("noise_control requires off, anc, transparency, or adaptive")
	}
	packet := append([]byte{}, aapNoiseControlHeader...)
	return append(packet, value, 0x00, 0x00, 0x00), nil
}

func parseAAPNoiseControl(packet []byte) (string, bool) {
	if len(packet) != 11 || !startsWith(packet, aapNoiseControlHeader) || packet[8] != 0 || packet[9] != 0 || packet[10] != 0 {
		return "", false
	}
	for mode, value := range aapNoiseControlValues {
		if packet[7] == value {
			return mode, true
		}
	}
	return "", false
}

func parsePrivateKeys(packet []byte) (appleAudioKeys, error) {
	if !startsWith(packet, aapPrivateKeysHeader) || len(packet) < 7 {
		return appleAudioKeys{}, fmt.Errorf("not an AAP private-keys packet")
	}
	count, offset := int(packet[6]), 7
	var keys appleAudioKeys
	for i := 0; i < count; i++ {
		if offset+4 > len(packet) {
			return keys, fmt.Errorf("truncated AAP private key")
		}
		kind, length := packet[offset], int(packet[offset+2])
		offset += 4
		if offset+length > len(packet) {
			return keys, fmt.Errorf("truncated AAP private key value")
		}
		value := hex.EncodeToString(packet[offset : offset+length])
		offset += length
		switch kind {
		case 0x01:
			keys.IRK = value
		case 0x04:
			keys.ENC = value
		}
	}
	if len(keys.IRK) != 32 || len(keys.ENC) != 32 {
		return keys, fmt.Errorf("AAP response did not include 128-bit IRK and ENC keys")
	}
	return keys, nil
}

func writeAAP(host *protocol.Session, channelID string, packet []byte) error {
	written, err := host.WriteL2CAP(channelID, packet)
	if err != nil {
		return err
	}
	if written != len(packet) {
		return fmt.Errorf("short AAP write: wrote %d of %d bytes", written, len(packet))
	}
	return nil
}

func parseExactBattery(packet []byte) (exactBattery, error) {
	if !startsWith(packet, aapBatteryHeader) || len(packet) < 7 {
		return exactBattery{}, fmt.Errorf("not an AAP battery packet")
	}
	count := int(packet[6])
	if count > 3 || len(packet) != 7+5*count {
		return exactBattery{}, fmt.Errorf("invalid AAP battery packet length")
	}
	var battery exactBattery
	for i := 0; i < count; i++ {
		offset := 7 + 5*i
		if packet[offset+1] != 0x01 || packet[offset+4] != 0x01 || packet[offset+2] > 100 {
			return exactBattery{}, fmt.Errorf("invalid AAP battery component")
		}
		status := packet[offset+3]
		if status != 0x00 && status != 0x01 && status != 0x02 && status != 0x04 {
			return exactBattery{}, fmt.Errorf("invalid AAP battery status")
		}
		if status == 0x04 {
			continue
		}
		component := &batteryComponent{Level: int(packet[offset+2]), Charging: status == 0x01, Status: batteryStatusName(status)}
		switch packet[offset] {
		case 0x01:
			battery.Headset = component
		case 0x02:
			battery.Right = component
		case 0x04:
			battery.Left = component
		case 0x08:
			battery.Case = component
		}
	}
	return battery, nil
}

func exactBatteryDisplay(battery exactBattery) string {
	var parts []string
	add := func(name string, component *batteryComponent) {
		if component == nil {
			return
		}
		value := fmt.Sprintf("%s %d%%", name, component.Level)
		if component.Charging {
			value += " charging"
		}
		parts = append(parts, value)
	}
	add("headset", battery.Headset)
	add("left", battery.Left)
	add("right", battery.Right)
	add("case", battery.Case)
	return strings.Join(parts, " · ")
}

func batteryStatusName(value byte) string {
	switch value {
	case 0x01:
		return "charging"
	case 0x02:
		return "discharging"
	default:
		return "unknown"
	}
}

func startsWith(value, prefix []byte) bool {
	return len(value) >= len(prefix) && string(value[:len(prefix)]) == string(prefix)
}

func mustHex(value string) []byte {
	decoded, err := hex.DecodeString(value)
	if err != nil {
		panic(err)
	}
	return decoded
}
