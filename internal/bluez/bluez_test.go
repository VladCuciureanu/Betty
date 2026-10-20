package bluez

import (
	"testing"

	"github.com/vladcuciureanu/betty/internal/domain"
)

func TestCode(t *testing.T) {
	cases := map[string]domain.ErrorCode{
		"org.freedesktop.DBus.Error.ServiceUnknown": domain.CodeUnavailable,
		"org.freedesktop.DBus.Error.UnknownObject":  domain.CodeNotFound,
		"org.freedesktop.DBus.Error.AccessDenied":   domain.CodePermission,
		"org.freedesktop.DBus.Error.NoReply":        domain.CodeTimeout,
		"org.bluez.Error.InProgress":                domain.CodeUnavailable,
		"org.bluez.Error.Failed":                    domain.CodeDisconnected,
		"org.bluez.Error.AuthenticationFailed":      domain.CodeAuthentication,
	}
	for name, want := range cases {
		if got, ok := Code(name); !ok || got != want {
			t.Errorf("Code(%q) = %q, %t; want %q", name, got, ok, want)
		}
	}
	if _, ok := Code("org.example.Unknown"); ok {
		t.Error("unknown names must not map")
	}
}
