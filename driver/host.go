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

// OpenHID opens a HID collection of a device Betty supplied in the current
// request, chosen by its top-level usage page and usage (zero for both opens
// the first one Betty can open), and reports the device's USB-style vendor
// and product IDs. It requires PermissionHID.
func (s *Session) OpenHID(deviceID string, usagePage, usage uint16) (HIDCollection, error) {
	var result HIDCollection
	err := s.CallHost(HostHIDOpen, HIDOpenParams{DeviceID: deviceID, UsagePage: usagePage, Usage: usage}, &result)
	return result, err
}

// ReadHID returns the next input report from a collection, starting with its
// report ID. A zero timeout waits for the request deadline; otherwise it must
// not exceed 30 seconds.
func (s *Session) ReadHID(handleID string, timeout time.Duration) ([]byte, error) {
	var result struct {
		Data string `json:"data"`
	}
	if err := s.CallHost(HostHIDRead, HIDReadParams{HandleID: handleID, TimeoutMS: milliseconds(timeout)}, &result); err != nil {
		return nil, err
	}
	return decodeHostData(result.Data)
}

// GetHIDFeature requests a feature report from the device, starting with its
// report ID. The device answers synchronously, and some devices change
// behavior when asked for one, so drivers should request only the reports
// they need.
func (s *Session) GetHIDFeature(handleID string, reportID uint8) ([]byte, error) {
	var result struct {
		Data string `json:"data"`
	}
	if err := s.CallHost(HostHIDGetFeature, HIDFeatureParams{HandleID: handleID, ReportID: reportID}, &result); err != nil {
		return nil, err
	}
	return decodeHostData(result.Data)
}

// CloseHID closes a collection. Betty also closes every collection left open
// when the request finishes.
func (s *Session) CloseHID(handleID string) error {
	return s.CallHost(HostHIDClose, HIDHandleParams{HandleID: handleID}, nil)
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

// GATTServices lists the GATT services and characteristics of a BLE device
// Betty supplied in the current request. It requires PermissionGATT. Betty
// connects on the first GATT call for a device and keeps the connection until
// the request finishes.
func (s *Session) GATTServices(deviceID string) ([]GATTService, error) {
	var result struct {
		Services []GATTService `json:"services"`
	}
	err := s.CallHost(HostGATTServices, GATTServicesParams{DeviceID: deviceID}, &result)
	return result.Services, err
}

// ReadGATT reads a characteristic. serviceUUID may be empty unless the
// characteristic UUID appears in several services.
func (s *Session) ReadGATT(deviceID, serviceUUID, characteristicUUID string) ([]byte, error) {
	var result struct {
		Data string `json:"data"`
	}
	params := GATTCharacteristicParams{DeviceID: deviceID, ServiceUUID: serviceUUID, CharacteristicUUID: characteristicUUID}
	if err := s.CallHost(HostGATTRead, params, &result); err != nil {
		return nil, err
	}
	return decodeHostData(result.Data)
}

// WriteGATT writes a characteristic, with a response unless withoutResponse
// is set.
func (s *Session) WriteGATT(deviceID, serviceUUID, characteristicUUID string, data []byte, withoutResponse bool) error {
	params := GATTWriteParams{
		GATTCharacteristicParams: GATTCharacteristicParams{DeviceID: deviceID, ServiceUUID: serviceUUID, CharacteristicUUID: characteristicUUID},
		Data:                     hex.EncodeToString(data),
		WithoutResponse:          withoutResponse,
	}
	return s.CallHost(HostGATTWrite, params, nil)
}

// SubscribeGATT enables notifications on a characteristic and returns a
// subscription ID for NextGATT. Betty queues up to 64 values per
// subscription and drops the oldest beyond that.
func (s *Session) SubscribeGATT(deviceID, serviceUUID, characteristicUUID string) (string, error) {
	var result struct {
		SubscriptionID string `json:"subscription_id"`
	}
	params := GATTCharacteristicParams{DeviceID: deviceID, ServiceUUID: serviceUUID, CharacteristicUUID: characteristicUUID}
	err := s.CallHost(HostGATTSubscribe, params, &result)
	return result.SubscriptionID, err
}

// NextGATT returns the next value a subscription received. A zero timeout
// waits for the request deadline; otherwise it must not exceed 30 seconds.
func (s *Session) NextGATT(subscriptionID string, timeout time.Duration) ([]byte, error) {
	var result struct {
		Data string `json:"data"`
	}
	if err := s.CallHost(HostGATTNext, GATTNextParams{SubscriptionID: subscriptionID, TimeoutMS: milliseconds(timeout)}, &result); err != nil {
		return nil, err
	}
	return decodeHostData(result.Data)
}

// UnsubscribeGATT stops a subscription. Betty also stops every subscription
// left open when the request finishes.
func (s *Session) UnsubscribeGATT(subscriptionID string) error {
	return s.CallHost(HostGATTUnsubscribe, GATTSubscriptionParams{SubscriptionID: subscriptionID}, nil)
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
