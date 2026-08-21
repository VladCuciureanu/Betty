# Betty (`bt`) Brainstorm

> Cross-platform Bluetooth CLI focused on making Bluetooth devices as easy as possible to inspect and manipulate from the terminal.

## 0. Product Goal

Betty is a small, cross-platform Bluetooth CLI distributed as a single primary binary named `bt`.

The guiding UX principle is:

> If a user knows the name of their device and the thing they want to change, they should be able to guess the command.

Examples:

```bash
bt scan
bt airpods
bt airpods battery
bt airpods anc on
bt xm6 ambient 5
bt keyboard disconnect
```

Betty should hide Bluetooth implementation details unless the user explicitly asks for them.

Raw BLE/GATT access must still exist as an advanced escape hatch:

```bash
bt gatt airpods services
bt gatt airpods read <uuid>
bt gatt airpods write <uuid> <value>
bt gatt airpods watch <uuid>
```

The project should optimize for:

1. Ease of use
2. Small binary size
3. Cross-platform behavior
4. Scriptability
5. Privacy
6. Extensible per-device support
7. Minimal dependencies
8. No daemon requirement

---

# 1. Core Technical Decisions

## Language

Use Go.

Do not switch languages without an explicit project-level decision.

## Binary

Primary executable:

```text
bt
```

Product/project name:

```text
Betty
```

## BLE/GATT

Prefer:

```text
tinygo.org/x/bluetooth
```

for portable BLE/GATT functionality where it provides the required behavior.

Do not force classic Bluetooth through the same abstraction if doing so produces weak or inconsistent platform behavior.

## Classic Bluetooth

Use thin platform-native backends.

Target:

```text
Linux   -> BlueZ / D-Bus
macOS   -> native Apple Bluetooth APIs
Windows -> WinRT / Win32 Bluetooth APIs
```

Do not reimplement Bluetooth protocols that the OS already provides.

## Runtime architecture

Betty must not require a daemon.

Base operations should run directly from the CLI process.

---

# 2. Product Principles

## 2.1 Prefer obvious commands over theoretically pure commands

Good:

```bash
bt xm6 anc on
bt airpods battery
bt keyboard disconnect
```

Avoid exposing internal concepts such as:

```bash
bt feature sony:noise-control --device ...
bt provider select ...
bt profile attach ...
```

unless such commands are strictly diagnostic/developer-facing.

## 2.2 Device support should enrich the same CLI

Installing support for a device must not create a separate command namespace.

After a plugin is installed:

```bash
bt airpods anc transparency
```

should work.

Do not require:

```bash
bt apple airpods anc transparency
```

or:

```bash
bt plugin apple execute ...
```

## 2.3 Standard Bluetooth support belongs in core

Core Betty should support as much standards-based functionality as practical without optional plugins.

Examples:

- scan
- list
- inspect
- pair
- unpair
- connect
- disconnect
- BLE discovery
- standard GATT services
- generic GATT reads/writes/subscriptions
- standard Battery Service when available
- standard metadata where available

Plugins are for proprietary or device-specific knowledge.

## 2.4 No network access by default

Running normal commands must never contact the Internet.

Examples that must remain offline:

```bash
bt
bt scan
bt list
bt airpods
bt airpods battery
bt connect airpods
```

Network access is permitted only for explicit network-related plugin operations such as:

```bash
bt plugin search airpods
bt plugin install airpods
bt plugin update
bt plugin outdated
```

Do not add telemetry.

Do not add silent analytics.

Do not add automatic update checks by default.

If update checks are ever added, they must be explicit opt-in.

## 2.5 Scriptability is first-class

Commands that return a single scalar value should make that scalar easy to consume.

Example:

```bash
bt xm6 battery
```

should prefer:

```text
73%
```

over:

```text
Battery level: 73%
```

Support machine-readable output where useful:

```bash
bt xm6 battery --json
```

Example:

```json
{
  "device": "WH-1000XM6",
  "property": "battery",
  "value": 73,
  "unit": "%"
}
```

For successful setters, prefer no output unless there is a useful reason to print confirmation.

Use meaningful exit codes.

---

# 3. CLI Design

The public CLI should center around:

```text
bt
├── scan
├── list
├── pair
├── unpair
├── connect
├── disconnect
├── plugin
├── gatt
└── <device>
    ├── <property>
    ├── <property> <value>
    └── <action>
```

