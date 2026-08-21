package app

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/vladcuciureanu/betty/internal/backend"
	"github.com/vladcuciureanu/betty/internal/ble"
	"github.com/vladcuciureanu/betty/internal/domain"
	"github.com/vladcuciureanu/betty/internal/output"
	"github.com/vladcuciureanu/betty/internal/resolver"
)

type application struct {
	backend backend.Backend
	ble     ble.Client
	render  output.Renderer
	timeout time.Duration
	version string
}

func Main(args []string, stdin io.Reader, stdout, stderr io.Writer, version string) int {
	if len(args) == 1 && (args[0] == "--version" || args[0] == "version") {
		fmt.Fprintln(stdout, "bt "+version)
		return 0
	}
	if len(args) == 1 && args[0] == "--help" {
		fmt.Fprintln(stdout, helpText)
		return 0
	}
	_ = stdin
	opts, rest, err := parseGlobal(args, stderr)
	renderer := output.Renderer{Out: stdout, Err: stderr, Mode: opts.mode, Verbose: opts.verbose}
	if err != nil {
		renderer.Failure(err)
		return domain.ExitCode(err)
	}
	a := application{backend: backend.New(), ble: ble.New(), render: renderer, timeout: opts.timeout, version: version}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()
	err = a.run(ctx, rest)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = domain.NewError(domain.CodeTimeout, "operation timed out after %s", opts.timeout)
		}
		renderer.Failure(err)
		return domain.ExitCode(err)
	}
	return 0
}

type globalOptions struct {
	mode    output.Mode
	verbose bool
	timeout time.Duration
}

func parseGlobal(args []string, stderr io.Writer) (globalOptions, []string, error) {
	o := globalOptions{timeout: 10 * time.Second}
	fs := flag.NewFlagSet("bt", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "print JSON")
	quiet := fs.Bool("quiet", false, "suppress successful output")
	fs.BoolVar(&o.verbose, "verbose", false, "print diagnostics")
	fs.DurationVar(&o.timeout, "timeout", o.timeout, "operation timeout")
	if err := fs.Parse(args); err != nil {
		return o, nil, domain.NewError(domain.CodeUsage, "%v", err)
	}
	if *jsonOut && *quiet {
		return o, nil, domain.NewError(domain.CodeUsage, "--json and --quiet cannot be combined")
	}
	if *jsonOut {
		o.mode = output.JSON
	}
	if *quiet {
		o.mode = output.Quiet
	}
	return o, fs.Args(), nil
}

func (a application) run(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		return a.render.Data(helpText)
	}
	if args[0] == "--version" || args[0] == "version" {
		return a.render.Data("bt " + a.version)
	}
	switch args[0] {
	case "scan":
		devices, err := a.scan(ctx)
		if err != nil {
			return err
		}
		return a.render.Data(devices)
	case "list":
		devices, err := a.backend.List(ctx)
		if err != nil {
			return err
		}
		return a.render.Data(devices)
	case "pair", "unpair", "connect", "disconnect":
		return a.mutate(ctx, args[0], args[1:])
	case "gatt":
		return a.gatt(ctx, args[1:])
	case "capabilities":
		return a.render.Data(a.backend.Capabilities())
	default:
		return a.device(ctx, args)
	}
}

