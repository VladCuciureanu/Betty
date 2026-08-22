//go:build hardware && (linux || darwin || windows)

package rfcomm

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestNativeRFCOMMChannelLifecycle(t *testing.T) {
	deviceID := os.Getenv("BETTY_HARDWARE_CLASSIC_ID")
	serviceUUID := os.Getenv("BETTY_HARDWARE_RFCOMM_UUID")
	if deviceID == "" || serviceUUID == "" {
		t.Skip("set BETTY_HARDWARE_CLASSIC_ID and BETTY_HARDWARE_RFCOMM_UUID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	host := New()
	channelID, err := host.OpenRFCOMM(ctx, deviceID, serviceUUID)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.CloseRFCOMM(context.Background(), channelID); err != nil {
		t.Fatal(err)
	}
}
