package app

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/vladcuciureanu/betty/internal/backend"
	"github.com/vladcuciureanu/betty/internal/domain"
)

type adapterList []domain.Adapter

func (adapters adapterList) WriteHuman(w io.Writer) error {
	for _, adapter := range adapters {
		if err := writeAdapter(w, adapter); err != nil {
			return err
		}
	}
	return nil
}

type adapterState domain.Adapter

func (a adapterState) WriteHuman(w io.Writer) error { return writeAdapter(w, domain.Adapter(a)) }

func writeAdapter(w io.Writer, adapter domain.Adapter) error {
	parts := []string{adapter.ID}
	if adapter.Name != "" {
		parts = append(parts, fmt.Sprintf("%q", adapter.Name))
	}
	if adapter.Address != "" {
		parts = append(parts, adapter.Address)
	}
	for _, flag := range []struct {
		name  string
		value *bool
	}{{"powered", adapter.Powered}, {"discoverable", adapter.Discoverable}, {"pairable", adapter.Pairable}} {
		switch {
		case flag.value == nil:
		case *flag.value:
			parts = append(parts, flag.name)
		default:
			parts = append(parts, "not "+flag.name)
		}
	}
	if adapter.Default {
		parts = append(parts, "(default)")
	}
	_, err := fmt.Fprintln(w, strings.Join(parts, "  "))
	return err
}

const adapterHelp = `Subcommands:
  bt adapter                       list local Bluetooth adapters
  bt adapter power on|off          turn the default adapter on or off
  bt adapter discoverable on|off   let nearby devices find this computer, or stop
  bt adapter name <name>           set the name nearby devices see

Changes print the adapter's state read back afterwards. Platforms differ:
Linux supports all of them, Windows only discoverable (its Bluetooth name is
the computer's name), and macOS none; the rest fail with unsupported.`

func (a *application) adapter(ctx context.Context, args []string) error {
	adapters, ok := a.backend.(backend.AdapterBackend)
	if !ok {
		return domain.NewError(domain.CodeUnsupported, "this platform backend cannot report the Bluetooth adapter")
	}
	if len(args) == 0 {
		list, err := adapters.Adapters(ctx)
		if err != nil {
			return err
		}
		return a.render.Data(adapterList(nonNil(list)))
	}
	if len(args) != 2 {
		return domain.NewError(domain.CodeUsage, "usage: bt adapter [power on|off | discoverable on|off | name <name>]")
	}
	var change domain.AdapterChange
	switch args[0] {
	case "power", "discoverable":
		var on bool
		switch args[1] {
		case "on":
			on = true
		case "off":
		default:
			return domain.NewError(domain.CodeUsage, "bt adapter %s takes on or off, not %q", args[0], args[1])
		}
		if args[0] == "power" {
			change.Powered = &on
		} else {
			change.Discoverable = &on
		}
	case "name":
		if strings.TrimSpace(args[1]) == "" {
			return domain.NewError(domain.CodeUsage, "the adapter name must not be empty")
		}
		change.Name = &args[1]
	default:
		return domain.NewError(domain.CodeUsage, "unknown adapter setting %q; use power, discoverable, or name", args[0])
	}
	state, err := adapters.SetAdapter(ctx, change)
	if err != nil {
		return err
	}
	// Report success only for a state read back from the adapter.
	for _, setting := range []struct {
		name       string
		want, have *bool
	}{{"power", change.Powered, state.Powered}, {"discoverable", change.Discoverable, state.Discoverable}} {
		if setting.want != nil && setting.have != nil && *setting.want != *setting.have {
			return domain.NewError(domain.CodeUnavailable, "the adapter did not change %s to %s", setting.name, args[1])
		}
	}
	if change.Name != nil && state.Name != "" && state.Name != *change.Name {
		return domain.NewError(domain.CodeUnavailable, "the adapter is named %q, not %q", state.Name, *change.Name)
	}
	return a.render.Data(adapterState(state))
}
