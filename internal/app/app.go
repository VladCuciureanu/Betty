// Package app implements the bt command line.
package app

import (
	"context"
	"errors"
	"io"
	"os/signal"
	"syscall"
	"time"

	"github.com/vladcuciureanu/betty/internal/backend"
	"github.com/vladcuciureanu/betty/internal/ble"
	"github.com/vladcuciureanu/betty/internal/domain"
	"github.com/vladcuciureanu/betty/internal/driver"
	"github.com/vladcuciureanu/betty/internal/hidbattery"
	"github.com/vladcuciureanu/betty/internal/l2cap"
	"github.com/vladcuciureanu/betty/internal/output"
	"github.com/vladcuciureanu/betty/internal/rfcomm"
)

type application struct {
	backend backend.Backend
	ble     ble.Client
	drivers *driver.Manager     // nil disables optional drivers
	hid     hidbattery.Provider // nil disables HID battery readings
	render  output.Renderer
	opts    options
}

// Main runs bt with args and returns the process exit status.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer, version string) int {
	return run(args, stdout, stderr, version, func(a *application) {
		pairingInput := stdin
		if a.opts.nonInteractive {
			pairingInput = nil
		}
		a.backend = backend.New(backend.PairingIO{In: pairingInput, Out: stderr})
		client := ble.New()
		a.ble = client
		a.hid = hidbattery.New()
		a.drivers = driver.New()
		a.drivers.SetHost(l2cap.New())
		a.drivers.SetRFCOMMHost(rfcomm.New())
		a.drivers.SetGATTHost(gattHost{client})
		// One process per driver serves the whole command.
		a.drivers.SetReuse(true)
		a.drivers.OnInspectError = func(id string, err error) {
			a.render.Warn("driver %s failed to inspect devices: %v", id, err)
		}
		if a.opts.verbose {
			a.drivers.OnDriverLog = func(id, line string) {
				a.render.Debugf("driver %s: %s", id, line)
			}
		}
	})
}

// run is Main with the platform dependencies supplied by connect, which is
// called only once a command that needs them is about to run.
func run(args []string, stdout, stderr io.Writer, version string, connect func(*application)) int {
	opts, rest, err := parseArgs(args)
	a := &application{opts: opts, render: output.Renderer{Out: stdout, Err: stderr, Mode: opts.mode, Verbose: opts.verbose}}
	if err != nil {
		return a.fail(err)
	}
	if opts.version || (len(rest) > 0 && rest[0] == "version") {
		return a.fail(a.render.Data(versionResult{Version: version}))
	}
	if len(rest) == 0 || rest[0] == "help" {
		var topic *command
		if len(rest) > 1 {
			if topic = findCommand(rest[1]); topic == nil {
				return a.fail(unknownCommand(rest[1]))
			}
		}
		return a.fail(a.render.Data(helpResult{Text: helpText(topic)}))
	}
	c := findCommand(rest[0])
	if c == nil {
		return a.fail(unknownCommand(rest[0]))
	}
	if opts.help {
		return a.fail(a.render.Data(helpResult{Text: helpText(c)}))
	}
	args = rest[1:]
	if err := c.checkArgs(args); err != nil {
		return a.fail(err)
	}
	if err := validateFlags(opts, c, args); err != nil {
		return a.fail(err)
	}
	connect(a)

	interrupted, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, timeout := interrupted, time.Duration(0)
	if c.streams == nil || !c.streams(args) {
		timeout = a.commandTimeout(c)
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(interrupted, timeout)
		defer cancel()
	}
	err = c.run(a, ctx, args)
	if a.drivers != nil {
		a.drivers.Close()
	}
	if interrupted.Err() != nil && c.streams != nil && c.streams(args) {
		// Interrupting a stream is how it is meant to end.
		return 0
	}
	return a.fail(classifyContextError(err, interrupted, timeout))
}

func (a *application) commandTimeout(c *command) time.Duration {
	if c.name == "pair" && !a.opts.timeoutSet && !a.opts.nonInteractive {
		return defaultPairTimeout
	}
	return a.opts.timeout
}

