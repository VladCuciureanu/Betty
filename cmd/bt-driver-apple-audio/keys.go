package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

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

// decryptAdvertisement returns the exact battery levels from an encrypted
// Proximity Pairing advertisement, using a key from keyring that verifiably
// belongs to the advertiser.
//
// Decrypting with a wrong key yields noise, and the decrypted format byte
// alone accepts a wrong key about 2 times in 256, which would attach a
// stranger's battery levels to the user's device. So a key is used only when
// two independent checks pass:
//
//   - When the advertiser's address is visible and resolvable (Linux,
//     Windows), it must resolve with the key's IRK: the device generates that
//     address from the same pairing.
//   - The decrypted levels must agree with the 10% estimates that the same
//     advertisement carries unencrypted.
func decryptAdvertisement(device protocol.Device, modelID uint16, keyring appleAudioKeyring) (exactBattery, bool) {
	estimate, ok := parseDevice(device)
	if !ok {
		return exactBattery{}, false
	}
	addresses := resolvableAddresses(device)
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
			if len(addresses) > 0 && keys.IRK != "" && !slices.ContainsFunc(addresses, func(a [6]byte) bool { return resolvesPrivateAddress(keys.IRK, a) }) {
				continue
			}
			plain, err := decryptPrivatePayload(data[11:27], keys.ENC)
			if err != nil || len(plain) < 4 || (plain[0] != 0x04 && plain[0] != 0x14) {
				continue
			}
			if !agreesWithEstimate(plain[1:4], estimate) {
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

// estimateTolerance is how far an exact level may be from the advertised
// estimate, which is a whole multiple of 10%.
const estimateTolerance = 10

// agreesWithEstimate reports whether decrypted component bytes (first pod,
// second pod, case) are valid levels that agree with the advertisement's
// unencrypted estimates. Which pod comes first is not relied on.
func agreesWithEstimate(components []byte, ad appleAudioAdvertisement) bool {
	for _, value := range components {
		if value != 0 && value != 0xff && value&0x7f > 100 {
			return false
		}
	}
	first, second, caseBattery := privateComponent(components[0]), privateComponent(components[1]), privateComponent(components[2])
	asIs, asIsCompared := agree([2]*batteryComponent{first, second}, [2]*int{ad.left, ad.right})
	swapped, swappedCompared := agree([2]*batteryComponent{first, second}, [2]*int{ad.right, ad.left})
	caseAgrees, caseCompared := agree([2]*batteryComponent{caseBattery}, [2]*int{ad.caseBattery})
	pods, podsCompared := asIs, asIsCompared
	if !asIs {
		pods, podsCompared = swapped, swappedCompared
	}
	// Agreement means something only if at least one level was compared.
	return pods && caseAgrees && podsCompared+caseCompared > 0
}

// agree reports whether each exact level is within estimateTolerance of its
// estimate, and how many pairs had both values to compare.
func agree(exact [2]*batteryComponent, estimates [2]*int) (bool, int) {
	compared := 0
	for i := range exact {
		if exact[i] == nil || estimates[i] == nil {
			continue
		}
		compared++
		if difference := exact[i].Level - *estimates[i]; difference < -estimateTolerance || difference > estimateTolerance {
			return false, compared
		}
	}
	return true, compared
}

// resolvableAddresses returns the device's addresses that are resolvable
// private addresses, the kind Proximity Pairing advertisements use.
func resolvableAddresses(device protocol.Device) [][6]byte {
	var addresses [][6]byte
	for _, endpoint := range device.Endpoints {
		if address, ok := parseAddress(endpoint.Address); ok && address[0]>>6 == 0b01 {
			addresses = append(addresses, address)
		}
	}
	return addresses
}

// resolvesPrivateAddress reports whether address, most significant byte
// first, is a resolvable private address generated from the IRK: its low 24
// bits must equal ah(IRK, prand), the AES-128 encryption of its top 24 bits
// (Bluetooth Core, Vol 3, Part H, 2.2.2). Stored IRKs come from sources that
// disagree on byte order, so both orders are tried.
func resolvesPrivateAddress(encodedIRK string, address [6]byte) bool {
	irk, err := hex.DecodeString(encodedIRK)
	if err != nil || len(irk) != 16 {
		return false
	}
	for _, key := range [][]byte{irk, reversed(irk)} {
		block, err := aes.NewCipher(key)
		if err != nil {
			return false
		}
		var in, out [16]byte
		copy(in[13:], address[:3])
		block.Encrypt(out[:], in[:])
		if bytes.Equal(out[13:], address[3:]) {
			return true
		}
	}
	return false
}

func parseAddress(value string) ([6]byte, bool) {
	var address [6]byte
	digits, err := hex.DecodeString(strings.NewReplacer(":", "", "-", "").Replace(value))
	if err != nil || len(digits) != 6 {
		return address, false
	}
	copy(address[:], digits)
	return address, true
}

func reversed(b []byte) []byte {
	out := slices.Clone(b)
	slices.Reverse(out)
	return out
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
