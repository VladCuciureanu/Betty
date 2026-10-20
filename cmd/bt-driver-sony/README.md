# Betty Sony Headphones driver

Optional Sony headphone support for [Betty](../../README.md). The driver is a
separate executable, `bt-driver-sony`; Sony protocol code and model handling are
not linked into the base `bt` binary.

## Capabilities

| Property | State | Values / shape | Current scope |
| --- | --- | --- | --- |
| `battery` | Available for v2 | one percentage, or left/right/case percentages, including charging state when reported | Connected device exposing Sony protocol v2 |
| `noise_control` | Implemented, validation-gated | `off`, `anc`, `ambient` | WH-CH720N and ULT Wear v2 layouts only |
| `ambient_level` | Implemented, validation-gated | integer `1` through `20` | WH-CH720N v2 layout only |
| `equalizer` | Planned | model-specific presets | Packet layout and hardware validation required |
| `dsee` | Planned | `off`, `on` | v2 models; hardware validation required |
| `sony.speak_to_chat` | Planned | `off`, `on` | v2 models; hardware validation required |

Controls remain `planned` during ordinary use until Betty-native hardware
evidence exists. Maintainers can opt into the implemented model-scoped paths:

```console
BETTY_VALIDATE_SONY_CONTROLS=1 bt get "WH-CH720N" noise_control
BETTY_VALIDATE_SONY_CONTROLS=1 bt set "WH-CH720N" ambient_level 10
```

Control writes first read current state, preserve unrelated fields, write the
new state, and require a matching device readback before reporting success.

## Device and protocol detection

The driver recognizes Sony's v1 and v2 service UUIDs. It can also offer
lower-confidence identification for names beginning with `WH-`, `WF-`, `WI-`,
`MDR-`, `LinkBuds`, or `ULT Wear`.

A familiar name does not prove protocol support. Exact battery access requires a
connected RFCOMM endpoint and a usable Sony protocol version.

The current control layouts are deliberately narrow:

- **WH-CH720N** uses the standard v2 ambient layout and supports a numeric
  ambient level from 1 to 20.
- **ULT Wear / WH-ULT900N** uses the v2 layout with an additional reserved byte;
  its current readback does not expose a numeric ambient level.
- **WH/WF-1000XM and other models** are not inferred from those layouts. Their
  controls remain planned until model-specific packet evidence and hardware
  validation exist.

## Platform behavior

The same userspace implementation runs on macOS, Linux, and Windows over native
RFCOMM transports.

- Linux uses BlueZ Bluetooth sockets.
- macOS uses the native IOBluetooth RFCOMM channel.
- Windows uses public Winsock Bluetooth APIs and resolves the service channel
  through SDP.

The Sony driver does not need a kernel driver, native helper DLL, root access,
or a background service on any platform.

## Driver permissions

Manifest ID: `official/sony-headphones`

Declared host permissions:

- `device-metadata`
- `rfcomm`

## Protocol research and provenance

Betty's implementation was written independently in Go and uses strict frame
length and checksum validation. These public implementations were consulted to
compare observable Sony protocol behavior:

- [SonyHeadphonesClient](https://github.com/Plutoberth/SonyHeadphonesClient): original public Sony headphone protocol investigation.
- [SonyBridge](https://github.com/AmitRajput-Dev/SonyBridge): v2 protocol behavior and WH-CH720N control layout.
- [sony-ult-ctl](https://github.com/Leonard013/sony-ult-ctl): ULT Wear's distinct reserved-byte layout.

Shared service UUIDs do not establish shared feature layouts. Betty excludes
unverified models instead of sending packets inferred from another family.
Hardware support claims belong in [COMPATIBILITY.md](../../COMPATIBILITY.md).

## Build and validate

```console
go build -o bt-driver-sony .
./bt-driver-sony --manifest
```

With Betty and explicitly selected hardware:

```console
PATH="$PWD:$PATH" BETTY_BINARY=/path/to/bt \
BETTY_HARDWARE_SONY='device id' \
BETTY_VALIDATE_SONY_CONTROLS=1 \
go test -tags=hardware -run TestSonyControlReadWriteReadback -v ../../hardware
```
