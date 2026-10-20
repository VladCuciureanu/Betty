package main

import (
	"encoding/hex"
	"errors"
	"fmt"
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

// aapSession is one conversation with an Apple audio device over its AAP
// L2CAP channel. It performs the exchange every request starts with: the
// handshake, feature negotiation, and the request for notifications.
type aapSession struct {
	host      *protocol.Session
	channelID string
	ready     bool
}

func openAAP(host *protocol.Session, deviceID string) (*aapSession, error) {
	channelID, err := host.OpenL2CAP(deviceID, airPodsPSM)
	if err != nil {
		return nil, err
	}
	session := &aapSession{host: host, channelID: channelID}
	if err := session.write(aapHandshake); err != nil {
		session.close()
		return nil, err
	}
	return session, nil
}

func (s *aapSession) close() { _ = s.host.CloseL2CAP(s.channelID) }

func (s *aapSession) write(packet []byte) error { return writeAAP(s.host, s.channelID, packet) }

// run reads at most attempts packets. It answers the handshake itself and
// calls onReady once, when the device accepts requests. Every other packet
// goes to onPacket, which returns true to end the session.
func (s *aapSession) run(attempts int, onReady func() error, onPacket func(packet []byte) (bool, error)) error {
	becomeReady := func() error {
		if s.ready {
			return nil
		}
		s.ready = true
		if err := s.write(aapRequestNotifications); err != nil {
			return err
		}
		return onReady()
	}
	for range attempts {
		packet, err := s.host.ReadL2CAP(s.channelID, 4096, aapReadTimeout)
		if err != nil {
			return err
		}
		switch {
		case startsWith(packet, aapHandshakeACK):
			if err := s.write(aapSetSpecificFeatures); err != nil {
				return err
			}
		case startsWith(packet, aapFeaturesACK):
			if err := becomeReady(); err != nil {
				return err
			}
		default:
			done, err := onPacket(packet)
			if done || err != nil {
				return err
			}
			// Some devices skip feature negotiation; ask anyway.
			if err := becomeReady(); err != nil {
				return err
			}
		}
	}
	return errAAPExhausted
}

// errAAPExhausted reports that the device sent the allowed number of packets
// without the one a request was waiting for.
var errAAPExhausted = errors.New("Apple audio device did not answer")

// readAAP reads exact battery state and, when requestKeys is set, the
// device's private keys. A battery reading already received is returned even
// if the session then fails.
func readAAP(host *protocol.Session, deviceID string, requestKeys bool) (exactBattery, *appleAudioKeys, error) {
	session, err := openAAP(host, deviceID)
	if err != nil {
		return exactBattery{}, nil, err
	}
	defer session.close()
	var battery *exactBattery
	var keys *appleAudioKeys
	keysRequested := false
	requestKeysOnce := func() error {
		if !requestKeys || keysRequested {
			return nil
		}
		keysRequested = true
		return session.write(aapRequestPrivateKeys)
	}
	err = session.run(12, requestKeysOnce, func(packet []byte) (bool, error) {
		switch {
		case startsWith(packet, aapBatteryHeader):
			parsed, err := parseExactBattery(packet)
			if err != nil {
				// A corrupt report is skipped: the device sends another.
				return false, nil
			}
			battery = &parsed
			if !requestKeys || keys != nil {
				return true, nil
			}
			return false, requestKeysOnce()
		case startsWith(packet, aapPrivateKeysHeader):
			if parsed, err := parsePrivateKeys(packet); err == nil {
				keys = &parsed
			}
			return battery != nil && keys != nil, nil
		}
		return false, nil
	})
	if battery != nil {
		return *battery, keys, nil
	}
	if errors.Is(err, errAAPExhausted) {
		return exactBattery{}, nil, fmt.Errorf("Apple audio device did not report battery state")
	}
	return exactBattery{}, nil, err
}

// readAAPNoiseControl reads the current noise-control mode or, when
// requested is set, writes it. A write succeeds only once the device reports
// the requested mode back; completing the L2CAP write is not success. Reports
// of another mode after the write are skipped, because the device may report
// its old state before it applies the new one.
func readAAPNoiseControl(host *protocol.Session, deviceID string, requested *string) (string, error) {
	session, err := openAAP(host, deviceID)
	if err != nil {
		return "", err
	}
	defer session.close()
	var command []byte
	if requested != nil {
		if command, err = encodeAAPNoiseControl(*requested); err != nil {
			return "", err
		}
	}
	var result, lastReported string
	writeOnce := func() error {
		if command == nil {
			return nil
		}
		packet := command
		command = nil
		return session.write(packet)
	}
	err = session.run(16, writeOnce, func(packet []byte) (bool, error) {
		mode, ok := parseAAPNoiseControl(packet)
		if !ok {
			return false, nil
		}
		lastReported = mode
		if requested == nil || mode == *requested {
			result = mode
			return true, nil
		}
		return false, nil
	})
	switch {
	case result != "":
		return result, nil
	case errors.Is(err, errAAPExhausted) && requested != nil && lastReported != "":
		return "", fmt.Errorf("Apple audio device kept reporting noise_control %q after %q was requested", lastReported, *requested)
	case errors.Is(err, errAAPExhausted):
		return "", fmt.Errorf("Apple audio device did not report noise-control state")
	default:
		return "", err
	}
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
			single := protocol.SingleBattery(*component)
			battery.Level, battery.Charging, battery.Status = single.Level, single.Charging, single.Status
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
