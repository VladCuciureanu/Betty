package driver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"

	protocol "github.com/vladcuciureanu/betty/driver"
	"github.com/vladcuciureanu/betty/internal/domain"
)

// Watch asks the installed drivers, in turn, to push changes to property of
// deviceID. The first driver that sends an event owns the watch: Watch calls
// onChange with the devices, updated by each event, until the driver ends
// the watch, onChange fails, or ctx ends. handled is false when no driver
// sent an event, so the caller can poll instead.
func (m *Manager) Watch(ctx context.Context, devices []domain.Device, deviceID, property string, onChange func([]domain.Device) error) (handled bool, err error) {
	installed, err := m.List()
	if err != nil {
		return false, err
	}
	related := []string{deviceID}
	for _, d := range devices {
		if d.ID == deviceID {
			related = append(related, d.PossibleMatches...)
		}
	}
	var candidates []domain.Device
	for _, d := range devices {
		if slices.Contains(related, d.ID) || slices.Contains(d.PossibleMatches, deviceID) {
			candidates = append(candidates, d)
		}
	}
	for _, item := range installed {
		if !matchesAny(item.Match, candidates) {
			continue
		}
		handled, err := m.watchOne(ctx, item, devices, deviceID, property, onChange)
		if handled || ctx.Err() != nil {
			return handled, err
		}
		// A driver that refuses or fails before its first event leaves the
		// watch to the next driver, or to polling.
	}
	return false, nil
}

// watchOne runs one watch request on its own driver process, which ends with
// the watch.
func (m *Manager) watchOne(ctx context.Context, item protocol.Manifest, devices []domain.Device, deviceID, property string, onChange func([]domain.Device) error) (bool, error) {
	p, err := m.start(item)
	if err != nil {
		return false, err
	}
	defer p.kill()
	stop := context.AfterFunc(ctx, func() { _ = p.cmd.Process.Kill() })
	defer stop()
	request := protocol.Request{Protocol: protocol.Version, Type: protocol.TypeRequest, ID: "watch", Method: protocol.MethodWatch, DeviceID: deviceID, Property: property, Devices: wireDevices(devices)}
	session := newHostSession(request.Devices)
	defer m.closeSession(session)
	if err := p.encoder.Encode(request); err != nil {
		return false, driverFailure(item, "write request: %v", err)
	}
	handled := false
	for {
		raw, err := readMessage(p.reader, maxMessageSize)
		if errors.Is(err, io.EOF) {
			if ctx.Err() != nil {
				return handled, nil
			}
			return handled, driverFailure(item, "exited during a watch")
		}
		if err != nil {
			return handled, driverFailure(item, "%v", err)
		}
		var message protocol.Response
		if err := json.Unmarshal(raw, &message); err != nil {
			return handled, driverFailure(item, "invalid message: %v", err)
		}
		switch message.Type {
		case protocol.TypeHostRequest:
			var call protocol.Message
			_ = json.Unmarshal(raw, &call)
			if err := p.encoder.Encode(m.hostReply(ctx, item, call, session)); err != nil {
				return handled, driverFailure(item, "write host response: %v", err)
			}
		case protocol.TypeEvent:
			if message.ID != request.ID {
				return handled, driverFailure(item, "sent an event for request %q instead of %q", message.ID, request.ID)
			}
			devices = applyEnrichments(devices, message.Enrichments)
			handled = true
			if err := onChange(devices); err != nil {
				return true, err
			}
		case protocol.TypeResponse:
			if message.ID != request.ID {
				return handled, driverFailure(item, "answered request %q instead of %q", message.ID, request.ID)
			}
			_, err := checkResponse(item, message)
			if !handled && err != nil && domain.AsError(err).Code == domain.CodeUnsupported {
				// An older driver that knows no watch method.
				return false, nil
			}
			return handled, err
		default:
			return handled, driverFailure(item, "sent unexpected message type %q", message.Type)
		}
	}
}
