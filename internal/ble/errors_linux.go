//go:build linux

package ble

import (
	"errors"

	"github.com/godbus/dbus/v5"

	"github.com/vladcuciureanu/betty/internal/bluez"
	"github.com/vladcuciureanu/betty/internal/domain"
)

func platformCode(err error) (domain.ErrorCode, bool) {
	var value dbus.Error
	if errors.As(err, &value) {
		return bluez.Code(value.Name)
	}
	var pointer *dbus.Error
	if errors.As(err, &pointer) {
		return bluez.Code(pointer.Name)
	}
	return "", false
}
