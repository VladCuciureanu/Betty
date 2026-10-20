//go:build linux

package backend

import (
	"context"
	"fmt"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/vladcuciureanu/betty/internal/domain"
)

func TestTranslateDBus(t *testing.T) {
	cases := map[string]domain.ErrorCode{
		"org.freedesktop.DBus.Error.ServiceUnknown": domain.CodeUnavailable,
		"org.freedesktop.DBus.Error.UnknownObject":  domain.CodeNotFound,
		"org.freedesktop.DBus.Error.AccessDenied":   domain.CodePermission,
		"org.freedesktop.DBus.Error.NoReply":        domain.CodeTimeout,
		"org.bluez.Error.InProgress":                domain.CodeUnavailable,
		"org.bluez.Error.Failed":                    domain.CodeDisconnected,
	}
	for name, want := range cases {
		for _, err := range []error{dbus.NewError(name, []any{"detail"}), *dbus.NewError(name, nil)} {
			if got := domain.AsError(translateDBus(fmt.Errorf("call: %w", err))).Code; got != want {
				t.Errorf("translateDBus(%s %T) = %q, want %q", name, err, got, want)
			}
		}
	}
	if err := translateDBus(context.Canceled); err != context.Canceled {
		t.Errorf("context errors must pass through, got %v", err)
	}
}

func TestDBusNameRecognizesAlreadyExists(t *testing.T) {
	if got := dbusName(dbus.NewError("org.bluez.Error.AlreadyExists", nil)); got != "org.bluez.Error.AlreadyExists" {
		t.Fatalf("dbusName=%q", got)
	}
}
