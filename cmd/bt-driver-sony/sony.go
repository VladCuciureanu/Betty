package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	protocol "github.com/vladcuciureanu/betty/driver"
)

const (
	sonyServiceV1 = "96cc203e506846adb32de316f5e069ba"
	sonyServiceV2 = "956c7b26d49a4ba8b03fb17d393cb6e2"

	sonyDataACK = byte(0x01)
	sonyDataMDR = byte(0x0c)
)

func sonyNoiseLayoutForModel(model string) (sonyNoiseLayout, bool) {
	model = strings.ToUpper(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(model, "WH-CH720N"):
		return sonyNoiseStandard, true
	case strings.HasPrefix(model, "ULT WEAR") || strings.HasPrefix(model, "WH-ULT900N"):
		return sonyNoiseReservedByte, true
	default:
		return sonyNoiseStandard, false
	}
}

type sonyProtocol int

const (
	protocolUnknown sonyProtocol = iota
	protocolV1
	protocolV2
)

type batteryComponent struct {
	Level    int  `json:"level"`
	Charging bool `json:"charging"`
}

type sonyBattery struct {
	Headset *batteryComponent `json:"headset,omitempty"`
	Left    *batteryComponent `json:"left,omitempty"`
	Right   *batteryComponent `json:"right,omitempty"`
	Case    *batteryComponent `json:"case,omitempty"`
}

type sonyNoiseState struct {
	Mode         string `json:"mode"`
	AmbientLevel *int   `json:"ambient_level,omitempty"`
	VoiceFocus   bool   `json:"voice_focus"`
}

type sonyNoiseLayout int

const (
	sonyNoiseStandard sonyNoiseLayout = iota
	sonyNoiseReservedByte
)

func (b sonyBattery) display() string {
	var values []string
	add := func(name string, component *batteryComponent) {
		if component == nil {
			return
		}
		value := fmt.Sprintf("%s %d%%", name, component.Level)
		if component.Charging {
			value += " charging"
		}
		values = append(values, value)
	}
	add("headset", b.Headset)
	add("left", b.Left)
	add("right", b.Right)
	add("case", b.Case)
	return strings.Join(values, " · ")
}

type sonyFrame struct {
	DataType byte
	Sequence byte
	Payload  []byte
}

type sonyStream struct {
	buffer []byte
}

func encodeSonyFrame(dataType, sequence byte, payload []byte) []byte {
	body := []byte{dataType, sequence, byte(len(payload) >> 24), byte(len(payload) >> 16), byte(len(payload) >> 8), byte(len(payload))}
	body = append(body, payload...)
	var checksum byte
	for _, value := range body {
		checksum += value
	}
	body = append(body, checksum)
	frame := []byte{0x3e}
	for _, value := range body {
		switch value {
		case 0x3c, 0x3d, 0x3e:
			frame = append(frame, 0x3d, value-0x10)
		default:
			frame = append(frame, value)
		}
	}
	return append(frame, 0x3c)
}

func (s *sonyStream) push(data []byte) ([]sonyFrame, error) {
	s.buffer = append(s.buffer, data...)
	var frames []sonyFrame
	for {
		start := bytes.IndexByte(s.buffer, 0x3e)
		if start < 0 {
			s.buffer = nil
			return frames, nil
		}
		if start > 0 {
			s.buffer = s.buffer[start:]
		}
		end := bytes.IndexByte(s.buffer[1:], 0x3c)
		if end < 0 {
			return frames, nil
		}
		end++
		frame, err := decodeSonyBody(s.buffer[1:end])
		s.buffer = s.buffer[end+1:]
		if err != nil {
			return frames, err
		}
		frames = append(frames, frame)
	}
}