## 3.1 Core examples

```bash
bt scan
bt list

bt connect xm6
bt disconnect xm6

bt xm6 connect
bt xm6 disconnect

bt xm6
bt xm6 battery
bt xm6 anc
bt xm6 anc on
bt xm6 anc ambient

bt airpods battery
bt airpods anc transparency
```

Supporting both:

```bash
bt connect xm6
```

and:

```bash
bt xm6 connect
```

is acceptable if implementation complexity remains low.

Do not duplicate behavior internally; both forms should dispatch to the same operation.

## 3.2 Device inspection

Running:

```bash
bt <device>
```

should show useful current state and discoverable controls.

Example:

```text
WH-1000XM6
connected

battery         73%
anc             on
ambient         0
multipoint      on
speak-to-chat   off
```

The exact output format can evolve, but the device should tell users what Betty knows how to read or manipulate.

## 3.3 Property discovery

A property may expose:

- readable/writable status
- data type
- accepted values
- units
- current value

Possible UX:

```bash
bt xm6 anc ?
```

Output:

```text
off
on
ambient
adaptive
```

Alternative help syntax may be chosen if it is more consistent, but keep property discovery simple.

## 3.4 Raw GATT

Support an advanced namespace:

```bash
bt gatt <device> services
bt gatt <device> characteristics
bt gatt <device> read <uuid>
bt gatt <device> write <uuid> <value>
bt gatt <device> watch <uuid>
```

This is an escape hatch and must not dominate the everyday UX.

---

# 4. Device Resolution

Betty must be forgiving about device identifiers.

Accept, where possible:

- saved alias
- exact device name
- case-insensitive name
- unambiguous partial name
- model name
- Bluetooth address
- stable OS identifier
- scan result index when operating in a clearly scoped context

Examples:

```bash
bt xm6 battery
bt airpods anc on
bt keyboard disconnect
```

## 4.1 Resolution order

Implement deterministic resolution.

Suggested precedence:

1. exact saved alias
2. exact stable ID/address
3. exact device name
4. exact model
5. unambiguous case-insensitive/partial match
6. ambiguity error

Never silently pick one of several equally plausible devices.

## 4.2 Ambiguity

Example:

```text
Multiple devices match "airpods":

1  AirPods Pro
2  Alice's AirPods

Use a more specific name or device ID.
```

Keep the error actionable.

## 4.3 Aliases

Plan for:

```bash
bt alias <device> <alias>
```

Example:

```bash
bt alias "WH-1000XM6" headphones
bt headphones anc on
```

Alias support can be deferred until after the first functional milestone if needed.

---

# 5. Internal Domain Model

The CLI must not expose internal architecture, but the implementation needs clear boundaries.

Use a physical-device model that can combine multiple underlying Bluetooth endpoints.

Conceptually:

```text
Physical Device
├── classic endpoint(s)
├── BLE endpoint(s)
├── OS metadata
├── generic capabilities/properties
└── optional plugin-provided properties/actions
```

Do not assume:

```text
1 Bluetooth address == 1 physical device
```

because some devices expose multiple transports/endpoints.

## 5.1 Device

Suggested initial shape:

```go
type Device struct {
    ID          string
    Name        string
    Alias       string
    Address     string
    Connected   bool
    Paired      bool
    Endpoints   []Endpoint
    Properties  []Property
    Actions     []Action
}
```

Keep this model small and evolve only as real devices require it.

## 5.2 Endpoint

Possible endpoint types:

```go
type EndpointKind string

const (
    EndpointBLE     EndpointKind = "ble"
    EndpointClassic EndpointKind = "classic"
)
```

Each physical device can contain zero or more endpoints.

## 5.3 Property

Treat end-user functionality primarily as dynamic properties.

Suggested shape:

```go
type PropertyType string

const (
    PropertyBool   PropertyType = "bool"
    PropertyInt    PropertyType = "int"
    PropertyFloat  PropertyType = "float"
    PropertyString PropertyType = "string"
    PropertyEnum   PropertyType = "enum"
)

type Property struct {
    Name      string
    Type      PropertyType
    Readable  bool
    Writable  bool
    Values    []string
    Unit      string
}
```

Examples:

