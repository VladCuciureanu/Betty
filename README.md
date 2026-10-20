# Betty

Betty is one command for every Bluetooth device you own, on macOS, Linux, and
Windows. Its executable is called `bt`.

```console
bt list
bt get keyboard battery
bt connect "Desk Speaker"
bt set headphones noise_control anc
bt gatt sensor read 2a19
```

Betty uses each operating system's native Bluetooth APIs for saved devices,
pairing, and connections, and `tinygo.org/x/bluetooth` for BLE discovery and
GATT. Device-specific features, such as headphone noise control, come from
optional drivers that anyone can write.

There is no daemon, account, cloud service, or network request. Every command
runs locally and exits.

> [!WARNING]
> Betty is pre-1.0 and has not yet been validated on real hardware; see
> [COMPATIBILITY.md](COMPATIBILITY.md). Commands, JSON output, and the driver
> protocol may change incompatibly until 1.0.

## Why Betty?

Bluetooth tooling is split by OS (`bluetoothctl` is Linux-only, `blueutil`
macOS-only, and Windows has little that can be scripted), by layer (BLE
libraries are code, not tools), and by vendor (headphone features live in each
vendor's phone app). Betty puts them behind one interface:

- **The same commands on every OS,** with explicit `unsupported` errors where an
  OS cannot do something, never a false success.
- **Built for scripts:** schema-versioned JSON, stable error codes, and
  distinct exit codes.
- **Open device support:** drivers add properties such as battery or
  `noise_control` through a [language-neutral protocol](docs/DRIVER_PROTOCOL.md)
  without vendor-specific commands.

## Install

Packages will be published with the first validated release. Until then, build
from source with Go 1.24 or newer. macOS builds also need the Xcode command-line
tools.

```console
go install github.com/vladcuciureanu/betty/cmd/bt@latest
```

Packaging is defined for [Homebrew](packaging/homebrew/README.md),
[Debian](packaging/linux/README.md), and [Windows MSI](packaging/windows/README.md).

## Everyday use

`bt list` shows the devices the operating system already knows, instantly.
`bt scan` also discovers nearby devices, which takes a few seconds.

```console
bt list
bt scan
```

Wherever a command takes a `<device>`, you can give its ID, its Bluetooth
address in any common notation, its exact name, or a unique part of its name.
Betty never chooses between matches: when a query matches several devices, it
fails and lists them.

```console
bt pair keyboard
bt connect AA:BB:CC:DD:EE:FF
bt inspect keyboard
bt disconnect keyboard
```

Betty first looks for the device among the devices the OS already knows. It
scans only when the device is not there, or when an operation needs a BLE
endpoint that only a scan reports. Commands on paired devices therefore start
immediately.

`pair`, `unpair`, `connect`, and `disconnect` print the state they verified.
Asking for the state a device is already in succeeds without doing anything, so
scripts can repeat them safely.

### Properties

Read, change, or watch properties that a device or an installed driver
provides:

```console
bt get headphones battery
bt set headphones noise_control anc
bt watch headphones battery
```

Without any driver, Betty reads `battery` from the standard GATT Battery Service
and `manufacturer`, `model`, `serial_number`, `hardware_revision`,
`software_revision`, and `firmware_version` from the Device Information Service.
On macOS it also reads battery levels that the system HID stack publishes for
connected keyboards, mice, and trackpads, matched by exact address.

`set` prints the value the driver read back from the device after writing it.
`watch` prints the value once and again whenever it changes, polling every 2
seconds by default (`--interval`). It reports retryable errors as warnings and
keeps going, and it ends when interrupted.

### GATT

```console
bt gatt sensor services
bt gatt sensor characteristics 180f
bt gatt sensor read 2a19
bt gatt sensor write <characteristic-uuid> 01
bt gatt sensor write <characteristic-uuid> 01 --without-response
bt gatt sensor watch <characteristic-uuid>
```

UUIDs may be written in 16-bit or 128-bit form. Writes use write-with-response
unless `--without-response` is given. `watch` prints each notification until
interrupted and releases the device on exit.

Run `bt help` for every command, and `bt help <command>` for details.

## Built for automation

Flags may appear anywhere on the command line:

```console
bt get headphones battery --json --timeout 15s
```

| Flag | Effect |
| --- | --- |
| `--json` | Print one JSON envelope per result on stdout. Errors, warnings, and diagnostics go to stderr as JSON envelopes too. |
| `-q`, `--quiet` | Print nothing on success. |
| `-v`, `--verbose` | Print diagnostics, such as why a command decided to scan. |
| `--non-interactive` | Fail instead of prompting for a pairing passkey. Only Linux honors it today; Windows and macOS may still show their own pairing dialogs. |
| `--timeout <duration>` | Bound the command. The default is 10 seconds, or 60 seconds for `pair`, which may wait for a person. Streaming commands such as `watch` have no deadline. |

Every result uses the same envelope:

```json
{"schema_version":1,"data":{"device_id":"windows:AA:BB:CC:DD:EE:FF","property":"battery","value":80,"unit":"%","estimated":false}}
```

Failures use the same envelope with an `error` holding a stable `code`, a
`message`, `retryable`, and optional `details`. Problems that don't stop a
command, such as a BLE scan failing while the OS scan succeeds, print as
`warning` envelopes. See [the JSON API](docs/JSON_API.md) for every shape.

| Exit status | Meaning |
| --- | --- |
| 0 | Success |
| 1 | Internal error |
| 2 | Usage error |
| 3 | Device not found |
| 4 | Ambiguous device |
| 5 | Unsupported, or a driver is required |
| 6 | Permission denied |
| 7 | Bluetooth unavailable or device disconnected |
| 8 | Authentication failed, or a person must act |
| 9 | Timeout |
| 130 | Interrupted |

## Device drivers

Betty's base executable contains no vendor protocol code. Device-specific
support comes from optional driver executables, while the CLI stays `inspect`,
`get`, `set`, and `watch`.

| Driver | Devices | Status | Details |
| --- | --- | --- | --- |
| Apple Audio | AirPods and Beats | identification, estimated battery from advertisements, exact battery paths per platform | [capabilities and platform behavior](cmd/bt-driver-apple-audio/README.md) |
| Sony Headphones | Sony WH/WF/WI/MDR, LinkBuds, and ULT families | identification and protocol v2 battery | [capabilities and tested scope](cmd/bt-driver-sony/README.md) |

```console
go build -o bt-driver-sony ./cmd/bt-driver-sony
bt driver install ./bt-driver-sony
bt driver list
```

Anyone can write a driver, in any language: see
[Writing a Betty driver](driver/README.md) and the
[driver protocol specification](docs/DRIVER_PROTOCOL.md). Go drivers can use the
`driver.Serve` SDK.

Drivers are trusted native code and are not sandboxed. Betty limits what they
can do through its host API: only the host operations their manifest declares,
only the devices passed in the current request, and private storage per
driver.

## Platform support

| Operation | Linux / BlueZ | macOS | Windows 10/11 |
| --- | --- | --- | --- |
| BLE scan and GATT | Implemented | Implemented | Implemented |
| Known-device listing | Implemented | Paired devices | Implemented |
| Classic discovery | Implemented | Not implemented: only paired devices and BLE advertisements are found | Implemented |
| Pair | Implemented | Paired-list devices only, so new Classic devices cannot be paired yet | Implemented; Windows may show its own dialog |
| Unpair | Implemented | Unsupported: no public API | Implemented |
| Connect and disconnect | Implemented | Opens or closes the baseband link, not individual profiles | Unsupported: no public API for every profile |

An operation a platform cannot perform returns `unsupported`. Device IDs are
opaque and platform-local; don't parse them.

## Development

```console
go test ./...
go vet ./...
```

There are three test levels:

| Level | Runs against | Where |
| --- | --- | --- |
| Unit (`go test ./...`) | Fakes | Everywhere, in CI |
| [End-to-end](e2e/README.md) (`-tags=e2e`) | Scripted Bluetooth devices | Linux in CI, fully virtual; Windows and macOS by hand, with a USB dongle |
| Hardware (`-tags=hardware`) | Your own real devices | By hand |

Hardware tests are opt-in because some of them pair, connect, disconnect, and
unpair a disposable device:

```console
go build -o bt ./cmd/bt
BETTY_BINARY="$PWD/bt" \
BETTY_HARDWARE_BLE=sensor \
BETTY_HARDWARE_CLASSIC=keyboard \
go test -tags=hardware ./hardware
```

The roadmap is in [FUTURE_WORK.md](FUTURE_WORK.md), and the latest code and
documentation review in [REVIEW.md](REVIEW.md).

## License

Betty is released under the [MIT License](LICENSE).
