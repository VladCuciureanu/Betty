# Betty

Betty is a small, cross-platform Bluetooth command-line utility. Its executable
is called `bt`.

```console
bt scan
bt list
bt connect "Desk Speaker"
bt get "AirPods Pro" battery
bt gatt sensor services
```

Betty gives people and applications one predictable interface for Bluetooth on
macOS, Linux, and Windows. It uses native operating-system APIs for saved
devices, pairing, and profile connections, while generic BLE discovery and GATT
operations are isolated behind `tinygo.org/x/bluetooth`.

There is no daemon, account, cloud service, or network request. Normal commands
run locally and exit.

> [!NOTE]
> Betty is under active development. The implementation and automated tests are
> substantial, but the native hardware evidence required for a stable release
> is still being collected in [COMPATIBILITY.md](COMPATIBILITY.md).

## Why Betty?

Bluetooth automation is surprisingly platform-specific. Device names are
ambiguous, BLE and Classic endpoints do not always share an identity, and an OS
may expose some connection operations but not others. Betty keeps those details
behind a compact interface without pretending the limitations do not exist.

- One command vocabulary across macOS, Linux, and Windows.
- Deterministic device selection that refuses ambiguous matches.
- Human output for terminals and stable JSON envelopes for applications.
- Generic BLE/GATT access without vendor-specific commands.
- Optional device drivers that do not bloat the base executable.
- Explicit `unsupported`, permission, interaction, adapter, and timeout errors.

Betty is a standalone CLI with a stable subprocess API for local applications
and scripts.

## Install

Published package instructions will be added with the first validated release.
For now, Go 1.24 or newer is required; macOS builds also require the Xcode
command-line tools.

```console
go install github.com/vladcuciureanu/betty/cmd/bt@latest
```

Or build a small local binary from a checkout:

```console
go build -trimpath -ldflags="-s -w" -o bt ./cmd/bt
```

Packaging is already defined for
[Homebrew](packaging/homebrew/README.md),
[Debian](packaging/linux/README.md), and
[Windows MSI](packaging/windows/README.md). Vendor support is packaged
separately from the base utility.

## Everyday use

Discover nearby devices or list devices already known to the operating system:

```console
bt scan
bt list
```

Betty accepts an opaque stable ID, an exact name, a case-insensitive name, or a
unique partial name. It never silently chooses between ambiguous matches.

```console
bt pair keyboard
bt connect headphones
bt inspect headphones
bt disconnect headphones
```

Read, change, or watch normalized properties exposed by the device and its
installed optional driver:

```console
bt get headphones battery
bt get headphones noise_control
bt set headphones noise_control anc
bt watch headphones battery
```

Successful mutating commands are silent by default, which keeps scripts simple.

On macOS, Betty also reads battery percentages published by the system HID
stack for connected Bluetooth keyboards, mice, and trackpads. It uses native
IOHID/IOKit APIs and correlates the reading by the exact endpoint address; it
does not run `ioreg`, parse command output, or guess from a device name.

### Generic BLE and GATT

```console
bt gatt sensor services
bt gatt sensor characteristics
bt gatt sensor characteristics 180f
bt gatt sensor read 2a19
bt gatt sensor write <characteristic-uuid> 01
bt gatt sensor watch <characteristic-uuid>
```

UUIDs may be written in canonical 16-bit or 128-bit form. Writes choose a mode
supported by the characteristic unless `--with-response` or
`--without-response` is supplied. Watches run until cancellation and release
their Bluetooth resources on exit.

Run `bt help` for the complete command list.

## Built for automation

Global flags precede the command:

```console
bt --json --non-interactive --timeout=15s get headphones battery
```

- `--json` emits schema-versioned JSON and keeps diagnostics off stdout.
- `--quiet` suppresses successful output.
- `--verbose` includes additional diagnostics.
- `--non-interactive` fails clearly when Betty would need terminal input.
- `--timeout` bounds ordinary operations; watches continue until cancelled.

JSON uses a versioned envelope:

```json
{"schema_version":1,"data":{"device_id":"opaque-id","property":"noise_control","value":"anc","estimated":false,"resolution":0}}
```

The stable value shapes and error behavior are documented in
[the JSON API contract](docs/JSON_API.md).

Watch output is newline-delimited JSON. It starts with a `snapshot` event and
then emits `changed` events. Clients should ignore fields they do not recognize.

Errors use the same envelope and include a stable code, message, retryability,
and optional details. Exit codes distinguish usage, not found, ambiguity,
unsupported features, permissions, unavailable Bluetooth, authentication or
interaction, and timeouts.

## Optional device support

Betty's base executable contains no Apple, Beats, or Sony packet parser or model
table. Device-specific functionality lives in optional executable drivers,
while the public CLI remains `inspect`, `get`, `set`, and `watch`.

| Driver | Devices | Available today | Details |
| --- | --- | --- | --- |
| Apple Audio | AirPods and Beats | identification, passive and exact battery paths | [capabilities and platform behavior](cmd/bt-driver-apple-audio/README.md) |
| Sony Headphones | Sony WH/WF/WI/MDR, LinkBuds, and ULT families | identification and v2 battery | [capabilities and tested protocol scope](cmd/bt-driver-sony/README.md) |

Build and install a driver from source:

```console
go build -o bt-driver-apple-audio ./cmd/bt-driver-apple-audio
bt driver install ./bt-driver-apple-audio
bt driver list
```

Drivers are trusted native executables in the current design. Betty verifies
their manifests, limits host operations to declared permissions and selected
device IDs, and isolates persistent state by driver, but it does not yet provide
a security sandbox. The wire protocol and host boundary are documented in
[driver/README.md](driver/README.md).

## Platform notes

| Operation | Linux / BlueZ | macOS | Windows 10/11 |
| --- | --- | --- | --- |
| BLE scan and GATT | Implemented | Implemented | Implemented |
| Saved-device listing | Implemented | Implemented | Implemented |
| Pair | Implemented | Implemented | Implemented; Windows may show UI |
| Unpair | Implemented | Public API unavailable | Implemented |
| Generic profile connect/disconnect | Implemented | Implemented | Not reliably available for every profile |

An unavailable platform operation returns `unsupported`; Betty does not report
success merely because a request was submitted. Stable device IDs are opaque,
platform-local values and must not be interpreted as MAC addresses.

See [COMPATIBILITY.md](COMPATIBILITY.md) for verified native hardware evidence
and unavoidable limitations.

## Development

```console
go test ./...
go vet ./...
```

Hardware tests are opt-in because some pair, connect, disconnect, and unpair a
disposable device:

```console
BETTY_BINARY="$PWD/bt" \
BETTY_HARDWARE_BLE=sensor \
BETTY_HARDWARE_CLASSIC=keyboard \
go test -tags=hardware ./hardware
```

The ordered release plan and remaining hardware/signing work live in
[FUTURE_WORK.md](FUTURE_WORK.md).

## License

Betty is released under the [MIT License](LICENSE).
