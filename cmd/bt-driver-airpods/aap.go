package main

import (
	"encoding/hex"
	"fmt"
	"strings"

	protocol "github.com/vladcuciureanu/betty/driver"
)

const airPodsPSM = 0x1001

var (
	aapHandshake            = mustHex("00000400010002000000000000000000")
	aapSetSpecificFeatures  = mustHex("040004004d00d700000000000000")
	aapRequestNotifications = mustHex("040004000f00ffffffffff")
	aapHandshakeACK         = mustHex("01000400")
	aapFeaturesACK          = mustHex("040004002b00")
	aapBatteryHeader        = mustHex("040004000400")
	aapRequestPrivateKeys   = mustHex("0400040030000500")
	aapPrivateKeysHeader    = mustHex("040004003100")
)

type exactBattery struct {
	Headset *batteryComponent `json:"headset,omitempty"`
	Left    *batteryComponent `json:"left,omitempty"`
	Right   *batteryComponent `json:"right,omitempty"`
	Case    *batteryComponent `json:"case,omitempty"`
}

type batteryComponent struct {
	Level    int    `json:"level"`
	Charging bool   `json:"charging"`
	Status   string `json:"status"`
}

func readAAP(session *protocol.Session, deviceID string, requestKeys bool) (exactBattery, *airPodsKeys, error) {
	var opened struct {
		ChannelID string `json:"channel_id"`
	}
	if err := session.CallHost(protocol.HostL2CAPOpen, protocol.L2CAPOpenParams{DeviceID: deviceID, PSM: airPodsPSM}, &opened); err != nil {
		return exactBattery{}, nil, err
	}
	defer session.CallHost(protocol.HostL2CAPClose, protocol.L2CAPChannelParams{ChannelID: opened.ChannelID}, nil)
	if err := writeAAP(session, opened.ChannelID, aapHandshake); err != nil {
		return exactBattery{}, nil, err
	}
	notificationsRequested := false
	keysRequested := false
	var battery *exactBattery
	var acquiredKeys *airPodsKeys
	for attempts := 0; attempts < 12; attempts++ {
		var read struct {
			Data string `json:"data"`
		}
		err := session.CallHost(protocol.HostL2CAPRead, protocol.L2CAPReadParams{ChannelID: opened.ChannelID, MaxBytes: 4096, TimeoutMS: 1500}, &read)
		if err != nil {
			if battery != nil {
				return *battery, nil, nil
			}
			return exactBattery{}, nil, err
		}
		packet, err := hex.DecodeString(read.Data)
		if err != nil {
			return exactBattery{}, nil, fmt.Errorf("host returned invalid L2CAP data: %w", err)
		}
		switch {
		case startsWith(packet, aapHandshakeACK):
			if err := writeAAP(session, opened.ChannelID, aapSetSpecificFeatures); err != nil {
				return exactBattery{}, nil, err
			}
		case startsWith(packet, aapFeaturesACK):
			if err := writeAAP(session, opened.ChannelID, aapRequestNotifications); err != nil {
				return exactBattery{}, nil, err
			}
			notificationsRequested = true
			if requestKeys {
				if err := writeAAP(session, opened.ChannelID, aapRequestPrivateKeys); err != nil {
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
				if err := writeAAP(session, opened.ChannelID, aapRequestPrivateKeys); err != nil {
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
				if err := writeAAP(session, opened.ChannelID, aapRequestNotifications); err != nil {
					return exactBattery{}, nil, err
				}
				notificationsRequested = true
			}
			if requestKeys && !keysRequested {
				if err := writeAAP(session, opened.ChannelID, aapRequestPrivateKeys); err != nil {
					return exactBattery{}, nil, err
				}
				keysRequested = true
			}
		}
	}
	if battery != nil {
		return *battery, acquiredKeys, nil
	}
	return exactBattery{}, nil, fmt.Errorf("AirPods did not report battery state")
}

func parsePrivateKeys(packet []byte) (airPodsKeys, error) {
	if !startsWith(packet, aapPrivateKeysHeader) || len(packet) < 7 {
		return airPodsKeys{}, fmt.Errorf("not an AAP private-keys packet")
	}
	count, offset := int(packet[6]), 7
	var keys airPodsKeys
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

func writeAAP(session *protocol.Session, channelID string, packet []byte) error {
	var result struct {
		Written int `json:"written"`
	}
	if err := session.CallHost(protocol.HostL2CAPWrite, protocol.L2CAPWriteParams{ChannelID: channelID, Data: hex.EncodeToString(packet)}, &result); err != nil {
		return err
	}
	if result.Written != len(packet) {
		return fmt.Errorf("short AAP write: wrote %d of %d bytes", result.Written, len(packet))
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
