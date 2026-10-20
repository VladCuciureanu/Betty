package app

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/vladcuciureanu/betty/internal/domain"
	"github.com/vladcuciureanu/betty/internal/driver"
)

type versionResult struct {
	Version string `json:"version"`
}

func (r versionResult) WriteHuman(w io.Writer) error {
	_, err := fmt.Fprintln(w, "bt", r.Version)
	return err
}

type helpResult struct {
	Text string `json:"help"`
}

func (r helpResult) WriteHuman(w io.Writer) error {
	_, err := fmt.Fprintln(w, r.Text)
	return err
}

func (a *application) scan(ctx context.Context, _ []string) error {
	devices, err := a.discover(ctx, nil)
	if err != nil {
		return err
	}
	return a.render.Data(devices)
}

func (a *application) list(ctx context.Context, _ []string) error {
	devices, err := a.known(ctx)
	if err != nil {
		return err
	}
	return a.render.Data(devices)
}

func (a *application) inspect(ctx context.Context, args []string) error {
	return a.withDevice(ctx, args[0], func(_ []domain.Device, d domain.Device) error {
		if address, ok := bleAddress(d); ok {
			information, err := a.ble.DeviceInformation(ctx, address)
			if err != nil {
				a.render.Debugf("reading Device Information from %s failed: %v", d.ID, err)
			} else {
				d.Information = information
			}
		}
		return a.render.Data(d)
	})
}

func (a *application) capabilities(context.Context, []string) error {
	return a.render.Data(a.backend.Capabilities())
}

// mutationResult reports the verified state after pair, unpair, connect,
// or disconnect. Changed is false when the device was already in that state.
type mutationResult struct {
	DeviceID  string `json:"device_id"`
	Name      string `json:"name"`
	Action    string `json:"action"`
	Changed   bool   `json:"changed"`
	Paired    bool   `json:"paired"`
	Connected bool   `json:"connected"`
}

func (r mutationResult) WriteHuman(w io.Writer) error {
	past := map[string]string{"pair": "Paired", "unpair": "Unpaired", "connect": "Connected", "disconnect": "Disconnected"}[r.Action]
	if !r.Changed {
		_, err := fmt.Fprintf(w, "%s was already %s\n", r.Name, strings.ToLower(past))
		return err
	}
	_, err := fmt.Fprintf(w, "%s %s\n", past, r.Name)
	return err
}

// mutation returns the command that changes pairing or connection state.
// Asking for the state a device is already in succeeds without doing
// anything, so scripts can run these commands repeatedly.
func mutation(action string) func(*application, context.Context, []string) error {
	return func(a *application, ctx context.Context, args []string) error {
		return a.withDevice(ctx, args[0], func(_ []domain.Device, d domain.Device) error {
			result := mutationResult{DeviceID: d.ID, Name: d.Name, Action: action, Paired: d.Paired, Connected: d.Connected}
			var err error
			switch action {
			case "pair":
				if result.Changed = !d.Paired; result.Changed {
					err = a.backend.Pair(ctx, d)
					result.Paired = true
				}
			case "unpair":
				if result.Changed = d.Paired; result.Changed {
					err = a.backend.Unpair(ctx, d)
					result.Paired, result.Connected = false, false
				}
			case "connect":
				if result.Changed = !d.Connected; result.Changed {
					err = a.backend.Connect(ctx, d)
					result.Connected = true
				}
			case "disconnect":
				if result.Changed = d.Connected; result.Changed {
					err = a.backend.Disconnect(ctx, d)
					result.Connected = false
				}
			}
			if err != nil {
				return err
			}
			return a.render.Data(result)
		})
	}
}

// gattValue is a characteristic value; watch adds an event.
type gattValue struct {
	Event          string `json:"event,omitempty"`
	DeviceID       string `json:"device_id"`
	Characteristic string `json:"characteristic"`
	Value          string `json:"value"`
}

func (r gattValue) WriteHuman(w io.Writer) error {
	_, err := fmt.Fprintln(w, r.Value)
	return err
}

type gattWriteResult struct {
	DeviceID       string `json:"device_id"`
	Characteristic string `json:"characteristic"`
	Bytes          int    `json:"bytes"`
	WithResponse   bool   `json:"with_response"`
}

func (r gattWriteResult) WriteHuman(w io.Writer) error {
	mode := "with response"
	if !r.WithResponse {
		mode = "without response"
	}
	_, err := fmt.Fprintf(w, "Wrote %d bytes to %s (%s)\n", r.Bytes, r.Characteristic, mode)
	return err
}

