package main

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"encoding/json"
	"fmt"

	protocol "github.com/vladcuciureanu/betty/driver"
)

const keyringStorageKey = "apple-audio/keyring-v1"

type appleAudioKeys struct {
	DeviceID string `json:"device_id,omitempty"`
	ModelID  uint16 `json:"model_id,omitempty"`
	IRK      string `json:"irk"`
	ENC      string `json:"enc"`
}

type appleAudioKeyring struct {
	Version int              `json:"version"`
	Keys    []appleAudioKeys `json:"keys"`
}

func (k *appleAudioKeyring) Upsert(value appleAudioKeys) {
	for i := range k.Keys {
		if k.Keys[i].DeviceID == value.DeviceID || (value.IRK != "" && k.Keys[i].IRK == value.IRK) {
			k.Keys[i] = value
			return
		}
	}
	k.Keys = append(k.Keys, value)
}

func loadKeyring(host *protocol.Session) (appleAudioKeyring, error) {
	keyring, _, err := loadStoredKeyring(host, keyringStorageKey)
	return keyring, err
}

func loadStoredKeyring(host *protocol.Session, key string) (appleAudioKeyring, bool, error) {
	value, found, err := host.GetState(key)
	if err != nil {
		return appleAudioKeyring{Version: 1}, false, err
	}
	if !found {
		return appleAudioKeyring{Version: 1}, false, nil
	}
	var keyring appleAudioKeyring
	if err := json.Unmarshal([]byte(value), &keyring); err != nil {
		return appleAudioKeyring{}, true, err
	}
	if keyring.Version != 1 {
		return appleAudioKeyring{}, true, fmt.Errorf("unsupported Apple audio keyring version %d", keyring.Version)
	}
	return keyring, true, nil
}

func saveKeyring(host *protocol.Session, keyring appleAudioKeyring) error {
	keyring.Version = 1
	raw, err := json.Marshal(keyring)
	if err != nil {
		return err
	}
	return host.SetState(keyringStorageKey, string(raw))
}

func decryptAdvertisement(device protocol.Device, modelID uint16, keyring appleAudioKeyring) (exactBattery, bool) {
	for _, manufacturer := range device.ManufacturerData {
		if manufacturer.CompanyID != 0x004c {
			continue
		}
		data, err := hex.DecodeString(manufacturer.Data)
		if err != nil || len(data) < 27 || data[0] != 0x07 || data[1] != 0x19 {
			continue
		}
		for _, keys := range keyring.Keys {
			if keys.ModelID != 0 && keys.ModelID != modelID {
				continue
			}
			plain, err := decryptPrivatePayload(data[11:27], keys.ENC)
			if err != nil || len(plain) < 4 || (plain[0] != 0x04 && plain[0] != 0x14) {
				continue
			}
			battery := exactBattery{}
			leftFirst := data[5]&0x20 != 0
			first, second := privateComponent(plain[1]), privateComponent(plain[2])
			if plain[1] == 0 || plain[2] == 0 {
				if first != nil {
					battery.Headset = first
				} else {
					battery.Headset = second
				}
			} else if leftFirst {
				battery.Left, battery.Right = first, second
			} else {
				battery.Right, battery.Left = first, second
			}
			battery.Case = privateComponent(plain[3])
			return battery, true
		}
	}
	return exactBattery{}, false
}

func decryptPrivatePayload(payload []byte, encodedKey string) ([]byte, error) {
	key, err := hex.DecodeString(encodedKey)
	if err != nil || len(key) != 16 || len(payload) != 16 {
		return nil, fmt.Errorf("invalid encrypted advertisement or key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(payload))
	cipher.NewCBCDecrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(out, payload)
	return out, nil
}

func privateComponent(value byte) *batteryComponent {
	if value == 0 || value == 0xff {
		return nil
	}
	level := int(value & 0x7f)
	if level > 100 {
		return nil
	}
	charging := value&0x80 != 0
	status := "discharging"
	if charging {
		status = "charging"
	}
	return &batteryComponent{Level: level, Charging: charging, Status: status}
}
