package main

import (
	"encoding/json"
	"net"
	"strings"
	"testing"

	protocol "github.com/vladcuciureanu/betty/driver"
)

// battery builds an AAP battery packet from 5-byte components, each written
// as "TT 01 LL SS 01": component type, level and status.
func battery(components ...string) []byte {
	packet := append([]byte{}, aapBatteryHeader...)
	packet = append(packet, byte(len(components)))
	for _, component := range components {
		packet = append(packet, mustHex(strings.ReplaceAll(component, " ", ""))...)
	}
	return packet
}

func TestParseExactBatteryFixtures(t *testing.T) {
	cases := []struct {
		name   string
		packet []byte
		ok     bool
		check  func(exactBattery) bool
	}{
		{"one earbud in the case, the other disconnected", battery("04 01 59 02 01", "02 01 00 04 01", "08 01 2a 01 01"), true, func(b exactBattery) bool {
			return b.Left != nil && b.Left.Level == 89 && b.Right == nil && b.Case != nil && b.Case.Charging
		}},
		{"no components", battery(), true, func(b exactBattery) bool { return b.Left == nil && b.Right == nil && b.Case == nil }},
		{"an unknown component type is ignored", battery("10 01 32 02 01", "04 01 59 02 01"), true, func(b exactBattery) bool {
			return b.Left != nil && b.Left.Level == 89
		}},
		{"unknown status", battery("04 01 59 00 01"), true, func(b exactBattery) bool { return b.Left != nil && b.Left.Status == "unknown" }},
		{"level above 100", battery("04 01 65 02 01"), false, nil},
		{"invalid status", battery("04 01 59 03 01"), false, nil},
		{"invalid component marker", battery("04 02 59 02 01"), false, nil},
		{"invalid trailer", battery("04 01 59 02 00"), false, nil},
		{"more than three components", battery("04 01 59 02 01", "02 01 58 02 01", "08 01 2a 01 01", "01 01 10 02 01"), false, nil},
		{"count larger than the packet", append(battery("04 01 59 02 01")[:7:7], 0x04, 0x01), false, nil},
		{"truncated header", aapBatteryHeader[:4], false, nil},
		{"another packet type", mustHex("0400040009000d02000000"), false, nil},
	}
	for _, tc := range cases {
		got, err := parseExactBattery(tc.packet)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err=%v", tc.name, err)
			continue
		}
		if tc.ok && !tc.check(got) {
			t.Errorf("%s: battery=%#v", tc.name, got)
		}
	}
}

func TestParsePrivateKeysFixtures(t *testing.T) {
	irk, enc := "00112233445566778899aabbccddeeff", "ffeeddccbbaa99887766554433221100"
	key := func(kind, value string) string { return kind + "001000" + value }
	cases := []struct {
		name   string
		packet string
		ok     bool
	}{
		{"both keys", "04000400310002" + key("01", irk) + key("04", enc), true},
		{"an unrelated key between them", "04000400310003" + key("01", irk) + key("02", enc) + key("04", enc), true},
		{"ENC missing", "04000400310001" + key("01", irk), false},
		{"a key too short", "04000400310002" + key("01", irk) + "04000800" + enc[:16], false},
		{"value truncated", "04000400310002" + key("01", irk) + "04001000" + enc[:20], false},
		{"key header truncated", "04000400310002" + key("01", irk) + "0400", false},
		{"no keys", "04000400310000", false},
		{"another packet type", "040004000400000000", false},
	}
	for _, tc := range cases {
		keys, err := parsePrivateKeys(mustHex(tc.packet))
		if (err == nil) != tc.ok {
			t.Errorf("%s: keys=%#v err=%v", tc.name, keys, err)
		}
		if tc.ok && (keys.IRK != irk || keys.ENC != enc) {
			t.Errorf("%s: keys=%#v", tc.name, keys)
		}
	}
}

// aapHost runs readAAP's host calls against fakeBetty's L2CAP device, which
// answers reads with packets in order and then times out.
func aapHost(t *testing.T, packets ...string) *protocol.Session {
	t.Helper()
	host, child := net.Pipe()
	t.Cleanup(func() { host.Close(); child.Close() })
	betty := &fakeBetty{t: t, packets: packets}
	go func() {
		decoder, encoder := json.NewDecoder(host), json.NewEncoder(host)
		for {
			var call protocol.Message
			if decoder.Decode(&call) != nil {
				return
			}
			reply := protocol.Message{Protocol: protocol.Version, Type: protocol.TypeHostResponse, ID: call.ID}
			result, code := betty.hostCall(call)
			if code != "" {
				reply.Error, reply.ErrorCode = call.Method+" failed", code
			} else {
				reply.Result, _ = json.Marshal(result)
			}
			if encoder.Encode(reply) != nil {
				return
			}
		}
	}()
	return protocol.NewSession(child, child)
}

func TestReadAAPSkipsUnrelatedNotifications(t *testing.T) {
	session := aapHost(t,
		"01000400",                 // handshake acknowledged
		"040004002b00",             // features acknowledged
		"0400040009000d02000000",   // noise control state
		"04000400ff0000",           // an unknown notification
		"0400040004000104015902",   // a malformed battery packet
		"040004000400010401590201", // the battery report
	)
	battery, keys, err := readAAP(session, "airpods", false)
	if err != nil || keys != nil || battery.Left == nil || battery.Left.Level != 89 {
		t.Fatalf("battery=%#v keys=%v err=%v", battery, keys, err)
	}
}

func TestReadAAPReportsADeviceThatNeverAnswers(t *testing.T) {
	session := aapHost(t, "01000400", "040004002b00")
	if _, _, err := readAAP(session, "airpods", false); err == nil {
		t.Fatal("a device that sent no battery report was read")
	}
}

func TestReadAAPKeepsABatteryReadingWhenKeysNeverArrive(t *testing.T) {
	session := aapHost(t, "01000400", "040004002b00", "040004000400010401590201")
	battery, keys, err := readAAP(session, "airpods", true)
	if err != nil || keys != nil || battery.Left == nil {
		t.Fatalf("battery=%#v keys=%v err=%v", battery, keys, err)
	}
}