func (a *application) gatt(ctx context.Context, args []string) error {
	operation, rest := args[1], args[2:]
	usage := map[string]string{
		"services":        "gatt <device> services",
		"characteristics": "gatt <device> characteristics [service-uuid]",
		"read":            "gatt <device> read <characteristic-uuid>",
		"write":           "gatt <device> write <characteristic-uuid> <hex> [--without-response]",
		"watch":           "gatt <device> watch <characteristic-uuid>",
	}
	wantArgs := map[string][2]int{"services": {0, 0}, "characteristics": {0, 1}, "read": {1, 1}, "write": {2, 2}, "watch": {1, 1}}
	limits, ok := wantArgs[operation]
	if !ok {
		return domain.NewError(domain.CodeUsage, "unknown gatt operation %q; use services, characteristics, read, write, or watch", operation)
	}
	if len(rest) < limits[0] || len(rest) > limits[1] {
		return domain.NewError(domain.CodeUsage, "usage: bt %s", usage[operation])
	}
	var value []byte
	if operation == "write" {
		var err error
		if value, err = hex.DecodeString(strings.TrimPrefix(rest[1], "0x")); err != nil || len(value) == 0 {
			return domain.NewError(domain.CodeUsage, "%q is not a hexadecimal value", rest[1])
		}
	}
	return a.withDevice(ctx, args[0], func(_ []domain.Device, d domain.Device) error {
		address, err := requireBLE(d)
		if err != nil {
			return err
		}
		switch operation {
		case "services":
			services, err := a.ble.Services(ctx, address)
			if err != nil {
				return err
			}
			return a.render.Data(nonNil(services))
		case "characteristics":
			service := ""
			if len(rest) == 1 {
				service = rest[0]
			}
			characteristics, err := a.ble.Characteristics(ctx, address, service)
			if err != nil {
				return err
			}
			return a.render.Data(nonNil(characteristics))
		case "read":
			data, err := a.ble.Read(ctx, address, rest[0])
			if err != nil {
				return err
			}
			return a.render.Data(gattValue{DeviceID: d.ID, Characteristic: rest[0], Value: hex.EncodeToString(data)})
		case "write":
			withResponse := !a.opts.withoutResponse
			if err := a.ble.Write(ctx, address, rest[0], value, !withResponse); err != nil {
				return err
			}
			return a.render.Data(gattWriteResult{DeviceID: d.ID, Characteristic: rest[0], Bytes: len(value), WithResponse: withResponse})
		default: // watch
			return a.ble.Watch(ctx, address, rest[0], func(data []byte) error {
				return a.render.Data(gattValue{Event: "notification", DeviceID: d.ID, Characteristic: rest[0], Value: hex.EncodeToString(data)})
			})
		}
	})
}

func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

type driverResult struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	File   string `json:"file,omitempty"`
}

func (r driverResult) WriteHuman(w io.Writer) error {
	var err error
	switch r.Action {
	case "remove":
		_, err = fmt.Fprintf(w, "Removed driver %s\n", r.ID)
	case "export-state":
		_, err = fmt.Fprintf(w, "Exported the state of %s to %s\n", r.ID, r.File)
	case "import-state":
		_, err = fmt.Fprintf(w, "Imported the state of %s from %s\n", r.ID, r.File)
	}
	return err
}

type checkResults []driver.CheckResult

func (r checkResults) WriteHuman(w io.Writer) error {
	for _, result := range r {
		mark := "ok  "
		if !result.Passed {
			mark = "FAIL"
		}
		line := mark + " " + result.Name
		if result.Detail != "" {
			line += ": " + result.Detail
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func (a *application) driverCommand(ctx context.Context, args []string) error {
	if a.drivers == nil {
		return domain.NewError(domain.CodeUnsupported, "optional drivers are unavailable")
	}
	if len(args) == 0 {
		args = []string{"list"}
	}
	usage := map[string]string{
		"list":         "driver list",
		"install":      "driver install <executable>",
		"remove":       "driver remove <id>",
		"export-state": "driver export-state <id> <file>",
		"import-state": "driver import-state <id> <file>",
		"check":        "driver check <executable>",
		"upgrade":      "driver upgrade <executable>",
	}
	wantArgs := map[string]int{"list": 0, "install": 1, "upgrade": 1, "remove": 1, "export-state": 2, "import-state": 2, "check": 1}
	want, ok := wantArgs[args[0]]
	if !ok {
		return domain.NewError(domain.CodeUsage, "unknown driver operation %q; use list, install, upgrade, remove, export-state, import-state, or check", args[0])
	}
	if len(args)-1 != want {
		return domain.NewError(domain.CodeUsage, "usage: bt %s", usage[args[0]])
	}
	switch args[0] {
	case "list":
		items, err := a.drivers.List()
		if err != nil {
			return err
		}
		return a.render.Data(nonNil(items))
	case "install":
		manifest, err := a.drivers.Install(ctx, args[1])
		if err != nil {
			return err
		}
		return a.render.Data(manifest)
	case "upgrade":
		manifest, err := a.drivers.Upgrade(ctx, args[1])
		if err != nil {
			return err
		}
		return a.render.Data(manifest)
	case "check":
		results, err := driver.Check(ctx, args[1])
		if err != nil {
			return err
		}
		if err := a.render.Data(checkResults(results)); err != nil {
			return err
		}
		failed := 0
		for _, result := range results {
			if !result.Passed {
				failed++
			}
		}
		if failed > 0 {
			return domain.NewError(domain.CodeUnsupported, "the driver failed %d of %d conformance checks", failed, len(results))
		}
		return nil
	case "remove":
		if err := a.drivers.Remove(args[1]); err != nil {
			return err
		}
		return a.render.Data(driverResult{ID: args[1], Action: "remove"})
	case "export-state":
		if err := a.drivers.ExportState(args[1], args[2]); err != nil {
			return err
		}
		return a.render.Data(driverResult{ID: args[1], Action: "export-state", File: args[2]})
	default: // import-state
		if err := a.drivers.ImportState(args[1], args[2]); err != nil {
			return err
		}
		return a.render.Data(driverResult{ID: args[1], Action: "import-state", File: args[2]})
	}
}