```text
battery
anc
ambient
volume
eq
multipoint
wear-detection
speak-to-chat
auto-pause
codec
```

Do not hardcode vendor names into common property names.

## 5.4 Actions

Some operations are actions rather than stateful properties.

Examples:

```text
connect
disconnect
pair
unpair
locate
factory-reset
```

Use an explicit action abstraction where a property model is unnatural.

---

# 6. Backend Architecture

Define a small internal backend interface and platform-specific implementations.

Suggested package layout:

```text
internal/
  backend/
    backend.go
    linux/
    darwin/
    windows/
```

Possible core interface:

```go
type Backend interface {
    Scan(ctx context.Context, opts ScanOptions) ([]DiscoveredDevice, error)
    Devices(ctx context.Context) ([]DiscoveredDevice, error)

    Pair(ctx context.Context, id string) error
    Unpair(ctx context.Context, id string) error

    Connect(ctx context.Context, id string) error
    Disconnect(ctx context.Context, id string) error
}
```

Do not force platform implementations to pretend every operation is equally supported.

Model support explicitly.

Example:

```go
type Support int

const (
    Unsupported Support = iota
    BestEffort
    Supported
)
```

This is especially relevant for classic Bluetooth connect behavior on Windows.

---

# 7. BLE Layer

Wrap `tinygo.org/x/bluetooth` behind Betty-owned interfaces.

Do not leak dependency-specific types across the codebase.

Suggested package:

```text
internal/ble
```

Initial requirements:

- scan advertisements
- connect to BLE peripheral
- enumerate services
- enumerate characteristics
- read characteristic
- write characteristic
- subscribe/notify
- standard Battery Service helper

Use context cancellation throughout.

Avoid introducing a large async framework.

---

# 8. Classic Bluetooth Layer

Use platform-specific native facilities.

## Linux

Prefer BlueZ through D-Bus.

Implement:

- discovery
- paired devices
- connect
- disconnect
- pair
- unpair
- connected state

Keep the D-Bus dependency minimal.

## macOS

Use native macOS Bluetooth APIs through a thin bridge where necessary.

Avoid creating a large Objective-C subsystem.

Expose only the operations Betty needs.

## Windows

Use WinRT / Win32 Bluetooth APIs.

Treat classic profile connection behavior carefully.

If Windows cannot support a generic operation reliably, report that limitation rather than faking parity.

---

# 9. Plugin System

Plugins are opt-in device-support packages.

Their purpose is to teach Betty proprietary protocols without bloating the core binary.

Examples:

```bash
bt plugin install airpods
bt plugin install sony
bt plugin install bose
```

After installation, supported properties appear naturally on the device:

```bash
bt airpods anc transparency
bt xm6 speak-to-chat on
```

## 9.1 Plugin granularity

Package boundaries should follow shared implementation/protocol families, not exact marketing models.

Avoid one package per device model unless the protocol is truly distinct.

Good conceptual package:

```text
apple-airpods
```

which may support multiple AirPods generations/models.

If two product families require unrelated protocol implementations, split them.

User-facing aliases may be broader than the internal canonical package name.

For example:

```bash
bt plugin install airpods
```

may resolve to:

```text
official/apple-airpods
```

## 9.2 Core vs plugin responsibilities

Core:

- discovery
- generic device resolution
- generic pairing/connectivity
- GATT primitives
- standard services
- CLI
- plugin lifecycle
- plugin host protocol

Plugins:

- device identification beyond generic discovery
- proprietary GATT protocol knowledge
- vendor packet encoding/decoding
- proprietary properties/actions
- model/version-specific behavior

## 9.3 Plugins must not own the CLI

Plugins expose structured properties/actions.

The core CLI renders and dispatches them.

A plugin should not parse top-level CLI arguments.

---

# 10. Plugin Security Model

Do not use Go's native `plugin` package.

Do not load arbitrary shared libraries into the main process.

Preferred design:

```text
.btplugin package
├── manifest.json
└── driver.wasm
```

Use WebAssembly as the preferred sandbox format.

The exact runtime can be chosen during implementation, but favor a pure-Go WASM runtime such as `wazero` if it satisfies size and API requirements.

## 10.1 Host API

Plugins should not receive unrestricted OS access.

Expose a narrow host API.

Initial conceptual host calls:

