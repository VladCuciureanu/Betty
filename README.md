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

Vendor-specific support is delivered as optional external drivers. The base `bt` binary contains no Apple or Beats protocol parser or model table. AirPods and Beats share Apple's Continuity advertisements and Apple Accessory Protocol, so one optional Apple Audio driver supports both families:

```console
go build -o bt-driver-apple-audio ./cmd/bt-driver-apple-audio
bt driver install ./bt-driver-apple-audio
bt driver list
```

Sony headphones use a separate optional driver with shared internal engines for Sony's v1 and v2 RFCOMM protocols:

```console
go build -o bt-driver-sony ./cmd/bt-driver-sony
bt driver install ./bt-driver-sony
```

The Sony driver detects the protocol through its SDP service UUID, validates Sony frame lengths and checksums, and can read exact v2 headset or left/right/case battery reports through native Linux, macOS, or Windows RFCOMM. Windows uses the public Winsock Bluetooth API and resolves the Sony service channel through SDP, so it needs neither a kernel driver nor a native helper DLL. Noise control, ambient level, EQ, DSEE, and speak-to-chat are represented as planned capabilities until Betty's generic capability command grammar and hardware write validation are complete.

The Apple Audio driver contains the platform-neutral Apple Accessory Protocol implementation. It performs the AAP handshake, enables notifications, and parses exact component battery reports when Betty can provide an L2CAP connection to PSM `0x1001`. It recognizes only known AirPods and Beats model IDs in Apple Continuity advertisements, reports estimated 10% battery buckets as a passive fallback, and exposes unverified endpoint relationships through `possible_matches`. Unknown Apple Continuity models are left unclaimed instead of being mislabeled as AirPods.

Drivers use a versioned duplex JSON protocol over stdin/stdout; they do not add their own CLI grammar. During a session, a permitted driver can request `l2cap.open/read/write/close` and namespaced `storage.get/set` operations from Betty. Access is limited to device IDs supplied to that driver and channel IDs opened by that session. Persistent values are isolated per driver and written with user-only permissions.

The driver binary and AAP implementation are cross-platform; transport availability is separate. Linux uses a native `AF_BLUETOOTH`/L2CAP socket and may require `CAP_NET_RAW`. macOS rejects the reserved Apple Audio PSM for third-party processes, but can obtain advertisement keys from its System keychain with user authorization. Windows uses the same AAP implementation through an optional Bluetooth profile driver: Betty discovers its device interface and sends the same L2CAP byte stream through a narrow `DeviceIoControl` transport.

