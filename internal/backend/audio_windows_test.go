//go:build windows

package backend

import "testing"

func TestAudioFilterAddressReadsBluetoothFilters(t *testing.T) {
	cases := map[string]string{
		`{2}.\\?\bthenum#{0000110b-0000-1000-8000-00805f9b34fb}_localmfg&005d#a&ae70f6&0&d8373bc1331a_c00000000#{6994ad04-93ef-11d0-a3cc-00a0c9223196}\src`:              "D8:37:3B:C1:33:1A",
		`{2}.\\?\BTHHFENUM#{0000111e-0000-1000-8000-00805f9b34fb}_VID&0001004c_PID&2002#8&1A2B3C&0&AABBCCDDEEFF_C00000000#{6994ad04-93ef-11d0-a3cc-00a0c9223196}\WaveIn`: "AA:BB:CC:DD:EE:FF",
		`{2}.\\?\hdaudio#func_01&ven_10ec&dev_1220&subsys_1458a0c3&rev_1001#5&32442090&0&0001#{6994ad04-93ef-11d0-a3cc-00a0c9223196}\eheadphonetopo`:                     "",
		`{2}.\\?\root#steamstreamingspeakers#0000#{6994ad04-93ef-11d0-a3cc-00a0c9223196}\wave`:                                                                           "",
	}
	for id, want := range cases {
		if got := audioFilterAddress(id); got != want {
			t.Errorf("audioFilterAddress(%q) = %q, want %q", id, got, want)
		}
	}
}
