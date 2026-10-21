package ble

import (
	"context"
	"strings"
)

// Descriptor is a GATT descriptor of a characteristic.
type Descriptor struct {
	ServiceUUID        string `json:"service_uuid"`
	CharacteristicUUID string `json:"characteristic_uuid"`
	UUID               string `json:"uuid"`
}

// Descriptors lists the descriptors of a characteristic, given as a UUID or
// "service/characteristic".
func (b *TinyGo) Descriptors(ctx context.Context, address, spec string) ([]Descriptor, error) {
	if err := descriptorSupport(); err != nil {
		return nil, err
	}
	conn, err := b.Open(ctx, address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	service, characteristic := splitCharacteristic(spec)
	return platformDescriptors(ctx, address, service, characteristic)
}

// ReadDescriptor reads a descriptor of a characteristic.
func (b *TinyGo) ReadDescriptor(ctx context.Context, address, spec, descriptor string) ([]byte, error) {
	if err := descriptorSupport(); err != nil {
		return nil, err
	}
	conn, err := b.Open(ctx, address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	service, characteristic := splitCharacteristic(spec)
	return platformReadDescriptor(ctx, address, service, characteristic, descriptor)
}

// WriteDescriptor writes a descriptor of a characteristic.
func (b *TinyGo) WriteDescriptor(ctx context.Context, address, spec, descriptor string, value []byte) error {
	if err := descriptorSupport(); err != nil {
		return err
	}
	conn, err := b.Open(ctx, address)
	if err != nil {
		return err
	}
	defer conn.Close()
	service, characteristic := splitCharacteristic(spec)
	return platformWriteDescriptor(ctx, address, service, characteristic, descriptor, value)
}

// normalizeUUID returns a 16-, 32- or 128-bit UUID as 128 bits, lowercase,
// with dashes, the form BlueZ reports.
func normalizeUUID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	compact := strings.ReplaceAll(value, "-", "")
	switch len(compact) {
	case 4:
		compact = "0000" + compact + "00001000800000805f9b34fb"
	case 8:
		compact = compact + "00001000800000805f9b34fb"
	}
	if len(compact) != 32 {
		return value
	}
	return compact[0:8] + "-" + compact[8:12] + "-" + compact[12:16] + "-" + compact[16:20] + "-" + compact[20:]
}
