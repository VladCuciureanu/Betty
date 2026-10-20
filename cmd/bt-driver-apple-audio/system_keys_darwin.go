//go:build darwin && cgo

package main

/*
#cgo CFLAGS: -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <stdlib.h>
#include "system_keys_darwin.h"
*/
import "C"

import (
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"unsafe"

	protocol "github.com/vladcuciureanu/betty/driver"
)

func systemAppleAudioKeys(device protocol.Device) (appleAudioKeys, error) {
	address := classicAddress(device)
	if address == "" {
		return appleAudioKeys{}, fmt.Errorf("device has no Classic Bluetooth address")
	}
	account := strings.ToUpper(strings.ReplaceAll(address, "-", ":"))
	cAccount := C.CString(account)
	defer C.free(unsafe.Pointer(cAccount))
	var data unsafe.Pointer
	var length C.size_t
	status := int(C.betty_airpods_system_keys(cAccount, &data, &length))
	if status != 0 {
		return appleAudioKeys{}, fmt.Errorf("read MobileBluetooth Keychain item for %s (OSStatus %d)", account, status)
	}
	defer C.free(data)
	values, err := parseMobileBluetoothPlist(C.GoBytes(data, C.int(length)))
	if err != nil {
		return appleAudioKeys{}, err
	}
	irk, err := normalizeSystemKey(values["MagicAccIRK"])
	if err != nil {
		return appleAudioKeys{}, fmt.Errorf("invalid MagicAccIRK: %w", err)
	}
	enc, err := normalizeSystemKey(values["MagicAccEncKey"])
	if err != nil {
		return appleAudioKeys{}, fmt.Errorf("invalid MagicAccEncKey: %w", err)
	}
	return appleAudioKeys{DeviceID: device.ID, IRK: irk, ENC: enc}, nil
}

func classicAddress(device protocol.Device) string {
	for _, endpoint := range device.Endpoints {
		if endpoint.Transport == "classic" || endpoint.Transport == "os" {
			return endpoint.Address
		}
	}
	return ""
}

func parseMobileBluetoothPlist(data []byte) (map[string]string, error) {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	values := map[string]string{}
	key := ""
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse MobileBluetooth Keychain plist: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "key":
			if err := decoder.DecodeElement(&key, &start); err != nil {
				return nil, err
			}
		case "string":
			var value string
			if err := decoder.DecodeElement(&value, &start); err != nil {
				return nil, err
			}
			if key != "" {
				values[key] = value
				key = ""
			}
		}
	}
	return values, nil
}

func normalizeSystemKey(value string) (string, error) {
	value = strings.ReplaceAll(strings.TrimSpace(value), "-", "")
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 16 {
		return "", fmt.Errorf("expected a 128-bit hexadecimal key")
	}
	return hex.EncodeToString(decoded), nil
}
