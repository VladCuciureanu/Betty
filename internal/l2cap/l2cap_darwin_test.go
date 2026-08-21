//go:build darwin

package l2cap

import (
	"strings"
	"testing"
)

func TestNativeErrorIncludesSymbolAndHexCode(t *testing.T) {
	code := ioReturnError
	err := nativeError("open", int(int32(code)))
	message := err.Error()
	if !strings.Contains(message, "kIOReturnError") || !strings.Contains(message, "0xe00002bc") {
		t.Fatalf("unexpected native error: %s", message)
	}
}