func decodeSonyBody(escaped []byte) (sonyFrame, error) {
	body := make([]byte, 0, len(escaped))
	for i := 0; i < len(escaped); i++ {
		if escaped[i] != 0x3d {
			body = append(body, escaped[i])
			continue
		}
		if i+1 >= len(escaped) {
			return sonyFrame{}, fmt.Errorf("truncated Sony escape sequence")
		}
		i++
		switch escaped[i] {
		case 0x2c, 0x2d, 0x2e:
			body = append(body, escaped[i]+0x10)
		default:
			return sonyFrame{}, fmt.Errorf("invalid Sony escape sequence")
		}
	}
	if len(body) < 7 {
		return sonyFrame{}, fmt.Errorf("Sony frame is too short")
	}
	length := int(body[2])<<24 | int(body[3])<<16 | int(body[4])<<8 | int(body[5])
	if length > 65535 || len(body) != 7+length {
		return sonyFrame{}, fmt.Errorf("invalid Sony frame length")
	}
	var checksum byte
	for _, value := range body[:len(body)-1] {
		checksum += value
	}
	if checksum != body[len(body)-1] {
		return sonyFrame{}, fmt.Errorf("invalid Sony frame checksum")
	}
	return sonyFrame{DataType: body[0], Sequence: body[1], Payload: append([]byte(nil), body[6:len(body)-1]...)}, nil
}

type sonySession struct {
	host      *protocol.Session
	channelID string
	sequence  byte
	stream    sonyStream
	pending   []sonyFrame
}

func openSonySession(host *protocol.Session, deviceID string, version sonyProtocol) (*sonySession, error) {
	services := []string{sonyServiceV2, sonyServiceV1}
	if version == protocolV1 {
		services = []string{sonyServiceV1}
	} else if version == protocolV2 {
		services = []string{sonyServiceV2}
	}
	// Report every failed attempt: the first is usually the informative one.
	var failures []error
	for _, service := range services {
		channelID, err := host.OpenRFCOMM(deviceID, service)
		if err == nil {
			return &sonySession{host: host, channelID: channelID}, nil
		}
		failures = append(failures, err)
	}
	return nil, errors.Join(failures...)
}

func (s *sonySession) close() {
	_ = s.host.CloseRFCOMM(s.channelID)
}

func (s *sonySession) send(dataType byte, payload []byte) error {
	frame := encodeSonyFrame(dataType, s.sequence, payload)
	s.sequence ^= 1
	written, err := s.host.WriteRFCOMM(s.channelID, frame)
	if err != nil {
		return err
	}
	if written != len(frame) {
		return fmt.Errorf("short Sony RFCOMM write: wrote %d of %d bytes", written, len(frame))
	}
	return nil
}

func (s *sonySession) receive(timeoutMS int) (sonyFrame, error) {
	if len(s.pending) > 0 {
		frame := s.pending[0]
		s.pending = s.pending[1:]
		return s.accept(frame, timeoutMS)
	}
	for attempts := 0; attempts < 8; attempts++ {
		data, err := s.host.ReadRFCOMM(s.channelID, 4096, time.Duration(timeoutMS)*time.Millisecond)
		if err != nil {
			return sonyFrame{}, err
		}
		frames, err := s.stream.push(data)
		if err != nil {
			return sonyFrame{}, err
		}
		if len(frames) > 0 {
			s.pending = append(s.pending, frames[1:]...)
			return s.accept(frames[0], timeoutMS)
		}
	}
	return sonyFrame{}, fmt.Errorf("Sony device did not return a complete frame")
}

func (s *sonySession) accept(frame sonyFrame, timeoutMS int) (sonyFrame, error) {
	if frame.DataType == sonyDataMDR {
		ackSequence := byte(0)
		if frame.Sequence == 0 {
			ackSequence = 1
		}
		if err := s.sendWithSequence(sonyDataACK, ackSequence, nil); err != nil {
			return sonyFrame{}, err
		}
	}
	if frame.DataType == sonyDataACK {
		return s.receive(timeoutMS)
	}
	return frame, nil
}

func (s *sonySession) sendWithSequence(dataType, sequence byte, payload []byte) error {
	frame := encodeSonyFrame(dataType, sequence, payload)
	written, err := s.host.WriteRFCOMM(s.channelID, frame)
	if err != nil {
		return err
	}
	if written != len(frame) {
		return fmt.Errorf("short Sony RFCOMM write")
	}
	return nil
}

