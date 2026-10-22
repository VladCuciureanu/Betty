// Package output renders command results and failures as human text or as
// schema-versioned JSON.
package output

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/vladcuciureanu/betty/internal/domain"
)

// SchemaVersion is the version of every JSON envelope Betty prints.
const SchemaVersion = 1

type Mode int

const (
	Human Mode = iota
	JSON
	Quiet
)

// Renderer writes results to Out and failures, warnings, and diagnostics to
// Err. In JSON mode every line on either stream is one JSON envelope.
type Renderer struct {
	Out, Err io.Writer
	Mode     Mode
	Verbose  bool
	// Sink, when set, receives results and warnings instead of Out and Err,
	// as bt serve does for each request. Diagnostics still go to Err.
	Sink Sink
}

// Sink receives a command's results and warnings.
type Sink interface {
	Data(any) error
	Warn(string)
}

// HumanWriter is implemented by results that print themselves in human mode.
type HumanWriter interface {
	WriteHuman(io.Writer) error
}

type envelope struct {
	SchemaVersion int           `json:"schema_version"`
	Data          any           `json:"data,omitempty"`
	Error         *domain.Error `json:"error,omitempty"`
	Warning       *message      `json:"warning,omitempty"`
	Debug         *message      `json:"debug,omitempty"`
}

type message struct {
	Message string `json:"message"`
}

// Data prints a successful result. Quiet mode prints nothing.
func (r Renderer) Data(data any) error {
	if r.Sink != nil {
		return r.Sink.Data(data)
	}
	switch r.Mode {
	case Quiet:
		return nil
	case JSON:
		return json.NewEncoder(r.Out).Encode(envelope{SchemaVersion: SchemaVersion, Data: data})
	default:
		return r.human(data)
	}
}

// Warn reports a problem that did not stop the command, in every mode.
func (r Renderer) Warn(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	if r.Sink != nil {
		r.Sink.Warn(text)
		return
	}
	if r.Mode == JSON {
		_ = json.NewEncoder(r.Err).Encode(envelope{SchemaVersion: SchemaVersion, Warning: &message{text}})
		return
	}
	fmt.Fprintln(r.Err, "bt: warning:", text)
}

// Debugf reports a diagnostic, only when Verbose is set.
func (r Renderer) Debugf(format string, args ...any) {
	if !r.Verbose {
		return
	}
	text := fmt.Sprintf(format, args...)
	if r.Mode == JSON {
		_ = json.NewEncoder(r.Err).Encode(envelope{SchemaVersion: SchemaVersion, Debug: &message{text}})
		return
	}
	fmt.Fprintln(r.Err, "bt: debug:", text)
}

// Failure prints err. In human mode it also lists the candidates of an
// ambiguous device match, so the user can retry with an exact ID.
func (r Renderer) Failure(err error) {
	e := domain.AsError(err)
	if r.Mode == JSON {
		_ = json.NewEncoder(r.Err).Encode(envelope{SchemaVersion: SchemaVersion, Error: e})
		return
	}
	fmt.Fprintln(r.Err, "bt:", e.Message)
	if details, ok := e.Details.(domain.AmbiguityDetails); ok {
		fmt.Fprintln(r.Err, "matching devices:")
		for _, match := range details.Matches {
			fmt.Fprintf(r.Err, "  %-28s %s%s\n", match.ID, match.Name, deviceState(match.Paired, match.Connected))
		}
	}
}

func (r Renderer) human(data any) error {
	switch v := data.(type) {
	case HumanWriter:
		return v.WriteHuman(r.Out)
	case []domain.Device:
		return writeDevices(r.Out, v)
	case domain.Device:
		return writeDevice(r.Out, v)
	case string:
		_, err := fmt.Fprintln(r.Out, v)
		return err
	default:
		bytes, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(r.Out, string(bytes))
		return err
	}
}

func writeDevices(w io.Writer, devices []domain.Device) error {
	sorted := slices.Clone(devices)
	slices.SortStableFunc(sorted, func(a, b domain.Device) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.ID, b.ID))
	})
	for _, d := range sorted {
		rssi := ""
		if d.RSSI != nil {
			rssi = fmt.Sprintf("  %d dBm", *d.RSSI)
		}
		if _, err := fmt.Fprintf(w, "%s%s%s\n  %s\n", d.Name, deviceState(d.Paired, d.Connected), rssi, d.ID); err != nil {
			return err
		}
	}
	return nil
}

func writeDevice(w io.Writer, d domain.Device) error {
	fmt.Fprintln(w, d.Name)
	fmt.Fprintf(w, "%-12s%s\n%-12s%t\n%-12s%t\n", "id", d.ID, "paired", d.Paired, "connected", d.Connected)
	for _, endpoint := range d.Endpoints {
		if endpoint.Address != "" {
			fmt.Fprintf(w, "%-12s%s (%s)\n", "address", endpoint.Address, endpoint.Transport)
		}
	}
	if d.RSSI != nil {
		fmt.Fprintf(w, "%-12s%d dBm\n", "rssi", *d.RSSI)
	}
	if d.Identification != nil {
		model := strings.TrimSpace(d.Identification.Vendor + " " + d.Identification.Model)
		fmt.Fprintf(w, "%-12s%s (%s)\n", "identified", model, d.Identification.Confidence)
	}
	if len(d.Services) > 0 {
		fmt.Fprintln(w, "services")
		for _, service := range d.Services {
			fmt.Fprintln(w, "  "+service)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(d.Information)) {
		fmt.Fprintf(w, "%-12s%s\n", key, d.Information[key])
	}
	for _, key := range slices.Sorted(maps.Keys(d.Properties)) {
		property := d.Properties[key]
		display := property.Display
		if display == "" {
			display = string(property.Value)
		}
		fmt.Fprintf(w, "%-12s%s\n", key, display)
	}
	if len(d.Capabilities) > 0 {
		fmt.Fprintln(w, "capabilities")
		for _, key := range slices.Sorted(maps.Keys(d.Capabilities)) {
			capability := d.Capabilities[key]
			fmt.Fprintf(w, "  %-18s %s", key, capability.Status)
			if len(capability.Values) > 0 {
				fmt.Fprintf(w, " (%s)", strings.Join(capability.Values, ", "))
			}
			if capability.RequiresConnection {
				fmt.Fprint(w, ", needs a connection")
			}
			fmt.Fprintln(w)
		}
	}
	return nil
}

func deviceState(paired, connected bool) string {
	switch {
	case connected:
		return "  connected"
	case paired:
		return "  paired"
	default:
		return ""
	}
}
