//go:build linux

package backend

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/vladcuciureanu/betty/internal/domain"
)

const (
	agentInterface = "org.bluez.Agent1"
	agentPath      = dbus.ObjectPath("/com/betty/PairingAgent")
)

type pairingAgent struct {
	in                  *bufio.Reader
	out                 io.Writer
	mu                  sync.Mutex
	interactionRequired bool
}

func (b *linuxBackend) registerPairingAgent() (func(), *pairingAgent, error) {
	if b.initErr != nil {
		return nil, nil, domain.NewError(domain.CodeUnavailable, "cannot connect to system D-Bus: %v", b.initErr)
	}
	agent := &pairingAgent{out: b.pairing.Out}
	capability := "NoInputNoOutput"
	if b.pairing.In != nil {
		agent.in = bufio.NewReader(b.pairing.In)
		capability = "KeyboardDisplay"
	}
	if agent.out == nil {
		agent.out = io.Discard
	}
	if err := b.conn.Export(agent, agentPath, agentInterface); err != nil {
		return nil, nil, fmt.Errorf("export BlueZ pairing agent: %w", err)
	}
	manager := b.conn.Object(bluez, "/org/bluez")
	if err := translateDBus(manager.Call("org.bluez.AgentManager1.RegisterAgent", 0, agentPath, capability).Err); err != nil {
		_ = b.conn.Export(nil, agentPath, agentInterface)
		return nil, nil, err
	}
	return func() {
		_ = manager.Call("org.bluez.AgentManager1.UnregisterAgent", 0, agentPath).Err
		_ = b.conn.Export(nil, agentPath, agentInterface)
	}, agent, nil
}

func (*pairingAgent) Release() *dbus.Error { return nil }

func (a *pairingAgent) RequestPinCode(device dbus.ObjectPath) (string, *dbus.Error) {
	value, err := a.prompt(fmt.Sprintf("PIN for %s: ", device))
	if err != nil || value == "" {
		return "", pairingRejected(err)
	}
	return value, nil
}

func (a *pairingAgent) DisplayPinCode(device dbus.ObjectPath, pin string) *dbus.Error {
	a.notice("PIN for %s: %s\n", device, pin)
	return nil
}

func (a *pairingAgent) RequestPasskey(device dbus.ObjectPath) (uint32, *dbus.Error) {
	value, err := a.prompt(fmt.Sprintf("Passkey for %s: ", device))
	if err != nil {
		return 0, pairingRejected(err)
	}
	passkey, parseErr := strconv.ParseUint(value, 10, 32)
	if parseErr != nil || passkey > 999999 {
		return 0, dbus.NewError("org.bluez.Error.Rejected", []any{"passkey must contain at most six decimal digits"})
	}
	return uint32(passkey), nil
}

func (a *pairingAgent) DisplayPasskey(device dbus.ObjectPath, passkey uint32, entered uint16) *dbus.Error {
	a.notice("Passkey for %s: %06d (%d digits entered)\n", device, passkey, entered)
	return nil
}

func (a *pairingAgent) RequestConfirmation(device dbus.ObjectPath, passkey uint32) *dbus.Error {
	value, err := a.prompt(fmt.Sprintf("Confirm passkey %06d for %s? [y/N] ", passkey, device))
	value = strings.ToLower(value)
	if err != nil || (value != "y" && value != "yes") {
		return pairingRejected(err)
	}
	return nil
}

func (a *pairingAgent) RequestAuthorization(device dbus.ObjectPath) *dbus.Error {
	return a.confirm(fmt.Sprintf("Authorize pairing with %s? [y/N] ", device))
}

func (a *pairingAgent) AuthorizeService(device dbus.ObjectPath, uuid string) *dbus.Error {
	return a.confirm(fmt.Sprintf("Authorize Bluetooth service %s for %s? [y/N] ", uuid, device))
}

func (*pairingAgent) Cancel() *dbus.Error { return nil }

func (a *pairingAgent) confirm(message string) *dbus.Error {
	value, err := a.prompt(message)
	value = strings.ToLower(value)
	if err != nil || (value != "y" && value != "yes") {
		return pairingRejected(err)
	}
	return nil
}

func (a *pairingAgent) prompt(message string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.in == nil {
		a.interactionRequired = true
		return "", errors.New("pairing requires interactive input")
	}
	if _, err := fmt.Fprint(a.out, message); err != nil {
		return "", err
	}
	value, err := a.in.ReadString('\n')
	value = strings.TrimSpace(value)
	if errors.Is(err, io.EOF) && value != "" {
		err = nil
	}
	return value, err
}

func (a *pairingAgent) needsInteraction() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.interactionRequired
}

func (a *pairingAgent) notice(format string, values ...any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, _ = fmt.Fprintf(a.out, format, values...)
}

func pairingRejected(err error) *dbus.Error {
	message := "pairing rejected"
	if err != nil {
		message = err.Error()
	}
	return dbus.NewError("org.bluez.Error.Rejected", []any{message})
}
