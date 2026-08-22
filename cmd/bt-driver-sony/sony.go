package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"

	protocol "github.com/vladcuciureanu/betty/driver"
)

const (
	sonyServiceV1 = "96cc203e506846adb32de316f5e069ba"
	sonyServiceV2 = "956c7b26d49a4ba8b03fb17d393cb6e2"

	sonyDataACK = byte(0x01)
	sonyDataMDR = byte(0x0c)
)

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
	var lastErr error
	for _, service := range services {
		var opened struct {
			ChannelID string `json:"channel_id"`
		}
		err := host.CallHost(protocol.HostRFCOMMOpen, protocol.RFCOMMOpenParams{DeviceID: deviceID, ServiceUUID: service}, &opened)
		if err == nil {
			return &sonySession{host: host, channelID: opened.ChannelID}, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (s *sonySession) close() {
	_ = s.host.CallHost(protocol.HostRFCOMMClose, protocol.RFCOMMChannelParams{ChannelID: s.channelID}, nil)
}

func (s *sonySession) send(dataType byte, payload []byte) error {
	frame := encodeSonyFrame(dataType, s.sequence, payload)
	s.sequence ^= 1
	var result struct {
		Written int `json:"written"`
	}
	if err := s.host.CallHost(protocol.HostRFCOMMWrite, protocol.RFCOMMWriteParams{ChannelID: s.channelID, Data: hex.EncodeToString(frame)}, &result); err != nil {
		return err
	}
	if result.Written != len(frame) {
		return fmt.Errorf("short Sony RFCOMM write: wrote %d of %d bytes", result.Written, len(frame))
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
		var result struct {
			Data string `json:"data"`
		}
		if err := s.host.CallHost(protocol.HostRFCOMMRead, protocol.RFCOMMReadParams{ChannelID: s.channelID, MaxBytes: 4096, TimeoutMS: timeoutMS}, &result); err != nil {
			return sonyFrame{}, err
		}
		data, err := hex.DecodeString(result.Data)
		if err != nil {
			return sonyFrame{}, fmt.Errorf("host returned invalid RFCOMM data: %w", err)
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
	var result struct {
		Written int `json:"written"`
	}
	if err := s.host.CallHost(protocol.HostRFCOMMWrite, protocol.RFCOMMWriteParams{ChannelID: s.channelID, Data: hex.EncodeToString(frame)}, &result); err != nil {
		return err
	}
	if result.Written != len(frame) {
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
	if reply, err := session.query([]byte{0x00, 0x00}, 0x01, 0x00); err != nil {
		return sonyBattery{}, fmt.Errorf("initialize Sony v2 session: %w", err)
	} else if len(reply) < 2 {
		return sonyBattery{}, fmt.Errorf("initialize Sony v2 session: truncated reply")
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
