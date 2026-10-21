package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	protocol "github.com/vladcuciureanu/betty/driver"
	"github.com/vladcuciureanu/betty/internal/domain"
)

// propertyResult is the JSON shape of get, set, and watch output.
type propertyResult struct {
	Event      string          `json:"event,omitempty"`
	DeviceID   string          `json:"device_id"`
	Property   string          `json:"property"`
	Value      json.RawMessage `json:"value"`
	Unit       string          `json:"unit,omitempty"`
	Estimated  bool            `json:"estimated"`
	Resolution int             `json:"resolution,omitempty"`
	// Verified is set only by set: whether the driver read the value back
	// from the device after writing it.
	Verified   *bool `json:"verified,omitempty"`
	Correlated bool  `json:"correlated,omitempty"`
	display    string
}

func (r propertyResult) WriteHuman(w io.Writer) error {
	text := r.display
	if text == "" {
		text = string(r.Value)
	}
	if r.Estimated && r.Resolution > 0 {
		text += fmt.Sprintf(" (estimated, ±%d)", r.Resolution)
	}
	if r.Correlated {
		text += " (from a nearby advertisement that probably belongs to this device)"
	}
	_, err := fmt.Fprintln(w, text)
	return err
}

func newPropertyResult(d domain.Device, name string, p domain.Property) (propertyResult, error) {
	if len(p.Value) == 0 || !json.Valid(p.Value) {
		return propertyResult{}, domain.NewError(domain.CodeInternal, "%s returned no valid value for %s", d.Name, name)
	}
	return propertyResult{DeviceID: d.ID, Property: name, Value: p.Value, Unit: p.Unit, Estimated: p.Estimated, Resolution: p.Resolution, Correlated: p.Correlated, display: p.Display}, nil
}

func (a *application) get(ctx context.Context, args []string) error {
	if a.opts.all {
		return a.getAll(ctx, args[0])
	}
	return a.withDevice(ctx, args[0], func(devices []domain.Device, d domain.Device) error {
		result, err := a.readProperty(ctx, devices, d, args[1])
		if err != nil {
			return err
		}
		return a.render.Data(result)
	})
}

// propertyEntry is one device's result in get --all: the property, or the
// error that kept Betty from reading it.
type propertyEntry struct {
	DeviceID   string          `json:"device_id"`
	Name       string          `json:"name"`
	Property   string          `json:"property"`
	Value      json.RawMessage `json:"value,omitempty"`
	Unit       string          `json:"unit,omitempty"`
	Estimated  bool            `json:"estimated,omitempty"`
	Resolution int             `json:"resolution,omitempty"`
	Error      *domain.Error   `json:"error,omitempty"`
	display    string
}

type propertyEntries []propertyEntry

func (entries propertyEntries) WriteHuman(w io.Writer) error {
	for _, entry := range entries {
		text := entry.display
		switch {
		case entry.Error != nil:
			text = "(" + entry.Error.Message + ")"
		case text == "":
			text = string(entry.Value)
		}
		if entry.Estimated && entry.Resolution > 0 {
			text += fmt.Sprintf(" (estimated, ±%d)", entry.Resolution)
		}
		if _, err := fmt.Fprintf(w, "%s: %s\n", entry.Name, text); err != nil {
			return err
		}
	}
	return nil
}

// getAll reads name from every known device, one at a time. Each device gets
// an equal share of the time left, so one that doesn't answer can't starve
// the rest.
func (a *application) getAll(ctx context.Context, name string) error {
	devices, err := a.known(ctx)
	if err != nil {
		return err
	}
	entries := make(propertyEntries, 0, len(devices))
	for i, d := range devices {
		readCtx, cancel, budget := ctx, context.CancelFunc(func() {}), time.Duration(0)
		if deadline, ok := ctx.Deadline(); ok {
			budget = max(time.Until(deadline)/time.Duration(len(devices)-i), time.Second)
			readCtx, cancel = context.WithTimeout(ctx, budget)
		}
		result, err := a.readProperty(readCtx, devices, d, name)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		entry := propertyEntry{DeviceID: d.ID, Name: d.Name, Property: name}
		if err != nil {
			entry.Error = domain.AsError(classifyContextError(err, ctx, budget))
		} else {
			entry.Value, entry.Unit, entry.Estimated, entry.Resolution, entry.display = result.Value, result.Unit, result.Estimated, result.Resolution, result.display
		}
		entries = append(entries, entry)
	}
	return a.render.Data(entries)
}