func (a application) scan(ctx context.Context) ([]domain.Device, error) {
	type result struct {
		devices []domain.Device
		err     error
	}
	ch := make(chan result, 2)
	go func() { d, e := a.backend.Scan(ctx); ch <- result{d, e} }()
	go func() { d, e := a.ble.Scan(ctx); ch <- result{d, e} }()
	var all []domain.Device
	var errs []error
	for range 2 {
		r := <-ch
		if r.err != nil {
			errs = append(errs, r.err)
		} else {
			all = merge(all, r.devices)
		}
	}
	if len(all) == 0 && len(errs) > 0 {
		return nil, errs[0]
	}
	return all, nil
}
func merge(dst, src []domain.Device) []domain.Device {
	for _, candidate := range src {
		found := -1
		for i, d := range dst {
			if d.ID == candidate.ID || (candidate.Name != "" && strings.EqualFold(d.Name, candidate.Name)) {
				found = i
				break
			}
		}
		if found < 0 {
			dst = append(dst, candidate)
			continue
		}
		d := &dst[found]
		d.Endpoints = append(d.Endpoints, candidate.Endpoints...)
		if candidate.RSSI != nil {
			d.RSSI = candidate.RSSI
		}
		if len(candidate.Services) > 0 {
			d.Services = candidate.Services
		}
		d.Paired = d.Paired || candidate.Paired
		d.Connected = d.Connected || candidate.Connected
	}
	return dst
}
func (a application) inventory(ctx context.Context, discover bool) ([]domain.Device, error) {
	if !discover {
		return a.backend.List(ctx)
	}
	scanCtx, cancel := context.WithTimeout(ctx, min(4*time.Second, a.timeout/2))
	defer cancel()
	return a.scan(scanCtx)
}
func (a application) mutate(ctx context.Context, action string, args []string) error {
	if len(args) != 1 {
		return domain.NewError(domain.CodeUsage, "usage: bt %s <device>", action)
	}
	devices, err := a.inventory(ctx, action == "pair")
	if err != nil {
		return err
	}
	d, err := resolver.Resolve(devices, args[0])
	if err != nil {
		return err
	}
	switch action {
	case "pair":
		err = a.backend.Pair(ctx, d)
	case "unpair":
		err = a.backend.Unpair(ctx, d)
	case "connect":
		err = a.backend.Connect(ctx, d)
	case "disconnect":
		err = a.backend.Disconnect(ctx, d)
	}
	return err
}
func (a application) device(ctx context.Context, args []string) error {
	devices, err := a.inventory(ctx, true)
	if err != nil {
		return err
	}
	d, err := resolver.Resolve(devices, args[0])
	if err != nil {
		return err
	}
	if len(args) == 1 {
		if address, addressErr := bleAddress(d); addressErr == nil {
			if info, readErr := a.ble.DeviceInformation(ctx, address); readErr == nil {
				d.Information = info
			}
		}
		return a.render.Data(d)
	}
	if len(args) == 2 && args[1] == "battery" {
		address, err := bleAddress(d)
		if err != nil {
			return err
		}
		value, err := a.ble.Read(ctx, address, "2a19")
		if err != nil {
			return err
		}
		if len(value) != 1 {
			return fmt.Errorf("invalid Battery Level value")
		}
		if a.render.Mode == output.Human {
			return a.render.Data(fmt.Sprintf("%d%%", value[0]))
		}
		return a.render.Data(map[string]any{"device": d, "property": "battery", "value": value[0], "unit": "%"})
	}
	if len(args) == 2 {
		switch args[1] {
		case "pair", "unpair", "connect", "disconnect":
			return a.mutate(ctx, args[1], []string{d.ID})
		default:
			return domain.NewError(domain.CodeUsage, "unknown device command %q", args[1])
		}
	}
	return domain.NewError(domain.CodeUsage, "unknown device command")
}
func (a application) gatt(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return domain.NewError(domain.CodeUsage, "usage: bt gatt <device> <services|characteristics|read|write|watch>")
	}
	devices, err := a.inventory(ctx, true)
	if err != nil {
		return err
	}
	d, err := resolver.Resolve(devices, args[0])
	if err != nil {
		return err
	}
	address, err := bleAddress(d)
	if err != nil {
		return err
	}
	switch args[1] {
	case "services":
		v, e := a.ble.Services(ctx, address)
		if e != nil {
			return e
		}
		return a.render.Data(v)
	case "characteristics":
		service := ""
		if len(args) > 2 {
			service = args[2]
		}
		v, e := a.ble.Characteristics(ctx, address, service)
		if e != nil {
			return e
		}
		return a.render.Data(v)
	case "read":
		if len(args) != 3 {
			return domain.NewError(domain.CodeUsage, "usage: bt gatt <device> read <uuid>")
		}
		v, e := a.ble.Read(ctx, address, args[2])
		if e != nil {
			return e
		}
		return a.render.Data(hex.EncodeToString(v))
	case "write":
		if len(args) < 4 {
			return domain.NewError(domain.CodeUsage, "usage: bt gatt <device> write <uuid> <hex> [--without-response]")
		}
		value, e := hex.DecodeString(strings.TrimPrefix(args[3], "0x"))
		if e != nil {
			return domain.NewError(domain.CodeUsage, "invalid hexadecimal value")
		}
		without := false
		if len(args) > 4 {
			switch args[4] {
			case "--without-response":
				without = true
			case "--with-response":
			default:
				return domain.NewError(domain.CodeUsage, "unknown write option %q", args[4])
			}
		}
		return a.ble.Write(ctx, address, args[2], value, without)
	case "watch":
		if len(args) != 3 {
			return domain.NewError(domain.CodeUsage, "usage: bt gatt <device> watch <uuid>")
		}
		return a.ble.Watch(ctx, address, args[2], func(v []byte) error { return a.render.Data(hex.EncodeToString(v)) })
	default:
		return domain.NewError(domain.CodeUsage, "unknown GATT command %q", args[1])
	}
}
func bleAddress(d domain.Device) (string, error) {
	for _, e := range d.Endpoints {
		if e.Transport == domain.TransportBLE {
			return e.Address, nil
		}
	}
	return "", domain.NewError(domain.CodeUnsupported, "%s has no discoverable BLE endpoint", d.Name)
}
func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

const helpText = `Betty (bt) — a small cross-platform Bluetooth CLI

Usage:
  bt [--json|--quiet] [--timeout 10s] <command>

Commands:
  scan                         discover nearby devices
  list                         list devices known to the OS
  pair|unpair <device>         change pairing state
  connect|disconnect <device>  change OS-managed connection state
  <device>                     inspect a device
  <device> battery             read the standard BLE battery level
  gatt <device> services
  gatt <device> characteristics [service-uuid]
  gatt <device> read <characteristic-uuid>
  gatt <device> write <characteristic-uuid> <hex> [--without-response]
  gatt <device> watch <characteristic-uuid>`
