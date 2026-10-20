package ble

import (
	"context"
	"errors"
	"strings"

	"tinygo.org/x/bluetooth"

	"github.com/vladcuciureanu/betty/internal/domain"
)

// translate maps a tinygo error to a Betty error code. tinygo exposes ATT
// errors as typed values, BlueZ errors as D-Bus errors on Linux, and almost
// everything else only as text, so the last resort is the message.
func translate(operation string, err error, fallback domain.ErrorCode) error {
	if err == nil {
		return nil
	}
	var typed *domain.Error
	if errors.As(err, &typed) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return domain.NewError(classify(err, fallback), "BLE %s failed: %v", operation, err)
}

func classify(err error, fallback domain.ErrorCode) domain.ErrorCode {
	var att bluetooth.AttributeProtocolError
	if errors.As(err, &att) {
		switch att {
		case bluetooth.ErrAttInsufficientAuthentication, bluetooth.ErrAttInsufficientAuthorization,
			bluetooth.ErrAttInsufficientEncryption, bluetooth.ErrAttInsufficientEncKeySize:
			return domain.CodeAuthentication
		case bluetooth.ErrAttReadNotPermitted, bluetooth.ErrAttWriteNotPermitted, bluetooth.ErrAttRequestNotSupported:
			return domain.CodeUnsupported
		case bluetooth.ErrAttInvalidHandle, bluetooth.ErrAttNotFound:
			return domain.CodeNotFound
		case bluetooth.ErrAttInvalidLength, bluetooth.ErrAttInvalidOffset, bluetooth.ErrAttValueNotAllowed:
			return domain.CodeUsage
		}
		return fallback
	}
	if code, ok := platformCode(err); ok {
		return code
	}
	message := strings.ToLower(err.Error())
	switch {
	case containsAny(message, "timeout", "timed out"):
		return domain.CodeTimeout
	case containsAny(message, "not powered", "adapter not enabled", "not ready"):
		return domain.CodeUnavailable
	case containsAny(message, "not connected", "disconnected", "unreachable"):
		return domain.CodeDisconnected
	case containsAny(message, "access denied", "not permitted", "not authorized", "unauthorized"):
		return domain.CodePermission
	case containsAny(message, "not supported", "not implemented"):
		return domain.CodeUnsupported
	case containsAny(message, "did not find", "could not find"):
		return domain.CodeNotFound
	}
	return fallback
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
