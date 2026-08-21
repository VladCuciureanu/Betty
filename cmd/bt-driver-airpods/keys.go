package main

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"encoding/json"
	"fmt"

	protocol "github.com/vladcuciureanu/betty/driver"
)

const keyringStorageKey = "airpods/keyring-v1"

type airPodsKeys struct {
	DeviceID string `json:"device_id,omitempty"`
	ModelID  uint16 `json:"model_id,omitempty"`
	IRK      string `json:"irk"`
	ENC      string `json:"enc"`
}

type airPodsKeyring struct {
	Version int           `json:"version"`
	Keys    []airPodsKeys `json:"keys"`
}

func (k *airPodsKeyring) Upsert(value airPodsKeys) {
	for i := range k.Keys {
		if k.Keys[i].DeviceID == value.DeviceID || (value.IRK != "" && k.Keys[i].IRK == value.IRK) {
			k.Keys[i] = value
			return
		}
	}
	k.Keys = append(k.Keys, value)
}

func loadKeyring(session *protocol.Session) (airPodsKeyring, error) {
	var result struct {
		Value string `json:"value"`
		Found bool   `json:"found"`
	}
	if err := session.CallHost(protocol.HostStorageGet, protocol.StorageGetParams{Key: keyringStorageKey}, &result); err != nil {
		return airPodsKeyring{Version: 1}, err
	}
	if !result.Found {
		return airPodsKeyring{Version: 1}, nil
	}
	var keyring airPodsKeyring
	if err := json.Unmarshal([]byte(result.Value), &keyring); err != nil {
		return airPodsKeyring{}, err
	}
	if keyring.Version != 1 {
		return airPodsKeyring{}, fmt.Errorf("unsupported AirPods keyring version %d", keyring.Version)
	}
	return keyring, nil
}

func saveKeyring(session *protocol.Session, keyring airPodsKeyring) error {
	keyring.Version = 1
	raw, err := json.Marshal(keyring)
	if err != nil {
		return err
	}
	return session.CallHost(protocol.HostStorageSet, protocol.StorageSetParams{Key: keyringStorageKey, Value: string(raw)}, nil)
}

func decryptAdvertisement(device protocol.Device, modelID uint16, keyring airPodsKeyring) (exactBattery, bool) {
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