```text
device.name
device.metadata
device.advertisement

ble.services
ble.characteristics
ble.read
ble.write
ble.subscribe

property.register
action.register

storage.get
storage.set
```

Do not expose by default:

- raw filesystem access
- network access
- process execution
- arbitrary sockets
- arbitrary environment-variable access

## 10.2 Plugin-local storage

If needed, give each plugin a small namespaced key/value store.

Example namespace:

```text
plugin:official/apple-airpods
```

Never allow a plugin to read another plugin's private storage.

## 10.3 Plugin permissions

If future functionality requires more power, introduce explicit permissions.

Example future manifest:

```json
{
  "permissions": ["ble", "plugin-storage"]
}
```

Do not design permissions too broadly in v1.

---

# 11. Keep Plugin Runtime Out of the Base Binary if Practical

Binary size matters.

Investigate a two-process architecture:

```text
bt
bt-plugin-host
```

Base Betty remains small.

If no plugins are installed, `bt-plugin-host` is unnecessary.

Potential layout:

```text
bt
bt-plugin-host
plugins/
  official/
    apple-airpods/
      manifest.json
      driver.wasm
```

`bt` communicates with `bt-plugin-host` over a small local protocol.

Candidates:

- stdin/stdout framed messages
- JSON initially for simplicity
- compact binary protocol later only if measurements justify it

Do not prematurely optimize the local IPC encoding.

If integrating the WASM runtime directly into `bt` proves both simpler and sufficiently small, document the measured trade-off before choosing it.

---

# 12. Plugin Manifest

Start with a small versioned manifest.

Example:

```json
{
  "schema": 1,
  "name": "apple-airpods",
  "namespace": "official",
  "version": "0.1.0",
  "plugin_api": "1",
  "description": "Extended controls for Apple AirPods devices",
  "matches": [],
  "provides": []
}
```

Fields may evolve.

Requirements:

- manifest schema version
- plugin version
- plugin API compatibility
- canonical package identity
- package provenance/signature metadata where appropriate

Do not encode device logic into the manifest if it belongs in WASM code.

---

# 13. Plugin Registry

Design for an official registry first.

Example UX:

```bash
bt plugin search airpods
bt plugin install airpods
bt plugin list
bt plugin update
bt plugin outdated
bt plugin remove airpods
```

Internally use canonical names such as:

```text
official/apple-airpods
official/sony-headphones
official/bose-headphones
```

Plan for third-party namespaces later.

Examples:

```text
vlad/foo-headset
someone/obscure-device
```

Do not require third-party registry support in the first plugin milestone.

## 13.1 Install behavior

An explicit:

```bash
bt plugin install airpods
```

may contact the registry.

Install flow:

1. resolve package alias
2. fetch package metadata
3. verify compatibility
4. download artifact
5. verify checksum/signature
6. install atomically
7. make plugin available to Betty

Avoid partially installed plugins.

## 13.2 Updates

Support explicit updates:

```bash
bt plugin update
bt plugin update airpods
```

Support inspection:

```bash
bt plugin outdated
```

Do not perform automatic network checks from ordinary Betty commands.

## 13.3 Signatures

Official packages should eventually be signed.

Design manifest/package storage so signatures can be added without changing the installed package identity.

Do not block the first local-plugin prototype on production-grade signing.

---

# 14. Plugin API Versioning

Version the Betty plugin API independently from Betty itself.

Conceptually:

```text
Betty version:      0.8.0
Plugin API version: 1
```

A plugin must declare compatibility.

Do not tie plugin compatibility to an exact Betty release unless absolutely necessary.

The host must reject incompatible plugins with a clear error.

Example:

```text
apple-airpods 2.0.0 requires Betty Plugin API 2
installed Betty supports Plugin API 1
```

---

# 15. Plugin Matching

Plugins need to identify supported devices.

Matching inputs may include:

- advertised name
- manufacturer data
- service UUIDs
- vendor/product identifiers
- GATT services
- classic SDP metadata
- OS metadata
- device model hints

Do not rely on device name alone.

Allow plugins to return confidence.

Conceptual API:

```go
type MatchConfidence int

const (
    NoMatch MatchConfidence = iota
    PossibleMatch
    StrongMatch
    ExactMatch
)
```

If multiple plugins match, Betty should merge non-conflicting properties/actions where possible.