func (s *sonySession) query(payload []byte, opcode, subtype byte) ([]byte, error) {
	if err := s.send(sonyDataMDR, payload); err != nil {
		return nil, err
	}
	for attempts := 0; attempts < 8; attempts++ {
		frame, err := s.receive(1500)
		if err != nil {
			return nil, err
		}
		if len(frame.Payload) >= 2 && frame.Payload[0] == opcode && frame.Payload[1] == subtype {
			return frame.Payload, nil
		}
	}
	return nil, fmt.Errorf("Sony device did not return response 0x%02x/0x%02x", opcode, subtype)
}

func readSonyBattery(host *protocol.Session, deviceID string, version sonyProtocol) (sonyBattery, error) {
	if version == protocolV1 {
		return sonyBattery{}, fmt.Errorf("Sony v1 battery protocol is not verified")
	}
	session, err := openSonySession(host, deviceID, version)
	if err != nil {
		return sonyBattery{}, err
	}
	defer session.close()
	if err := initializeSonySession(session); err != nil {
		return sonyBattery{}, err
	}
	if reply, err := session.query([]byte{0x22, 0x00}, 0x23, 0x00); err == nil {
		return parseSonyBattery(0x00, reply)
	}
	var battery sonyBattery
	if reply, err := session.query([]byte{0x22, 0x09}, 0x23, 0x09); err == nil {
		parsed, parseErr := parseSonyBattery(0x09, reply)
		if parseErr == nil {
			battery.Left, battery.Right = parsed.Left, parsed.Right
		}
	}
	if reply, err := session.query([]byte{0x22, 0x0a}, 0x23, 0x0a); err == nil {
		parsed, parseErr := parseSonyBattery(0x0a, reply)
		if parseErr == nil {
			battery.Case = parsed.Case
		}
	}
	if battery.Left == nil && battery.Right == nil && battery.Case == nil {
		return sonyBattery{}, fmt.Errorf("Sony device did not report battery state")
	}
	return battery, nil
}

func initializeSonySession(session *sonySession) error {
	reply, err := session.query([]byte{0x00, 0x00}, 0x01, 0x00)
	if err != nil {
		return fmt.Errorf("initialize Sony v2 session: %w", err)
	}
	if len(reply) < 2 {
		return fmt.Errorf("initialize Sony v2 session: truncated reply")
	}
	return nil
}

// readSonyNoiseStateV2 reads the second-generation Ambient Sound Control state.
// It remains behind a planned capability until each model layout is verified on
// Betty hardware; keeping the packet engine separate lets that validation use
// the same implementation that will later serve get/set.
func readSonyNoiseStateV2(session *sonySession, layout sonyNoiseLayout) (sonyNoiseState, error) {
	reply, err := session.query([]byte{0x66, 0x17}, 0x67, 0x17)
	if err != nil {
		return sonyNoiseState{}, err
	}
	return parseSonyNoiseStateV2(reply, layout)
}

func parseSonyNoiseStateV2(payload []byte, layout sonyNoiseLayout) (sonyNoiseState, error) {
	if len(payload) < 7 || payload[0] != 0x67 || payload[1] != 0x17 || payload[2] != 0x01 {
		return sonyNoiseState{}, fmt.Errorf("invalid Sony v2 ambient-sound response")
	}
	for _, value := range []byte{payload[3], payload[4]} {
		if value > 1 {
			return sonyNoiseState{}, fmt.Errorf("invalid Sony v2 ambient-sound flags")
		}
	}
	state := sonyNoiseState{Mode: "off"}
	if payload[3] == 1 {
		state.Mode = "anc"
		if payload[4] == 1 {
			state.Mode = "ambient"
		}
	}
	voiceIndex, levelIndex := 5, 6
	if layout == sonyNoiseReservedByte {
		voiceIndex, levelIndex = 6, -1
	}
	if payload[voiceIndex] > 1 || (levelIndex >= 0 && payload[levelIndex] > 20) {
		return sonyNoiseState{}, fmt.Errorf("invalid Sony v2 ambient-sound values")
	}
	state.VoiceFocus = payload[voiceIndex] == 1
	if levelIndex >= 0 {
		level := int(payload[levelIndex])
		if level < 1 && state.Mode == "ambient" {
			return sonyNoiseState{}, fmt.Errorf("invalid Sony v2 ambient level")
		}
		if level >= 1 {
			state.AmbientLevel = &level
		}
	}
	return state, nil
}

