package driver

import (
	"encoding/hex"
	"fmt"
	"time"
)

// Typed wrappers for the host calls a driver may make while it handles a
// request. Each call requires the matching manifest permission, and channel
// and device IDs are scoped to the current request.

// OpenL2CAP opens a Classic L2CAP channel to a device Betty supplied in the
// current request. It requires PermissionL2CAP.
func (s *Session) OpenL2CAP(deviceID string, psm uint16) (string, error) {
	var result struct {
		ChannelID string `json:"channel_id"`
	}
	err := s.CallHost(HostL2CAPOpen, L2CAPOpenParams{DeviceID: deviceID, PSM: psm}, &result)
	return result.ChannelID, err
}

// ReadL2CAP reads at most maxBytes (1 to 65535) from a channel. A zero timeout
// waits for the request deadline; otherwise it must not exceed 30 seconds.
func (s *Session) ReadL2CAP(channelID string, maxBytes int, timeout time.Duration) ([]byte, error) {
	var result struct {
		Data string `json:"data"`
	}
	if err := s.CallHost(HostL2CAPRead, L2CAPReadParams{ChannelID: channelID, MaxBytes: maxBytes, TimeoutMS: milliseconds(timeout)}, &result); err != nil {
		return nil, err
	}
	return decodeHostData(result.Data)
}

// WriteL2CAP writes one packet to a channel and reports the bytes written.
func (s *Session) WriteL2CAP(channelID string, data []byte) (int, error) {
	var result struct {
		Written int `json:"written"`
	}
	err := s.CallHost(HostL2CAPWrite, L2CAPWriteParams{ChannelID: channelID, Data: hex.EncodeToString(data)}, &result)
	return result.Written, err
}

// CloseL2CAP closes a channel. Betty also closes every channel left open when
// the request finishes.
func (s *Session) CloseL2CAP(channelID string) error {
	return s.CallHost(HostL2CAPClose, L2CAPChannelParams{ChannelID: channelID}, nil)
}

// OpenRFCOMM opens an RFCOMM channel to the SDP service with the given UUID on
// a device Betty supplied in the current request. It requires PermissionRFCOMM.
func (s *Session) OpenRFCOMM(deviceID, serviceUUID string) (string, error) {
	var result struct {
		ChannelID string `json:"channel_id"`
	}
	err := s.CallHost(HostRFCOMMOpen, RFCOMMOpenParams{DeviceID: deviceID, ServiceUUID: serviceUUID}, &result)
	return result.ChannelID, err
}

// ReadRFCOMM reads at most maxBytes (1 to 65535) from a channel. A zero timeout
// waits for the request deadline; otherwise it must not exceed 30 seconds.
func (s *Session) ReadRFCOMM(channelID string, maxBytes int, timeout time.Duration) ([]byte, error) {
	var result struct {
		Data string `json:"data"`
	}
	if err := s.CallHost(HostRFCOMMRead, RFCOMMReadParams{ChannelID: channelID, MaxBytes: maxBytes, TimeoutMS: milliseconds(timeout)}, &result); err != nil {
		return nil, err
	}
	return decodeHostData(result.Data)
}

// WriteRFCOMM writes data to a channel and reports the bytes written.
func (s *Session) WriteRFCOMM(channelID string, data []byte) (int, error) {
	var result struct {
		Written int `json:"written"`
	}
	err := s.CallHost(HostRFCOMMWrite, RFCOMMWriteParams{ChannelID: channelID, Data: hex.EncodeToString(data)}, &result)
	return result.Written, err
}

// CloseRFCOMM closes a channel. Betty also closes every channel left open when
// the request finishes.
func (s *Session) CloseRFCOMM(channelID string) error {
	return s.CallHost(HostRFCOMMClose, RFCOMMChannelParams{ChannelID: channelID}, nil)
}

// GetState reads a value from this driver's private persistent state. It
// requires PermissionStorage.
func (s *Session) GetState(key string) (string, bool, error) {
	var result struct {
		Value string `json:"value"`
		Found bool   `json:"found"`
	}
	err := s.CallHost(HostStorageGet, StorageGetParams{Key: key}, &result)
	return result.Value, result.Found, err
}

// SetState stores a value of at most 64 KiB in this driver's private persistent
// state. Keys are 1 to 128 characters from [A-Za-z0-9._/-].
func (s *Session) SetState(key, value string) error {
	return s.CallHost(HostStorageSet, StorageSetParams{Key: key, Value: value}, nil)
}

func milliseconds(timeout time.Duration) int {
	if timeout <= 0 {
		return 0
	}
	return int(max(timeout/time.Millisecond, 1))
}

func decodeHostData(data string) ([]byte, error) {
	decoded, err := hex.DecodeString(data)
	if err != nil {
		return nil, fmt.Errorf("host returned invalid hexadecimal data: %w", err)
	}
	return decoded, nil
}