If providers conflict, use a deterministic priority mechanism and make diagnostics available.

The normal user should not need to understand provider resolution.

---

# 16. Plugin Discovery UX

Because plugin support is opt-in and Betty does not phone home, normal scanning must not search the registry.

If an unsupported device is recognized only generically, Betty may say:

```text
AirPods Pro
connected

battery    81%

Extended device controls may be available via plugins.
Run:
  bt plugin search airpods
```

This message must not imply that Betty contacted the Internet.

Do not automatically search.

---

# 17. Configuration

Avoid Viper.

Prefer no config file until one is clearly useful.

When configuration becomes necessary, use a small dependency or a tiny hand-rolled format.

Potential config:

```text
~/.config/bt/config.toml
```

Potential state:

```text
~/.local/share/bt/
```

Potential cache:

```text
~/.cache/bt/
```

Use platform-appropriate locations instead of hardcoding Unix paths on Windows/macOS.

Initial persistent data may include:

- aliases
- plugin installation metadata
- plugin registry metadata cache
- opt-in settings

Do not persist Bluetooth state that the OS already owns unless needed.

---

# 18. CLI Parsing

Avoid Cobra and Viper initially.

Use a small command dispatcher and Go standard library parsing where practical.

Example shape:

```go
switch args[0] {
case "scan":
    ...
case "list":
    ...
case "plugin":
    ...
case "gatt":
    ...
default:
    // attempt device resolution
}
```

If the command tree becomes difficult to maintain, reconsider a small CLI library based on measured binary-size and maintenance impact.

Do not introduce Cobra by default.

---

# 19. Output Model

Centralize output rendering.

Suggested internal modes:

```go
type OutputMode int

const (
    OutputHuman OutputMode = iota
    OutputJSON
    OutputQuiet
)
```

Do not scatter `fmt.Printf` throughout business logic.

Commands should return structured results to a renderer.

This makes:

```bash
bt xm6 battery
bt xm6 battery --json
```

share the same underlying operation.

---

# 20. Error Model

Use typed/sentinel errors for common user-facing cases.

Examples:

```text
ErrDeviceNotFound
ErrDeviceAmbiguous
ErrUnsupported
ErrNotPaired
ErrNotConnected
ErrPermissionDenied
ErrBluetoothUnavailable
ErrPluginNotInstalled
ErrPluginIncompatible
ErrPluginFailed
```

Render concise errors by default.

Provide richer diagnostics through a flag such as:

```bash
bt --verbose ...
```

or:

```bash
BT_DEBUG=1
```

Choose one consistent mechanism.

---

# 21. Repository Layout

Initial recommendation:

```text
/
├── cmd/
│   └── bt/
│       └── main.go
│
├── internal/
│   ├── app/
│   ├── cli/
│   ├── device/
│   ├── resolver/
│   ├── output/
│   ├── backend/
│   │   ├── backend.go
│   │   ├── linux/
│   │   ├── darwin/
│   │   └── windows/
│   ├── ble/
│   ├── gatt/
│   ├── properties/
│   ├── plugins/
│   │   ├── manifest/
│   │   ├── registry/
│   │   ├── runtime/
│   │   └── protocol/
│   └── config/
│
├── plugin-sdk/
│   └── ...
│
├── plugins/
│   └── examples/
│
├── go.mod
├── go.sum
├── README.md
└── PLAN.md
```

Do not create all packages immediately.

Create packages only when a milestone needs them.

Avoid architecture for architecture's sake.

---

# 22. Build Configuration

Primary release build should use size-conscious flags:

```bash
go build \
  -trimpath \
  -ldflags="-s -w" \
  -o bt \
  ./cmd/bt
```

Track binary size in CI.

Record size by platform for each release.

Avoid UPX in official release builds unless there is a strong measured reason.

Keep the source compatible with TinyGo where practical, but do not constrain the initial implementation around TinyGo.

TinyGo is an optional later optimization.

---

# 23. Dependency Policy

Before adding a dependency, evaluate:

1. binary-size impact
2. transitive dependencies
3. cross-platform behavior
4. maintenance status
5. license
6. whether the standard library is sufficient
7. TinyGo compatibility when relevant

Avoid convenience dependencies that save only a small amount of code.

Prefer Betty-owned wrappers around third-party APIs.