Windows does not offer arbitrary Classic L2CAP channels to ordinary desktop applications. Exact active battery reads therefore require a signed profile driver bound to the AAP SDP service. Betty's normal Windows MSI contains no Apple Audio code or kernel driver. Users who want AirPods or Beats support can explicitly install the separate Advanced Support MSI, which contains Betty's independently authored KMDF profile driver and the optional userspace driver. Without the kernel interface, the userspace driver can still return passive estimates or use imported advertisement keys. The design and protocol were cross-checked against the [LibrePods Windows investigation](https://github.com/librepods-org/librepods/pull/716), but its kernel source and interface GUID are not bundled or copied.

When an AAP transport is available, the driver requests and stores the device IRK and advertisement-encryption key. On macOS, an explicit battery read instead asks the Security framework for the paired device's `MobileBluetooth` item in the System keychain; macOS may display an authorization prompt. Ordinary scans never request Keychain access. The driver can then decrypt private BLE advertisements to obtain exact battery levels without an active L2CAP channel. State created under the former `official/airpods` driver ID is read and migrated automatically. Driver state can be moved to another machine:

```console
bt driver export-state official/apple-audio apple-audio-state.json
bt driver import-state official/apple-audio apple-audio-state.json
```

Exports contain private device keys and are created with user-only permissions (`0600`). Treat them like credentials, transfer them securely, and delete unnecessary copies. Import replaces the installed driver's existing state.

The AAP packet flow and battery layout follow the reverse-engineered [LibrePods protocol documentation](https://github.com/librepods-org/librepods/blob/main/docs/AAP%20Definitions.md). Private-key provisioning, Beats model identifiers, and encrypted advertisement behavior were cross-checked against [MagicPodsCore](https://github.com/steam3d/MagicPodsCore). Powerbeats Pro 2 transport and heart-rate behavior were cross-checked against [BeatsBar](https://github.com/mahdi-salmanzade/BeatsBar). Betty's implementation is written independently in Go behind its generic driver protocol; no third-party implementation is linked or copied into Betty.

The current executable-driver prototype should be treated as trusted native code. Sandboxing and cryptographic endpoint verification remain future driver-host work. The Apple Audio driver advertises planned capabilities only for models that support them. Noise control is deliberately unavailable until AAP reads/writes and hardware behavior are implemented and tested. Powerbeats Pro 2 also advertises planned standard-GATT heart-rate support.

## Platform behavior

| Operation | Linux/BlueZ | macOS | Windows 10/11 |
| --- | --- | --- | --- |
| BLE scan and GATT | Supported | Supported | Supported |
| Saved-device listing | Supported | Supported | Supported |
| Pair | Supported | Supported | Supported; Windows may show UI |
| Unpair | Supported | Not exposed by a supported public API | Supported |
| Generic OS profile connect/disconnect | Supported | Supported | Not reliably exposed for every profile |
| Apple/Beats exact battery | AAP or encrypted BLE | Encrypted BLE using authorized System-keychain keys | AAP with optional profile driver, or encrypted BLE after key import |
| Apple/Beats ANC control | Planned over existing AAP transport | Blocked by macOS for PSM `0x1001` | Transport ready with optional profile driver; commands planned |

Unsupported operations fail with a stable `unsupported` error instead of reporting false success. Linux needs a running BlueZ service and permission to use its system D-Bus API. Bluetooth permissions must be granted to the terminal application on macOS.

On Linux, Betty temporarily registers a BlueZ `KeyboardDisplay` pairing agent for `bt pair`. PIN, passkey, and authorization prompts are written to stderr so JSON stdout remains valid. Pairing fails with an authentication error when required input is unavailable or rejected. Pair, unpair, connect, and disconnect return only after BlueZ reports the requested state.

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

### Linux packages

Build the device-generic Debian package and separate optional vendor packages on Linux:

```console
./packaging/linux/build-deb.sh 1.0.0
```

`betty` contains only the CLI. `betty-apple-audio` installs the external AirPods/Beats driver under `/usr/lib/betty/drivers`; it replaces the earlier `betty-airpods` package name. The `bt` executable is never given raw-socket privileges.

### Windows MSI

Windows releases contain two signed, machine-wide packages:

- `betty-windows-x64.msi` installs only `bt.exe` and the system `PATH` entry. It contains no Apple Audio code.
- `betty-apple-audio-advanced-windows-x64.msi` is an explicit optional install. It adds the AirPods/Beats userspace driver and stages and binds the signed `BettyAAP` Bluetooth profile driver.

Both packages require administrator approval because they install per-machine resources. Users who do not own AirPods or Beats need only the normal MSI.

Build it from an x64 Windows 11 machine with Visual Studio, the WDK, Go, and the .NET SDK:

```powershell
./packaging/windows/build.ps1 -Version 1.0.0
```

The build produces both MSIs. Pass `-BaseOnly` to build the normal MSI without compiling or requiring any Apple Audio or WDK payload. Unsigned Advanced Support builds are only suitable for compile-time validation. Current Windows releases require new kernel drivers to be signed by Microsoft through Hardware Dev Center; an ordinary code-signing PFX is not sufficient. `build-driver-submission.ps1` produces the EV-signed submission CAB. After Microsoft returns the signed package, pass its directory with `-MicrosoftSignedDriverDir` and use `-RequireMicrosoftDriverSignature` to verify it under kernel policy before packaging.

The executables and final MSI separately require either `BETTY_SIGNING_PFX` plus `BETTY_SIGNING_PFX_PASSWORD`, their `BETTY_SIGNING_PFX_BASE64` CI equivalent, or `BETTY_SIGNING_CERT_SHA1`. Passing `-RequireSigning` makes missing application-signing credentials fatal. Tag releases require both signing stages and refuse to publish an MSI built around an unsigned or non-Microsoft-signed kernel package.

Remove Advanced Support first. Its uninstaller unbinds the profile driver, deletes its published package from the Driver Store, and removes the Apple Audio userspace driver. The normal Betty uninstaller is blocked while Advanced Support remains installed, preventing shared files from being orphaned; afterward it removes the system `PATH` entry and `bt.exe`. MSI rollback reverses partially completed driver changes. Per-user Apple Audio keys under the user's Betty configuration directory are deliberately preserved as user data and can be removed separately if desired.

Hardware tests are intentionally separate from the default suite. Real releases require validation with a Classic device and a generic BLE peripheral on all three operating systems. Native L2CAP can be probed with `BETTY_HARDWARE_CLASSIC_ID=macos:<address> go test -tags=hardware -run TestNativeChannelLifecycle -v ./internal/l2cap`. Sony RFCOMM lifecycle can be probed on any supported OS with `BETTY_HARDWARE_CLASSIC_ID=<platform>:<address> BETTY_HARDWARE_RFCOMM_UUID=96cc203e506846adb32de316f5e069ba go test -tags=hardware -run TestNativeRFCOMMChannelLifecycle -v ./internal/rfcomm`. AirPods are expected to return the explicit macOS unsupported error for PSM `0x1001`.

Set `BETTY_BINARY` to the built executable and opt in with disposable hardware:

```console
BETTY_HARDWARE_BLE=sensor BETTY_HARDWARE_CLASSIC=keyboard go test -tags=hardware ./hardware
```

The Classic lifecycle test pairs, connects, disconnects, and unpairs the selected device. Do not point it at hardware whose pairing state must be preserved.