func encodeSonyNoiseStateV2(state sonyNoiseState, layout sonyNoiseLayout) ([]byte, error) {
	enabled, ambient := byte(1), byte(0)
	switch state.Mode {
	case "off":
		enabled = 0
	case "anc":
	case "ambient":
		ambient = 1
	default:
		return nil, fmt.Errorf("invalid Sony noise-control mode %q", state.Mode)
	}
	level := byte(1)
	if state.AmbientLevel != nil {
		if *state.AmbientLevel < 1 || *state.AmbientLevel > 20 {
			return nil, fmt.Errorf("Sony ambient level must be between 1 and 20")
		}
		level = byte(*state.AmbientLevel)
	}
	voice := byte(0)
	if state.VoiceFocus {
		voice = 1
	}
	payload := []byte{0x68, 0x17, 0x01, enabled, ambient, voice, level}
	if layout == sonyNoiseReservedByte {
		payload = []byte{0x68, 0x17, 0x01, enabled, ambient, 0x02, voice, 0x00}
	}
	return payload, nil
}

func writeAndVerifySonyNoiseStateV2(session *sonySession, desired sonyNoiseState, layout sonyNoiseLayout) (sonyNoiseState, error) {
	payload, err := encodeSonyNoiseStateV2(desired, layout)
	if err != nil {
		return sonyNoiseState{}, err
	}
	if err := session.send(sonyDataMDR, payload); err != nil {
		return sonyNoiseState{}, err
	}
	actual, err := readSonyNoiseStateV2(session, layout)
	if err != nil {
		return sonyNoiseState{}, fmt.Errorf("verify Sony noise-control write: %w", err)
	}
	if actual.Mode != desired.Mode || actual.VoiceFocus != desired.VoiceFocus || !sameAmbientLevel(actual.AmbientLevel, desired.AmbientLevel, desired.Mode) {
		return actual, fmt.Errorf("Sony device did not apply the requested noise-control state")
	}
	return actual, nil
}

func sameAmbientLevel(actual, desired *int, mode string) bool {
	if mode != "ambient" || desired == nil {
		return true
	}
	return actual != nil && *actual == *desired
}

func parseSonyBattery(subtype byte, payload []byte) (sonyBattery, error) {
	if len(payload) < 2 || payload[0] != 0x23 || payload[1] != subtype {
		return sonyBattery{}, fmt.Errorf("not a Sony battery response")
	}
	component := func(level, charging byte) (*batteryComponent, error) {
		if level > 100 || charging > 1 {
			return nil, fmt.Errorf("invalid Sony battery component")
		}
		return &batteryComponent{Level: int(level), Charging: charging == 1}, nil
	}
	var result sonyBattery
	var err error
	switch subtype {
	case 0x00:
		if len(payload) < 4 {
			return result, fmt.Errorf("truncated Sony headset battery response")
		}
		result.Headset, err = component(payload[2], payload[3])
	case 0x09:
		if len(payload) < 6 {
			return result, fmt.Errorf("truncated Sony earbud battery response")
		}
		if result.Left, err = component(payload[2], payload[3]); err == nil {
			result.Right, err = component(payload[4], payload[5])
		}
	case 0x0a:
		if len(payload) < 4 {
			return result, fmt.Errorf("truncated Sony case battery response")
		}
		result.Case, err = component(payload[2], payload[3])
	default:
		return result, fmt.Errorf("unsupported Sony battery subtype")
	}
	return result, err
}