// readProperty reads name from d, asking drivers first and then standard
// GATT services. It returns needsDiscovery when a scan might help.
func (a *application) readProperty(ctx context.Context, devices []domain.Device, d domain.Device, name string) (propertyResult, error) {
	if a.drivers != nil {
		updated, _, err := a.drivers.Property(ctx, devices, "get", d.ID, name, nil)
		if err != nil {
			// A driver may need devices that only a scan reports, such as
			// the advertisements macOS Apple Audio battery readings come
			// from, so a failed read is tried once more after a scan.
			return propertyResult{}, needsDiscovery(err)
		}
		if found, ok := findByID(updated, d.ID); ok {
			d = found
		}
	}
	if property, ok := d.Properties[name]; ok {
		return newPropertyResult(d, name, property)
	}
	if capability, ok := d.Capabilities[name]; ok && capability.Status != domain.StatusAvailable {
		return propertyResult{}, domain.NewError(domain.CodeUnsupported, "%s on %s is %s: %s", name, d.Name, capability.Status, capability.Reason)
	}
	switch {
	case name == "battery":
		return a.readGATTBattery(ctx, d)
	case deviceInformationName(name) != "":
		return a.readDeviceInformation(ctx, d, name)
	case !a.hasDrivers():
		return propertyResult{}, domain.NewError(domain.CodeDriverRequired, "no installed driver provides %q; Betty itself reads only battery and Device Information properties", name)
	default:
		return propertyResult{}, needsDiscovery(domain.NewError(domain.CodeUnsupported, "%s does not expose %q", d.Name, name))
	}
}

func (a *application) readGATTBattery(ctx context.Context, d domain.Device) (propertyResult, error) {
	address, err := requireBLE(d)
	if err != nil {
		return propertyResult{}, err
	}
	value, err := a.ble.Read(ctx, address, "2a19")
	if err != nil {
		return propertyResult{}, err
	}
	if len(value) != 1 || value[0] > 100 {
		return propertyResult{}, domain.NewError(domain.CodeUnsupported, "%s returned an invalid Battery Level", d.Name)
	}
	raw, _ := json.Marshal(protocol.SingleBattery(protocol.BatteryComponent{Level: int(value[0])}))
	return newPropertyResult(d, "battery", domain.Property{Value: raw, Unit: "%", Display: fmt.Sprintf("%d%%", value[0])})
}

func (a *application) readDeviceInformation(ctx context.Context, d domain.Device, name string) (propertyResult, error) {
	address, err := requireBLE(d)
	if err != nil {
		return propertyResult{}, err
	}
	information, err := a.ble.DeviceInformation(ctx, address)
	if err != nil {
		return propertyResult{}, err
	}
	value, ok := information[deviceInformationName(name)]
	if !ok {
		return propertyResult{}, domain.NewError(domain.CodeUnsupported, "%s does not report %s", d.Name, name)
	}
	raw, _ := json.Marshal(value)
	return newPropertyResult(d, name, domain.Property{Value: raw, Display: value})
}

// deviceInformationName maps a property to its Device Information Service
// field, or returns "" for other properties.
func deviceInformationName(property string) string {
	switch property {
	case "manufacturer", "model", "serial_number", "hardware_revision", "software_revision",
		"firmware_version", "system_id", "pnp_id", "regulatory_certification":
		return property
	case "firmware_revision": // the characteristic's own name
		return "firmware_version"
	default:
		return ""
	}
}

func (a *application) hasDrivers() bool {
	if a.drivers == nil {
		return false
	}
	items, err := a.drivers.List()
	return err == nil && len(items) > 0
}

func (a *application) set(ctx context.Context, args []string) error {
	name, input := args[1], args[2]
	return a.withDevice(ctx, args[0], func(devices []domain.Device, d domain.Device) error {
		capability, ok := d.Capabilities[name]
		if !ok {
			if !a.hasDrivers() {
				return domain.NewError(domain.CodeDriverRequired, "no installed driver can set %q", name)
			}
			return needsDiscovery(domain.NewError(domain.CodeUnsupported, "%s does not expose %q", d.Name, name))
		}
		if capability.Status != domain.StatusAvailable || !capability.Writable {
			return domain.NewError(domain.CodeUnsupported, "%s on %s is not writable: %s", name, d.Name, capability.Reason)
		}
		value, err := propertyInput(capability, name, input)
		if err != nil {
			return err
		}
		devices, handled, err := a.drivers.Property(ctx, devices, "set", d.ID, name, value)
		if err != nil {
			return err
		}
		if !handled {
			return domain.NewError(domain.CodeUnsupported, "no installed driver can set %s on %s", name, d.Name)
		}
		d, _ = findByID(devices, d.ID)
		property, ok := d.Properties[name]
		if !ok {
			return domain.NewError(domain.CodeUnsupported, "the driver did not verify %s after writing it", name)
		}
		result, err := newPropertyResult(d, name, property)
		if err != nil {
			return err
		}
		result.Verified = &property.Verified
		if !property.Verified {
			a.render.Warn("the driver reports %s as requested, not as read back from %s", name, d.Name)
		}
		return a.render.Data(result)
	})
}