---

# 24. Testing Strategy

## Unit tests

Test:

- device resolution
- ambiguity handling
- property parsing
- property value coercion
- output formatting
- plugin manifests
- plugin API version checks
- plugin package integrity logic
- plugin matching
- command dispatch
- no-network-by-default invariants

## Backend tests

Use small backend interfaces so platform behavior can be mocked.

Do not require physical Bluetooth hardware for most tests.

## Integration tests

Add opt-in hardware tests for:

- Linux
- macOS
- Windows
- BLE reads/writes
- pairing/connect/disconnect
- one supported proprietary device plugin

Mark hardware tests clearly and exclude them from default CI.

## Plugin tests

Provide a fake BLE host environment so a plugin can be tested against captured advertisements/GATT traffic.

This is important for community plugin development.

---

# 25. Privacy Tests

Add tests that ensure normal commands cannot unexpectedly access the network.

Prefer architectural enforcement over only behavioral tests.

For example:

- core app has no registry client unless a plugin network command is executed
- plugin runtime receives no networking host calls
- regular device commands do not initialize update services

Privacy is a product guarantee, not just documentation.

---

# 26. Logging and Diagnostics

Default output should be clean.

Diagnostics should be opt-in.

Useful diagnostics:

```bash
bt --verbose scan
bt --verbose airpods anc on
```

Potential information:

- selected backend
- resolved physical device
- selected BLE endpoint
- plugin match
- property provider
- GATT operation
- OS error code

Never print secrets or unrelated environment information.

---

# 27. Milestones

Implement sequentially.

Do not start plugin registry infrastructure before core Bluetooth behavior works.

## Milestone 0 — Skeleton

Deliver:

- Go module
- `cmd/bt`
- simple custom command parser
- version command
- structured output foundation
- backend interface
- CI skeleton
- release size reporting

Commands:

```bash
bt --version
bt --help
```

Acceptance:

- builds on Linux/macOS/Windows
- no Cobra/Viper
- binary size recorded
- clean package structure without unnecessary abstraction

---

## Milestone 1 — Device Discovery

Deliver:

```bash
bt scan
bt list
```

Requirements:

- BLE scanning
- basic classic/OS device enumeration where feasible
- normalize results into a device model
- merge obvious duplicate endpoints where safe
- deterministic IDs
- human + JSON output

Acceptance:

```bash
bt scan
bt scan --json
```

work on all three platforms to the extent allowed by their native APIs.

---

## Milestone 2 — Generic Device Operations

Deliver:

```bash
bt connect <device>
bt disconnect <device>
bt pair <device>
bt unpair <device>

bt <device>
bt <device> connect
bt <device> disconnect
```

Also implement device resolution.

Acceptance:

- exact names work
- unique partial names work
- addresses/IDs work
- ambiguity produces actionable errors
- OS limitations are surfaced honestly

---

## Milestone 3 — Generic BLE/GATT

Deliver:

```bash
bt gatt <device> services
bt gatt <device> characteristics
bt gatt <device> read <uuid>
bt gatt <device> write <uuid> <value>
bt gatt <device> watch <uuid>
```

Acceptance:

- works without plugins
- no plugin architecture required yet
- no vendor-specific assumptions

---

## Milestone 4 — Standard Properties

Deliver property abstraction.

Initial properties:

```text
battery
```

Potential additional standard properties only if standards/platform support is solid.

Commands:

```bash
bt <device> battery
bt <device>
```

Acceptance:

- property renderer exists
- device inspection lists readable properties
- standard GATT Battery Service works where available
- JSON output is structured

---

## Milestone 5 — Local Plugin Prototype

Do not build a network registry yet.

Deliver:

```bash
bt plugin install ./path/to/plugin.btplugin
bt plugin list
bt plugin remove <plugin>
```

Implement:

- manifest schema v1
- plugin API v1
- WASM runtime prototype
- sandboxed BLE host calls
- dynamic property registration
- local package install
- plugin compatibility checks

Acceptance:

- a local example plugin can recognize a fake/test device
- plugin can expose a property
- plugin can issue allowed BLE reads/writes through host API
- plugin cannot access network/files/processes
- base CLI still works with no plugins

---

## Milestone 6 — First Real Device Plugin

Choose one real device family available for testing.

