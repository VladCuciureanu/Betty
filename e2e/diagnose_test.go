//go:build e2e

package e2e_test

import (
	"os"
	"os/exec"
	"testing"
)

// TestDiagnose prints what BlueZ and bt see of one scripted sensor. It runs
// only with BETTY_E2E_DIAGNOSE=1.
func TestDiagnose(t *testing.T) {
	if os.Getenv("BETTY_E2E_DIAGNOSE") != "1" || !virtual() {
		t.Skip("set BETTY_E2E_DIAGNOSE=1 on Linux to print diagnostics")
	}
	p := startPeer(t, "sensor:Diagnose Sensor", "speaker:Diagnose Speaker")
	t.Logf("peer: host %s, devices %+v", p.host, p.devices)
	for _, command := range [][]string{
		{"bluetoothctl", "show"},
		{"bluetoothctl", "--timeout", "12", "scan", "on"},
		{"bluetoothctl", "devices"},
		{"busctl", "--system", "tree", "org.bluez"},
	} {
		output, err := exec.Command(command[0], command[1:]...).CombinedOutput()
		t.Logf("$ %v (err=%v)\n%s", command, err, output)
	}
	sensor := p.device(0)
	for _, command := range [][]string{
		{"timeout", "20", "bluetoothctl", "connect", sensor.Address},
		{"bluetoothctl", "info", sensor.Address},
		{"timeout", "5", "bluetoothctl", "disconnect", sensor.Address},
	} {
		output, err := exec.Command(command[0], command[1:]...).CombinedOutput()
		t.Logf("$ %v (err=%v)\n%s", command, err, output)
	}
	for _, args := range [][]string{{"--verbose", "get", sensor.Name, "battery"}} {
		r := bt(t, nil, args...)
		t.Logf("$ bt %v (exit %d)\nstdout: %s\nstderr: %s", args, r.code, r.stdout, r.stderr)
	}
	for _, args := range [][]string{{"--verbose", "scan"}, {"--verbose", "list"}} {
		r := bt(t, nil, args...)
		t.Logf("$ bt %v (exit %d)\nstdout: %s\nstderr: %s", args, r.code, r.stdout, r.stderr)
	}
	for _, command := range []string{
		`grep -iE "device_found|inquiry|discover|eir|create_device|device_create|ignor|invalid" /tmp/bluetoothd.log | tail -n 80`,
		`grep -nE "Inquiry|Extended Inquiry Result|Advertising Report|Device Found|Scan Enable|Command Status|Status: [^S]" /tmp/btmon.log | tail -n 60`,
		`grep -n -A6 -E "LE (Extended )?Create Connection \(|Connection Complete|Disconnect Complete|Disconnect \(" /tmp/btmon.log | tail -n 80`,
		`grep -iE "connect|gatt|att|le_conn|error|fail" /tmp/bluetoothd.log | tail -n 40`,
	} {
		output, err := exec.Command("sh", "-c", command).CombinedOutput()
		t.Logf("$ %s (err=%v)\n%s", command, err, output)
	}
}
