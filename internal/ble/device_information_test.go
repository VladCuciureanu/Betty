package ble

import "testing"

func TestDeviceInformationValue(t *testing.T) {
	cases := []struct {
		uuid  string
		raw   []byte
		name  string
		value string
		ok    bool
	}{
		{"2a29", []byte("Acme\x00\x00"), "manufacturer", "Acme", true},
		{"2a26", []byte("1.2.3"), "firmware_version", "1.2.3", true},
		{"2a23", []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}, "system_id", "0102030405060708", true},
		{"2a50", []byte{0x02, 0x6d, 0x04, 0x2b, 0xc5, 0x00, 0x01}, "pnp_id", "usb:046d:c52b:0100", true},
		{"2a50", []byte{0x01, 0x4c, 0x00, 0x02, 0x20, 0x01, 0x00}, "pnp_id", "bluetooth:004c:2002:0001", true},
		{"2a50", []byte{0x03, 0x4c, 0x00, 0x02, 0x20, 0x01, 0x00}, "", "", false}, // unknown vendor ID source
		{"2a50", []byte{0x01, 0x4c}, "", "", false},
		{"2a24", []byte{0x00}, "", "", false}, // empty text
		{"2a19", []byte{0x55}, "", "", false}, // not Device Information
	}
	for _, tc := range cases {
		name, value, ok := deviceInformationValue(tc.uuid, tc.raw)
		if ok != tc.ok || (ok && (name != tc.name || value != tc.value)) {
			t.Errorf("%s %x: %q %q %t", tc.uuid, tc.raw, name, value, ok)
		}
	}
}