// propertyInput converts a command-line value to JSON for capability,
// validating it against the capability's accepted values or range.
func propertyInput(capability domain.DeviceCapability, name, input string) (json.RawMessage, error) {
	if len(capability.Values) > 0 && !slices.Contains(capability.Values, input) {
		return nil, domain.NewError(domain.CodeUsage, "invalid %s value %q; accepted values: %s", name, input, strings.Join(capability.Values, ", "))
	}
	var value any = input
	switch capability.Kind {
	case domain.KindInteger:
		parsed, err := strconv.ParseInt(input, 10, 64)
		if err != nil {
			return nil, domain.NewError(domain.CodeUsage, "%s requires an integer", name)
		}
		if err := validateRange(capability, name, float64(parsed)); err != nil {
			return nil, err
		}
		value = parsed
	case domain.KindNumber:
		parsed, err := strconv.ParseFloat(input, 64)
		if err != nil {
			return nil, domain.NewError(domain.CodeUsage, "%s requires a number", name)
		}
		if err := validateRange(capability, name, parsed); err != nil {
			return nil, err
		}
		value = parsed
	}
	return json.Marshal(value)
}

func validateRange(capability domain.DeviceCapability, name string, value float64) error {
	if capability.Minimum != nil && value < *capability.Minimum {
		return domain.NewError(domain.CodeUsage, "%s must be at least %v", name, *capability.Minimum)
	}
	if capability.Maximum != nil && value > *capability.Maximum {
		return domain.NewError(domain.CodeUsage, "%s must be at most %v", name, *capability.Maximum)
	}
	if capability.Step != nil && *capability.Step > 0 {
		base := 0.0
		if capability.Minimum != nil {
			base = *capability.Minimum
		}
		steps := (value - base) / *capability.Step
		if math.Abs(steps-math.Round(steps)) > 1e-9 {
			return domain.NewError(domain.CodeUsage, "%s must use increments of %v", name, *capability.Step)
		}
	}
	return nil
}

// watch prints the property once, then again whenever its value changes.
// After the first read it reuses the resolved device, so polling does not
// rescan. Retryable failures are reported as warnings and polling continues.
func (a *application) watch(ctx context.Context, args []string) error {
	query, name := args[0], args[1]
	if handled, err := a.watchThroughDriver(ctx, query, name); handled || err != nil {
		return err
	}
	var devices []domain.Device
	var device domain.Device
	resolved := false
	previous, event := "", "snapshot"
	for {
		readCtx, cancel := context.WithTimeout(ctx, a.opts.timeout)
		var result propertyResult
		var err error
		if resolved {
			result, err = a.readProperty(readCtx, devices, device, name)
		} else {
			err = a.withDevice(readCtx, query, func(all []domain.Device, d domain.Device) error {
				var readErr error
				result, readErr = a.readProperty(readCtx, all, d, name)
				if readErr == nil {
					devices, device, resolved = all, d, true
				}
				return readErr
			})
		}
		cancel()
		switch {
		case ctx.Err() != nil:
			return nil
		case err != nil && !domain.AsError(err).Retryable:
			return err
		case err != nil:
			a.render.Warn("reading %s failed, retrying: %v", name, err)
			resolved = false
		case string(result.Value) != previous:
			result.Event = event
			if err := a.render.Data(result); err != nil {
				return err
			}
			previous, event = string(result.Value), "changed"
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(a.opts.interval):
		}
	}
}

// watchThroughDriver offers the watch to the drivers, which can push each
// change instead of being polled. handled is false when none takes it.
func (a *application) watchThroughDriver(ctx context.Context, query, name string) (handled bool, err error) {
	if !a.hasDrivers() {
		return false, nil
	}
	resolveCtx, cancel := context.WithTimeout(ctx, a.opts.timeout)
	var devices []domain.Device
	var device domain.Device
	err = a.withDevice(resolveCtx, query, func(all []domain.Device, d domain.Device) error {
		devices, device = all, d
		return nil
	})
	cancel()
	if err != nil {
		return false, err
	}
	previous, event := "", "snapshot"
	handled, err = a.drivers.Watch(ctx, devices, device.ID, name, func(updated []domain.Device) error {
		d, ok := findByID(updated, device.ID)
		if !ok {
			return nil
		}
		property, ok := d.Properties[name]
		if !ok {
			return nil
		}
		result, err := newPropertyResult(d, name, property)
		if err != nil || string(result.Value) == previous {
			return err
		}
		result.Event = event
		previous, event = string(result.Value), "changed"
		return a.render.Data(result)
	})
	if ctx.Err() != nil {
		// Interrupting a watch is how it is meant to end.
		return true, nil
	}
	return handled, err
}
