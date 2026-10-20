package app

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/vladcuciureanu/betty/internal/domain"
	"github.com/vladcuciureanu/betty/internal/hidbattery"
	"github.com/vladcuciureanu/betty/internal/resolver"
)

const (
	// lookupScanLimit bounds the scan a command runs when the device it
	// needs is not among the devices the OS already knows.
	lookupScanLimit = 4 * time.Second
	// scanReserve is left of the deadline after a scan, for the work that
	// follows it.
	scanReserve = time.Second
)

// discoveryNeeded marks an operation that failed on known devices but might
// succeed with devices found by an active scan.
type discoveryNeeded struct{ err error }

func (e discoveryNeeded) Error() string { return e.err.Error() }
func (e discoveryNeeded) Unwrap() error { return e.err }

// needsDiscovery wraps err to ask withDevice to scan and retry once.
func needsDiscovery(err error) error { return discoveryNeeded{err} }

// withDevice resolves query and runs op on the device. It first tries the
// devices the OS already knows, which is fast. It scans and retries once when
// the query matches no known device, or when op returns needsDiscovery, for
// example because it needs a BLE endpoint that only a scan reports.
func (a *application) withDevice(ctx context.Context, query string, op func(devices []domain.Device, d domain.Device) error) error {
	raw, err := a.backend.List(ctx)
	var devices []domain.Device
	if err != nil {
		a.render.Debugf("listing known devices failed (%v); scanning instead", err)
	} else {
		devices = a.enrich(ctx, raw, "list")
		d, resolveErr := resolver.Resolve(devices, query)
		switch {
		case resolveErr == nil:
			opErr := op(devices, d)
			var retry discoveryNeeded
			if !errors.As(opErr, &retry) {
				return opErr
			}
			a.render.Debugf("%s: %v; scanning for more endpoints", d.ID, retry.err)
		case domain.AsError(resolveErr).Code != domain.CodeNotFound:
			return resolveErr
		default:
			a.render.Debugf("%q is not a known device; scanning", query)
		}
	}
	scanCtx, cancel := context.WithTimeout(ctx, min(lookupScanLimit, scanBudget(ctx)))
	defer cancel()
	devices, err = a.discover(scanCtx, raw)
	if err != nil {
		return err
	}
	d, err := resolver.Resolve(devices, query)
	if err != nil {
		return err
	}
	err = op(devices, d)
	var retry discoveryNeeded
	if errors.As(err, &retry) {
		return retry.err
	}
	return err
}

// known returns the devices the OS already knows, without scanning.
func (a *application) known(ctx context.Context) ([]domain.Device, error) {
	devices, err := a.backend.List(ctx)
	if err != nil {
		return nil, err
	}
	return a.enrich(ctx, devices, "list"), nil
}

// enrich deduplicates raw devices and adds HID batteries and driver data.
// Merging must happen before enrichment, because merging keeps only the
// first entry's properties.
func (a *application) enrich(ctx context.Context, raw []domain.Device, purpose string) []domain.Device {
	devices := a.applyHIDBatteries(ctx, mergeDevices(raw))
	if a.drivers != nil {
		devices = a.drivers.InspectFor(ctx, devices, purpose)
	}
	return devices
}

// discover scans with the OS backend and BLE at the same time and merges the
// results into base, the raw devices already known. When one scan fails and
// the other succeeds, the failure becomes a warning.
func (a *application) discover(ctx context.Context, base []domain.Device) ([]domain.Device, error) {
	type result struct {
		devices []domain.Device
		err     error
	}
	scanCtx, cancel := context.WithTimeout(ctx, scanBudget(ctx))
	defer cancel()
	osResult, bleResult := make(chan result, 1), make(chan result, 1)
	go func() { d, err := a.backend.Scan(scanCtx); osResult <- result{d, err} }()
	go func() { d, err := a.ble.Scan(scanCtx); bleResult <- result{d, err} }()
	// Merge in a fixed order, OS first, so that output never depends on
	// which scan finished first.
	fromOS, fromBLE := <-osResult, <-bleResult
	if fromOS.err != nil && fromBLE.err != nil {
		return nil, fromOS.err
	}
	if fromOS.err != nil {
		a.render.Warn("system Bluetooth scan failed: %v", fromOS.err)
	}
	if fromBLE.err != nil {
		a.render.Warn("BLE scan failed: %v", fromBLE.err)
	}
	return a.enrich(ctx, mergeDevices(base, fromOS.devices, fromBLE.devices), "scan"), nil
}

