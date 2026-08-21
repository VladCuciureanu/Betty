package output

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/vladcuciureanu/betty/internal/domain"
)

type Mode int

const (
	Human Mode = iota
	JSON
	Quiet
)

type Renderer struct {
	Out, Err io.Writer
	Mode     Mode
	Verbose  bool
}
type envelope struct {
	SchemaVersion int           `json:"schema_version"`
	Data          any           `json:"data,omitempty"`
	Error         *domain.Error `json:"error,omitempty"`
}

func (r Renderer) Data(data any) error {
	if r.Mode == Quiet {
		return nil
	}
	if r.Mode == JSON {
		return json.NewEncoder(r.Out).Encode(envelope{SchemaVersion: 1, Data: data})
	}
	return r.human(data)
}
func (r Renderer) Failure(err error) {
	e := domain.AsError(err)
	if r.Mode == JSON {
		_ = json.NewEncoder(r.Err).Encode(envelope{SchemaVersion: 1, Error: e})
		return
	}
	fmt.Fprintln(r.Err, "bt:", e.Message)
}
func (r Renderer) human(data any) error {
	switch v := data.(type) {
	case []domain.Device:
		sort.Slice(v, func(i, j int) bool { return v[i].Name < v[j].Name })
		for _, d := range v {
			state := ""
			if d.Connected {
				state = "  connected"
			} else if d.Paired {
				state = "  paired"
			}
			rssi := ""
			if d.RSSI != nil {
				rssi = fmt.Sprintf("  %d dBm", *d.RSSI)
			}
			fmt.Fprintf(r.Out, "%s%s%s\n  %s\n", d.Name, state, rssi, d.ID)
		}
		return nil
	case domain.Device:
		fmt.Fprintln(r.Out, v.Name)
		fmt.Fprintf(r.Out, "id          %s\npaired      %t\nconnected   %t\n", v.ID, v.Paired, v.Connected)
		if v.RSSI != nil {
			fmt.Fprintf(r.Out, "rssi        %d dBm\n", *v.RSSI)
		}
		if len(v.Services) > 0 {
			fmt.Fprintln(r.Out, "services")
			for _, s := range v.Services {
				fmt.Fprintln(r.Out, "  "+s)
			}
		}
		if len(v.Information) > 0 {
			keys := make([]string, 0, len(v.Information))
			for key := range v.Information {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				fmt.Fprintf(r.Out, "%-12s%s\n", key, v.Information[key])
			}
		}
		return nil
	case string:
		fmt.Fprintln(r.Out, v)
		return nil
	case []string:
		for _, line := range v {
			fmt.Fprintln(r.Out, line)
		}
		return nil
	default:
		bytes, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(r.Out, string(bytes))
		return nil
	}
}
