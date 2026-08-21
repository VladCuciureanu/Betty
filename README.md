# Betty (`bt`)

Betty is a small cross-platform CLI for finding and controlling Bluetooth devices.

```console
bt scan
bt pair keyboard
bt airpods battery
bt gatt sensor services
```

Betty uses native operating-system Bluetooth APIs for saved devices, pairing, and profile connections. Generic BLE scanning and GATT access use a pinned, isolated version of `tinygo.org/x/bluetooth`. Normal commands never access the network and Betty does not run a daemon.

## Install from source

Go 1.24 or newer is required. macOS builds also require Xcode command-line tools.

```console
go install github.com/vladcuciureanu/betty/cmd/bt@latest
```

For a small local release build:

```console
go build -trimpath -ldflags="-s -w" -o bt ./cmd/bt
```

## Commands

```text
bt scan
bt list
bt pair <device>
bt unpair <device>
bt connect <device>
bt disconnect <device>
bt <device>
bt <device> battery

bt gatt <device> services
bt gatt <device> characteristics [service-uuid]
bt gatt <device> read <characteristic-uuid>
bt gatt <device> write <characteristic-uuid> <hex> [--without-response]
bt gatt <device> watch <characteristic-uuid>
```

Global flags must precede the command: `--json`, `--quiet`, `--verbose`, and `--timeout=10s`.

Device matching tries stable ID, exact name, case-insensitive name, then an unambiguous partial name. Stable IDs are opaque and local to the operating system.

## Platform behavior

| Operation | Linux/BlueZ | macOS | Windows 10/11 |
| --- | --- | --- | --- |
| BLE scan and GATT | Supported | Supported | Supported |
| Saved-device listing | Supported | Supported | Supported |
| Pair | Supported | Supported | Supported; Windows may show UI |
| Unpair | Supported | Not exposed by a supported public API | Supported |
| Generic OS profile connect/disconnect | Supported | Supported | Not reliably exposed for every profile |

Unsupported operations fail with a stable `unsupported` error instead of reporting false success. Linux needs a running BlueZ service and permission to use its system D-Bus API. Bluetooth permissions must be granted to the terminal application on macOS.

## JSON and exit codes

JSON results use a versioned envelope:

```json
{"schema_version":1,"data":[]}
```

Errors use the same envelope with a structured `error`. Exit codes are `2` usage, `3` not found, `4` ambiguous, `5` unsupported, `6` permission, `7` unavailable, `8` authentication, and `9` timeout. Other failures return `1`.

## Development

```console
go test ./...
go vet ./...
```

Hardware tests are intentionally separate from the default suite. Real releases require validation with a Classic device and a generic BLE peripheral on all three operating systems.

Set `BETTY_BINARY` to the built executable and opt in with disposable hardware:

```console
BETTY_HARDWARE_BLE=sensor BETTY_HARDWARE_CLASSIC=keyboard go test -tags=hardware ./hardware
```

The Classic lifecycle test pairs, connects, disconnects, and unpairs the selected device. Do not point it at hardware whose pairing state must be preserved.