Good candidate:

```text
Sony WH-1000X family
```

or another device that is physically available during development.

Implement at least:

```text
battery or vendor battery if useful
anc
ambient/transparency equivalent
one additional proprietary setting
```

Acceptance:

```bash
bt xm6 anc on
bt xm6 anc ambient
bt xm6 <vendor-feature> ...
```

works without exposing plugin internals.

This milestone validates the plugin/property model.

Do not finalize the plugin API before this milestone exposes real-world friction.

---

## Milestone 7 — Plugin Registry

After the local plugin system proves itself, add explicit network commands.

Deliver:

```bash
bt plugin search <query>
bt plugin install <name>
bt plugin update [name]
bt plugin outdated
```

Requirements:

- no background calls
- checksums
- atomic install/update
- alias resolution
- plugin API compatibility check
- official namespace
- registry metadata format

Acceptance:

- normal `bt` commands remain network-free
- plugin commands clearly initiate network access
- installed plugin can update independently from Betty

---

## Milestone 8 — Plugin Host Size Optimization

Measure:

1. WASM runtime embedded in `bt`
2. separate `bt-plugin-host`
3. plugin host installed only on first plugin install

Choose based on:

- base `bt` binary size
- total installed size
- startup latency
- implementation complexity
- security isolation
- cross-platform reliability

Document measurements in the repository.

Do not choose based only on intuition.

---

## Milestone 9 — Aliases and UX Polish

Deliver:

```bash
bt alias <device> <alias>
bt unalias <alias>
```

Improve:

- ambiguity messages
- property help
- device summaries
- shell completion if it can be implemented without significant size cost
- command suggestions for typos
- consistent exit codes

Acceptance:

A new user should be able to:

```bash
bt scan
bt xm6
bt xm6 battery
bt xm6 anc on
```

without reading documentation.

---

## Milestone 10 — Broader Device Ecosystem

Add official/community plugins for likely families such as:

```text
Apple AirPods
Sony headphones
Bose headphones
Samsung Galaxy Buds
selected Logitech devices
selected gaming headsets
```

Only implement device families for which protocol behavior can be validated.

Do not guess proprietary protocol packets.

---

# 28. Plugin SDK

Once the first real plugin stabilizes, provide a small SDK.

Goals:

- easy plugin scaffolding
- typed host functions
- test harness
- fixture-based advertisement/GATT tests
- manifest generation/validation
- package builder

Possible workflow:

```bash
bt plugin init my-headphones
bt plugin dev ./my-headphones
bt plugin pack ./my-headphones
```

Do not prioritize this before the plugin runtime works.

---

# 29. Developer Fixtures

Create recorded device fixtures for testing plugin matching and parsing.

Example:

```text
testdata/
  devices/
    sony-wh1000xm6/
      advertisement.json
      services.json
      characteristics.json
      responses/
```

Do not store sensitive personal device identifiers in public fixtures.

Normalize addresses/serials.

---

# 30. Security Considerations

Treat plugin packages as untrusted.

Requirements:

- sandbox execution
- narrow host API
- bounded memory where practical
- execution timeout/cancellation
- validate all plugin output
- validate UUIDs and packet lengths
- no implicit filesystem/network/process access
- package checksum verification
- future signature support

A malformed plugin must not be able to crash the core CLI process where isolation can reasonably prevent it.

---

# 31. Cross-Platform Semantics

Do not promise fake parity.

For each operation, distinguish:

```text
supported
best-effort
unsupported
```

Examples may differ by platform:

- classic audio connect
- pairing UI behavior
- background scan behavior
- permission requirements
- adapter selection
- device identifiers

Expose differences only when necessary.

Keep common CLI syntax stable.

---

# 32. Permissions

Handle Bluetooth permissions gracefully.

On macOS/Windows/Linux, detect common permission failures and provide actionable errors.

Do not require elevated privileges unless the platform truly requires them.

Never silently invoke `sudo`.

---

# 33. Adapter Support

Start with the default Bluetooth adapter.

Design backend APIs so explicit adapter selection can be added later.

Possible future syntax:

```bash
bt --adapter hci1 scan
```

Do not complicate v0 with multi-adapter UX unless needed during testing.

---

# 34. Versioning

Use semantic versioning for Betty releases.

Version separately:

