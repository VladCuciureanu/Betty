//go:build hardware

package l2cap

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestNativeChannelLifecycle(t *testing.T) {
	device := os.Getenv("BETTY_HARDWARE_CLASSIC_ID")
	if device == "" {
		t.Skip("set BETTY_HARDWARE_CLASSIC_ID to an isolated paired device")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	host := New()
	channel, err := host.OpenL2CAP(ctx, device, 0x1001)
	if err != nil {
		t.Fatalf("open PSM 0x1001: %v", err)
	}
	if err := host.CloseL2CAP(context.Background(), channel); err != nil {
		t.Fatalf("close: %v", err)
	}
}
