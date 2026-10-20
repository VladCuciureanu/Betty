// Package bluez maps BlueZ and D-Bus error names to Betty error codes. Both
// the Linux OS backend and the tinygo BLE client talk to bluetoothd over
// D-Bus, so they share one table.
package bluez

import "github.com/vladcuciureanu/betty/internal/domain"

var codes = map[string]domain.ErrorCode{
	"org.bluez.Error.NotSupported":            domain.CodeUnsupported,
	"org.bluez.Error.NotImplemented":          domain.CodeUnsupported,
	"org.bluez.Error.NotReady":                domain.CodeUnavailable,
	"org.bluez.Error.NotAvailable":            domain.CodeUnavailable,
	"org.bluez.Error.InProgress":              domain.CodeUnavailable,
	"org.bluez.Error.Busy":                    domain.CodeUnavailable,
	"org.bluez.Error.AuthenticationFailed":    domain.CodeAuthentication,
	"org.bluez.Error.AuthenticationCanceled":  domain.CodeAuthentication,
	"org.bluez.Error.AuthenticationRejected":  domain.CodeAuthentication,
	"org.bluez.Error.AuthenticationTimeout":   domain.CodeAuthentication,
	"org.bluez.Error.ConnectionAttemptFailed": domain.CodeDisconnected,
	"org.bluez.Error.NotAuthorized":           domain.CodePermission,
	"org.bluez.Error.NotPermitted":            domain.CodePermission,
	"org.bluez.Error.NotConnected":            domain.CodeDisconnected,
	"org.bluez.Error.DoesNotExist":            domain.CodeNotFound,
	"org.bluez.Error.InvalidArguments":        domain.CodeUsage,
	// bluetoothd reports most link-level failures, such as "Host is down" or
	// "Page Timeout", as a generic Failed: the device could not be reached.
	"org.bluez.Error.Failed": domain.CodeDisconnected,

	"org.freedesktop.DBus.Error.ServiceUnknown": domain.CodeUnavailable,
	"org.freedesktop.DBus.Error.NameHasNoOwner": domain.CodeUnavailable,
	"org.freedesktop.DBus.Error.UnknownObject":  domain.CodeNotFound,
	"org.freedesktop.DBus.Error.AccessDenied":   domain.CodePermission,
	"org.freedesktop.DBus.Error.NoReply":        domain.CodeTimeout,
	"org.freedesktop.DBus.Error.Timeout":        domain.CodeTimeout,
}

// Code returns the Betty error code for a D-Bus error name.
func Code(name string) (domain.ErrorCode, bool) {
	code, ok := codes[name]
	return code, ok
}