- Betty CLI
- plugin API
- plugin manifest schema
- individual plugins
- registry metadata schema if necessary

Avoid coupling these unnecessarily.

---

# 35. README Goals

README should communicate the product in under one screen.

Suggested opening:

```text
Betty (`bt`) is a small cross-platform CLI for controlling Bluetooth devices.

bt scan
bt airpods battery
bt airpods anc transparency
bt xm6 disconnect
```

Then explain:

- standard Bluetooth works out of the box
- proprietary controls are added through opt-in plugins
- Betty does not access the network unless explicitly asked to perform a network operation

Do not lead with architecture.

---

# 36. Non-Goals for v1

Do not implement these unless a milestone proves they are necessary:

- GUI/TUI
- background daemon
- telemetry
- cloud account
- automatic plugin discovery over the Internet
- automatic plugin updates
- giant device database compiled into core
- scripting language
- arbitrary native plugins
- Cobra/Viper
- embedded web server
- Bluetooth stack reimplementation
- every OS-specific Bluetooth feature
- every vendor protocol

---

# 37. Open Questions

Codex should not block initial implementation on these.

Record decisions when real implementation data is available.

## Plugin host

Determine through measurement whether WASM runtime should be:

1. embedded in `bt`
2. in `bt-plugin-host`
3. dynamically installed with first plugin

Current preference:

```text
separate optional bt-plugin-host
```

because base binary size matters.

## Plugin package archive

Choose a deterministic package format.

Possibilities:

- zip-based `.btplugin`
- tar-based `.btplugin`

Favor cross-platform standard-library support and simple reproducibility.

## Registry transport

Start with a simple HTTPS-hosted metadata/index format.

Do not build a custom service unless static hosting proves insufficient.

## Registry source

Potentially host official plugin metadata/artifacts on GitHub or another static host initially.

This should be revisited when registry work begins.

## Shell completion

Only add if the implementation cost and binary-size impact remain small.

## Config format

Do not choose until configuration is actually required.

---

# 38. First Implementation Tasks for Codex

Begin here.

## Task 1

Create the Go module and minimal executable:

```text
cmd/bt/main.go
```

Support:

```bash
bt --help
bt --version
```

Use no CLI framework.

## Task 2

Create a small command-dispatch package that supports:

```text
scan
list
connect
disconnect
pair
unpair
gatt
plugin
<device>
```

Commands may initially return "not implemented".

## Task 3

Create the core structured result/output system.

Support:

```text
human
json
quiet
```

Do not bake formatting into backend code.

## Task 4

Define the minimal device and backend interfaces.

Avoid speculative fields.

Add only what is required for discovery.

## Task 5

Implement BLE scanning using `tinygo.org/x/bluetooth`.

Keep it behind a Betty-owned interface.

## Task 6

Make:

```bash
bt scan
bt scan --json
```

functional.

Only after this works should implementation proceed to Milestone 2.

---

# 39. Definition of Done for Initial MVP

The first useful Betty release is done when a user can install one small executable and successfully run:

```bash
bt scan
bt list

bt connect <device>
bt disconnect <device>

bt <device>
bt <device> battery

bt gatt <device> services
bt gatt <device> read <uuid>
bt gatt <device> write <uuid> <value>
```

on the supported platforms, with honest reporting of platform-specific limitations.

No plugin registry is required for the initial MVP.

---

# 40. Definition of Done for Plugin v1

Plugin v1 is done when:

1. plugins are explicitly installed
2. normal Betty commands remain offline
3. plugin code is sandboxed
4. plugin API is versioned
5. plugins can identify supported devices
6. plugins can expose dynamic properties/actions
7. plugins can safely perform approved BLE operations
8. a real proprietary device feature works
9. plugins can be installed/removed locally
10. registry-based install/update works only through explicit commands
11. plugins update independently of Betty
12. installing a plugin does not change normal CLI grammar

Example target:

```bash
bt plugin install airpods
bt airpods anc transparency
```

The user should not need to know that a plugin executed.

---

# 41. Guiding Rule for Future Decisions

When architecture and UX conflict, preserve the simple CLI unless doing so would be misleading or unsafe.

Internal complexity is acceptable.

External complexity is not.

The target experience is:

```bash
bt <device> <thing> [value]
```

Everything else exists to make that work reliably.