// fail renders err, if any, and returns the exit status for it.
func (a *application) fail(err error) int {
	if err != nil {
		a.render.Failure(err)
	}
	return domain.ExitCode(err)
}

// classifyContextError turns a bare context error into a timeout or
// cancellation error. Errors that already carry a code are kept.
func classifyContextError(err error, interrupted context.Context, timeout time.Duration) error {
	if err == nil || domain.AsError(err).Code != domain.CodeInternal {
		return err
	}
	if interrupted.Err() != nil || errors.Is(err, context.Canceled) {
		e := domain.NewError(domain.CodeCanceled, "interrupted")
		e.Cause = err
		return e
	}
	if errors.Is(err, context.DeadlineExceeded) {
		e := domain.NewError(domain.CodeTimeout, "timed out after %s; use --timeout to allow longer", timeout)
		e.Cause = err
		return e
	}
	return err
}

func streamsAlways([]string) bool { return true }

// commands is the command table. Help is generated from it, in this order.
var commands = []command{
	{name: "scan", usage: "scan", summary: "discover nearby devices", maxArgs: 0, run: (*application).scan},
	{name: "list", usage: "list", summary: "list devices the OS already knows, without scanning", maxArgs: 0, run: (*application).list},
	{name: "inspect", usage: "inspect <device>", summary: "show a device's state, properties, and capabilities", minArgs: 1, maxArgs: 1, run: (*application).inspect},
	{name: "get", usage: "get <device> <property>", summary: "read a property, such as battery", minArgs: 2, maxArgs: 2, run: (*application).get},
	{name: "set", usage: "set <device> <property> <value>", summary: "change a property and print the verified result", minArgs: 3, maxArgs: 3, run: (*application).set},
	{name: "watch", usage: "watch <device> <property> [--interval 2s]", summary: "print a property whenever it changes, until interrupted", minArgs: 2, maxArgs: 2, run: (*application).watch, streams: streamsAlways},
	{name: "pair", usage: "pair <device>", summary: "pair with a device", minArgs: 1, maxArgs: 1, run: mutation("pair")},
	{name: "unpair", usage: "unpair <device>", summary: "remove a pairing", minArgs: 1, maxArgs: 1, run: mutation("unpair")},
	{name: "connect", usage: "connect <device>", summary: "connect a paired device", minArgs: 1, maxArgs: 1, run: mutation("connect")},
	{name: "disconnect", usage: "disconnect <device>", summary: "disconnect a device", minArgs: 1, maxArgs: 1, run: mutation("disconnect")},
	{name: "gatt", usage: "gatt <device> services|characteristics|read|write|watch ...", summary: "use BLE GATT directly; see bt help gatt", details: gattHelp, minArgs: 2, maxArgs: 4, run: (*application).gatt, streams: func(args []string) bool { return len(args) > 1 && args[1] == "watch" }},
	{name: "capabilities", usage: "capabilities", summary: "show which operations this platform supports", maxArgs: 0, run: (*application).capabilities},
	{name: "driver", usage: "driver list|install|remove|export-state|import-state|check ...", summary: "manage optional device drivers; see bt help driver", details: driverHelp, minArgs: 0, maxArgs: 3, run: (*application).driverCommand},
}

const gattHelp = `Subcommands:
  bt gatt <device> services                       list primary services
  bt gatt <device> characteristics [service]      list characteristics, optionally of one service
  bt gatt <device> read <characteristic>          read a value, printed as hex
  bt gatt <device> write <characteristic> <hex>   write a value [--without-response]
  bt gatt <device> watch <characteristic>         print notifications until interrupted

UUIDs may be 16-bit (2a19) or 128-bit. Writes use write-with-response
unless --without-response is given.`

const driverHelp = `Subcommands:
  bt driver list                          list available drivers
  bt driver install <executable>          install a driver for this user
  bt driver remove <id>                   remove a user-installed driver
  bt driver export-state <id> <file>      save a driver's private state (mode 0600)
  bt driver import-state <id> <file>      replace a driver's private state
  bt driver check <executable>            check a driver against the protocol, without installing it

See docs/DRIVER_PROTOCOL.md to write a driver.`
