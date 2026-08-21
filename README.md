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

BLE scan results retain hexadecimal manufacturer and service payloads. Betty never merges devices merely because their names match.

Vendor-specific support is delivered as optional external drivers. The base `bt` binary contains no AirPods parser or model table. To build and install the first-party AirPods driver locally:

```console
go build -o bt-driver-airpods ./cmd/bt-driver-airpods
bt driver install ./bt-driver-airpods
bt driver list
```

The driver contains the platform-neutral Apple Accessory Protocol implementation. It performs the AirPods handshake, enables notifications, and parses exact component battery reports when Betty can provide an L2CAP connection to PSM `0x1001`. It also recognizes Apple proximity-pairing advertisements as a passive fallback, reports estimated 10% battery buckets, and exposes unverified endpoint relationships through `possible_matches`.

Drivers use a versioned duplex JSON protocol over stdin/stdout; they do not add their own CLI grammar. During a session, a permitted driver can request `l2cap.open/read/write/close` and namespaced `storage.get/set` operations from Betty. Access is limited to device IDs supplied to that driver and channel IDs opened by that session. Persistent values are isolated per driver and written with user-only permissions.

The driver binary and AAP implementation are cross-platform; transport availability is separate. Linux uses a native `AF_BLUETOOTH`/L2CAP socket and may require `CAP_NET_RAW`. macOS rejects the reserved AirPods PSM for third-party processes, but can obtain advertisement keys from its System keychain with user authorization. Windows uses the same AAP implementation through an optional Bluetooth profile driver: Betty discovers its device interface and sends the same L2CAP byte stream through a narrow `DeviceIoControl` transport.

Windows does not offer arbitrary Classic L2CAP channels to ordinary desktop applications. Exact active battery reads therefore require a signed profile driver bound to the AirPods SDP service. Betty currently implements the user-mode ABI used by the in-development [LibrePods Windows profile driver](https://github.com/librepods-org/librepods/pull/716); it does not bundle, install, or silently elevate to install that kernel component. When the interface is absent, the active transport reports `unsupported` internally and the AirPods driver can still return passive estimates or use imported advertisement keys.

When an AAP transport is available, the driver requests and stores the AirPods IRK and advertisement-encryption key. On macOS, an explicit battery read instead asks the Security framework for the paired device's `MobileBluetooth` item in the System keychain; macOS may display an authorization prompt. Ordinary scans never request Keychain access. The driver can then decrypt private BLE advertisements to obtain exact battery levels without an active L2CAP channel. Driver state can be moved to another machine:

```console
bt driver export-state official/airpods airpods-state.json
bt driver import-state official/airpods airpods-state.json
```

Exports contain private device keys and are created with user-only permissions (`0600`). Treat them like credentials, transfer them securely, and delete unnecessary copies. Import replaces the installed driver's existing state.

The AAP packet flow and battery layout follow the reverse-engineered [LibrePods protocol documentation](https://github.com/librepods-org/librepods/blob/main/docs/AAP%20Definitions.md). Private-key provisioning and encrypted advertisement behavior were cross-checked against [MagicPodsCore](https://github.com/steam3d/MagicPodsCore). Betty's implementation is written independently in Go behind its generic driver protocol; neither GPL codebase is linked or copied into Betty.

The current executable-driver prototype should be treated as trusted native code. Sandboxing and cryptographic endpoint verification remain future driver-host work. The AirPods driver also advertises a planned `noise_control` capability (`off`, `anc`, `transparency`, and `adaptive`). It is deliberately marked unavailable until AAP reads/writes and hardware behavior are implemented and tested.

## Platform behavior

| Operation | Linux/BlueZ | macOS | Windows 10/11 |
| --- | --- | --- | --- |
| BLE scan and GATT | Supported | Supported | Supported |
| Saved-device listing | Supported | Supported | Supported |
| Pair | Supported | Supported | Supported; Windows may show UI |
| Unpair | Supported | Not exposed by a supported public API | Supported |
| Generic OS profile connect/disconnect | Supported | Supported | Not reliably exposed for every profile |
| AirPods exact battery | AAP or encrypted BLE | Encrypted BLE using authorized System-keychain keys | AAP with optional profile driver, or encrypted BLE after key import |
| AirPods ANC control | Planned over existing AAP transport | Blocked by macOS for PSM `0x1001` | Transport ready with optional profile driver; commands planned |

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

Hardware tests are intentionally separate from the default suite. Real releases require validation with a Classic device and a generic BLE peripheral on all three operating systems. Native L2CAP can be probed with `BETTY_HARDWARE_CLASSIC_ID=macos:<address> go test -tags=hardware -run TestNativeChannelLifecycle -v ./internal/l2cap`. AirPods are expected to return the explicit macOS unsupported error for PSM `0x1001`.

Set `BETTY_BINARY` to the built executable and opt in with disposable hardware:

```console
BETTY_HARDWARE_BLE=sensor BETTY_HARDWARE_CLASSIC=keyboard go test -tags=hardware ./hardware
```

The Classic lifecycle test pairs, connects, disconnects, and unpairs the selected device. Do not point it at hardware whose pairing state must be preserved.
