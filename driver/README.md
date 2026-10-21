# Writing a Betty driver

A driver teaches Betty about one family of devices. It is a separate
executable: Betty starts it, sends it JSON requests over stdin, and reads JSON
responses from stdout. Drivers add properties and capabilities to Betty's
generic `inspect`, `get`, `set`, and `watch` commands; they never add vendor
commands to the CLI.

- **Any language:** implement the
  [driver protocol specification](../docs/DRIVER_PROTOCOL.md). It includes a
  complete Python example.
- **Go:** use this package. `driver.Serve` implements the protocol, so you only
  write handlers.

## Go quickstart

```go
package main

import (
	"encoding/json"
	"strings"

	"github.com/vladcuciureanu/betty/driver"
)

func main() {
	driver.Serve(driver.Driver{
		Manifest: driver.Manifest{
			ID:          "example/acme",
			Name:        "ACME Gadgets",
			Version:     "0.1.0",
			Permissions: []string{driver.PermissionRFCOMM},
			// Betty starts the driver only for requests with a matching device.
			Match: &driver.Match{NameContains: []string{"ACME"}},
		},
		Inspect: inspect,
		Get:     get,
	})
}

// inspect identifies ACME devices. It runs on every scan and list, so it must
// not open connections.
func inspect(_ *driver.Session, request driver.InspectRequest) (map[string]driver.Enrichment, error) {
	enrichments := map[string]driver.Enrichment{}
	for _, device := range request.Devices {
		if !strings.HasPrefix(device.Name, "ACME-") {
			continue
		}
		enrichments[device.ID] = driver.Enrichment{
			Identification: &driver.Identification{Vendor: "ACME", Family: "Gadget", Confidence: driver.ConfidenceName},
			Capabilities: map[string]driver.Capability{
				"battery": {Status: driver.StatusAvailable, Kind: driver.KindBattery, Readable: true},
			},
		}
	}
	return enrichments, nil
}

// get reads a property. Returning nil, nil means "not mine" and lets another
// driver answer.
func get(host *driver.Session, request driver.PropertyRequest) (*driver.Enrichment, error) {
	if request.Property != "battery" || !strings.HasPrefix(request.Device.Name, "ACME-") {
		return nil, nil
	}
	if !request.Device.Connected {
		return nil, driver.Errorf(driver.CodeDeviceDisconnected, "%s is not connected", request.Device.Name)
	}
	channel, err := host.OpenRFCOMM(request.Device.ID, "00001101-0000-1000-8000-00805f9b34fb")
	if err != nil {
		return nil, driver.Wrap(err, driver.CodeBluetoothUnavailable)
	}
	defer host.CloseRFCOMM(channel)
	level, err := readBatteryLevel(host, channel) // your device protocol
	if err != nil {
		return nil, driver.Wrap(err, driver.CodeBluetoothUnavailable)
	}
	value, _ := json.Marshal(level)
	return &driver.Enrichment{Properties: map[string]driver.Property{"battery": {Value: value, Unit: "%"}}}, nil
}
```

Build it, install it, and use it through the normal CLI:

```console
go build -o bt-driver-acme .
bt driver install ./bt-driver-acme
bt get "ACME-42" battery
```

## The SDK

| Piece | What it does |
| --- | --- |
| `Driver` | Your manifest and the optional `Inspect`, `Get`, `Set`, and `Watch` handlers. `Watch` pushes each change to `bt watch`; without it, `bt watch` polls `Get`. |
| `Serve(d)` | Runs the driver: answers `--manifest`, handles requests until Betty closes stdin, and exits. |
| `Driver.Run(args, in, out)` | The same over arbitrary streams, for tests. |
| `*Session` | Host calls for the current request: `OpenL2CAP`, `ReadL2CAP`, `WriteL2CAP`, `CloseL2CAP`, the same four for RFCOMM, `GATTServices`, `ReadGATT`, `WriteGATT`, `SubscribeGATT`, `NextGATT`, `UnsubscribeGATT`, `GetState`, and `SetState`. |
| `Errorf(code, …)` | Returns a structured error with a stable code. |
| `Wrap(err, fallback)` | Keeps the code of a failed host call, or applies `fallback`. |
| `PropertyRequest.DecodeValue` | Decodes a `set` value, reporting `usage` for the wrong JSON type. |
| Constants | Methods, purposes, permissions, capability statuses and types, confidence levels, and every `ErrorCode`. |

Handler rules, which `Serve` enforces where it can:

- A missing handler, or a `nil` enrichment, means the request isn't yours.
  Betty then asks other drivers. Return an error only for a device and property
  you own.
- `Get` and `Set` run only when the target device is in the request.
  `request.Device` is already resolved.
- `Set` must return state read back from the device, never the requested
  value.
- A plain `error` becomes `internal`, and a panic becomes an `internal` error
  response; neither crashes the session.
- Host calls are synchronous and sequential. Don't call `Session` from more
  than one goroutine.

## Testing a driver

Handlers are plain functions, so unit-test them directly with `nil` for the
session when they make no host calls. To test the wire behavior, pipe encoded
requests into `Driver.Run` and decode its output. See
[`serve_test.go`](serve_test.go) and the Sony driver's
[`sony_test.go`](../cmd/bt-driver-sony/sony_test.go).

Before installing a driver, in any language, check it against the protocol:

```console
bt driver check ./my-driver
```

The check runs the driver as Betty would, with scripted requests and a host
that refuses every host call, and reports each rule it tests: a valid
manifest, a response for each request with the request's ID, serving several
requests in one process, `unsupported` for an unknown method, not claiming
properties it doesn't own, exiting when its input ends, and host calls only
within the manifest's permissions. It needs no Bluetooth.

## Reference drivers

- [Sony Headphones](../cmd/bt-driver-sony): built on `driver.Serve`, using
  RFCOMM host calls.
- [Apple Audio](../cmd/bt-driver-apple-audio): built on `driver.Serve`, using
  L2CAP and driver-storage host calls.

## Security status

Executable drivers are trusted native code. Manifest permissions and the host
API prevent accidental access, but they are not a sandbox. Install only drivers
you trust. See the [security model](../docs/DRIVER_PROTOCOL.md#12-security-model).