// scanBudget is how long a scan may run before ctx's deadline, leaving
// scanReserve for the work after it.
func scanBudget(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return defaultTimeout - scanReserve
	}
	remaining := time.Until(deadline)
	if remaining > 2*scanReserve {
		return remaining - scanReserve
	}
	return remaining / 2
}

func (a *application) applyHIDBatteries(ctx context.Context, devices []domain.Device) []domain.Device {
	if a.hid == nil {
		return devices
	}
	observations, err := a.hid.Batteries(ctx)
	if err != nil {
		a.render.Debugf("reading HID batteries failed: %v", err)
		return devices
	}
	return hidbattery.Apply(devices, observations)
}

// mergeDevices combines device lists, in order. Two entries are the same
// device when they share an ID or a 48-bit Bluetooth address on any
// endpoint: a dual-mode device advertises over BLE with the same public
// address it uses for Classic, so the OS entry and the BLE entry are one
// device. Names are never used as identity. The result is never nil.
func mergeDevices(sources ...[]domain.Device) []domain.Device {
	merged := []domain.Device{}
	for _, source := range sources {
		for _, candidate := range source {
			i := slices.IndexFunc(merged, func(d domain.Device) bool { return sameDevice(d, candidate) })
			if i < 0 {
				merged = append(merged, candidate)
				continue
			}
			mergeInto(&merged[i], candidate)
		}
	}
	return merged
}

func sameDevice(a, b domain.Device) bool {
	if a.ID == b.ID {
		return true
	}
	for _, left := range a.Endpoints {
		address := resolver.NormalizeAddress(left.Address)
		if address == "" {
			continue
		}
		for _, right := range b.Endpoints {
			if resolver.NormalizeAddress(right.Address) == address {
				return true
			}
		}
	}
	return false
}

// mergeInto adds what candidate knows to d. The first source's ID and name
// win, except that a real name replaces one that is only an address.
func mergeInto(d *domain.Device, candidate domain.Device) {
	for _, endpoint := range candidate.Endpoints {
		if !slices.ContainsFunc(d.Endpoints, func(e domain.Endpoint) bool { return sameEndpoint(e, endpoint) }) {
			d.Endpoints = append(d.Endpoints, endpoint)
		}
	}
	if (d.Name == "" || resolver.NormalizeAddress(d.Name) != "") && candidate.Name != "" && resolver.NormalizeAddress(candidate.Name) == "" {
		d.Name = candidate.Name
	}
	for _, service := range candidate.Services {
		if !slices.Contains(d.Services, service) {
			d.Services = append(d.Services, service)
		}
	}
	if candidate.RSSI != nil {
		d.RSSI = candidate.RSSI
	}
	if len(candidate.ManufacturerData) > 0 {
		d.ManufacturerData = candidate.ManufacturerData
	}
	if len(candidate.ServiceData) > 0 {
		d.ServiceData = candidate.ServiceData
	}
	if candidate.LastSeenAt != nil {
		d.LastSeenAt = candidate.LastSeenAt
	}
	d.Paired = d.Paired || candidate.Paired
	d.Connected = d.Connected || candidate.Connected
}

func sameEndpoint(a, b domain.Endpoint) bool {
	if a.Transport != b.Transport {
		return false
	}
	if address := resolver.NormalizeAddress(a.Address); address != "" {
		return address == resolver.NormalizeAddress(b.Address)
	}
	return a.ID != "" && a.ID == b.ID
}

// bleAddress returns the address of d's BLE endpoint, which GATT needs.
func bleAddress(d domain.Device) (string, bool) {
	for _, e := range d.Endpoints {
		if e.Transport == domain.TransportBLE && e.Address != "" {
			return e.Address, true
		}
	}
	return "", false
}

// requireBLE returns d's BLE address, or asks for a scan when d has none yet.
func requireBLE(d domain.Device) (string, error) {
	if address, ok := bleAddress(d); ok {
		return address, nil
	}
	return "", needsDiscovery(domain.NewError(domain.CodeUnsupported, "%s has no discoverable BLE endpoint; GATT needs one", d.Name))
}

func findByID(devices []domain.Device, id string) (domain.Device, bool) {
	i := slices.IndexFunc(devices, func(d domain.Device) bool { return d.ID == id })
	if i < 0 {
		return domain.Device{}, false
	}
	return devices[i], true
}
