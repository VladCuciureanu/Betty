//go:build linux

package backend

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestPairingAgentConfirmation(t *testing.T) {
	var output bytes.Buffer
	agent := pairingAgent{in: bufioReader("yes\n"), out: &output}
	if err := agent.RequestConfirmation(dbus.ObjectPath("/org/bluez/hci0/dev_AA"), 123456); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "123456") {
		t.Fatalf("prompt=%q", output.String())
	}
}

func TestPairingAgentRejectsInvalidPasskey(t *testing.T) {
	agent := pairingAgent{in: bufioReader("1234567\n"), out: &bytes.Buffer{}}
	if _, err := agent.RequestPasskey(dbus.ObjectPath("/org/bluez/hci0/dev_AA")); err == nil || err.Name != "org.bluez.Error.Rejected" {
		t.Fatalf("error=%v", err)
	}
}

func TestNonInteractivePairingAgentReportsRequiredInteraction(t *testing.T) {
	agent := &pairingAgent{out: io.Discard}
	if _, err := agent.prompt("PIN: "); err == nil || !agent.needsInteraction() {
		t.Fatalf("err=%v required=%t", err, agent.needsInteraction())
	}
}

func TestPairingAgentAcceptsPINAtEOF(t *testing.T) {
	agent := pairingAgent{in: bufioReader("0000"), out: &bytes.Buffer{}}
	if pin, err := agent.RequestPinCode(dbus.ObjectPath("/org/bluez/hci0/dev_AA")); err != nil || pin != "0000" {
		t.Fatalf("pin=%q error=%v", pin, err)
	}
}

func bufioReader(value string) *bufio.Reader { return bufio.NewReader(strings.NewReader(value)) }
