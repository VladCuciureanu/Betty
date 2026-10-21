//go:build hardware

package l2cap

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// TestNativeChannelLifecycle opens and closes an L2CAP channel to a paired
// Classic device. It uses SDP's PSM 1 by default, which every Classic device
// serves and every platform lets apps open; set BETTY_HARDWARE_L2CAP_PSM to
// try another.
func TestNativeChannelLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows opens Classic L2CAP only through the Apple Audio kernel driver, which serves AirPods' channel alone")
	}
	device := os.Getenv("BETTY_HARDWARE_CLASSIC_ID")
	if device == "" {
		t.Skip("set BETTY_HARDWARE_CLASSIC_ID to an isolated paired device")
	}
	psm := uint64(0x0001)
	if value := os.Getenv("BETTY_HARDWARE_L2CAP_PSM"); value != "" {
		var err error
		if psm, err = strconv.ParseUint(value, 0, 16); err != nil {
			t.Fatalf("BETTY_HARDWARE_L2CAP_PSM=%q: %v", value, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	host := New()
	channel, err := host.OpenL2CAP(ctx, device, uint16(psm))
	if err != nil {
		t.Fatalf("open PSM 0x%04x: %v", psm, err)
	}
	if err := host.CloseL2CAP(context.Background(), channel); err != nil {
		t.Fatalf("close: %v", err)
	}
}
