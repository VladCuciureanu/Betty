package backend

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// pairingAgent answers the OS's pairing requests (PIN, passkey,
// confirmation) on the user's terminal. Without input it records that
// interaction was required so Pair can report interaction_required.
type pairingAgent struct {
	in                  *bufio.Reader
	out                 io.Writer
	mu                  sync.Mutex
	interactionRequired bool
}

func newPairingAgent(pairing PairingIO) *pairingAgent {
	agent := &pairingAgent{out: pairing.Out}
	if pairing.In != nil {
		agent.in = bufio.NewReader(pairing.In)
	}
	if agent.out == nil {
		agent.out = io.Discard
	}
	return agent
}

func (a *pairingAgent) interactive() bool { return a.in != nil }

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

// confirmed asks a yes/no question; anything but y/yes is a no.
func (a *pairingAgent) confirmed(message string) (bool, error) {
	value, err := a.prompt(message)
	if err != nil {
		return false, err
	}
	value = strings.ToLower(value)
	return value == "y" || value == "yes", nil
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
