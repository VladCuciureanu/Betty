# Betty Roadmap

Betty is a small, cross-platform Bluetooth device utility. It discovers and manages Bluetooth devices, exposes generic BLE/GATT operations, and provides a minimal property interface for optional device-protocol drivers.

Betty is a platform in one narrow sense: third parties extend it with device drivers through a published, language-neutral protocol. It is not an automation platform, UI framework, package marketplace, or background service. Client applications should own presentation, shortcuts, refresh policy, workflows, and installation guidance.

## Version 1 release definition

Version 1 is ready when the generic CLI works predictably on native macOS, Linux, and Windows hardware, the property API is stable enough for scripts, and supported installation paths have been tested through clean removal.

Platform limitations are acceptable when Betty returns an explicit structured `unsupported` error. Betty must never claim success merely because an asynchronous request was submitted or a temporary BLE object was opened.

### Required command surface

```text
bt scan
bt list
bt inspect <device>
bt pair <device>
bt unpair <device>
bt connect <device>
bt disconnect <device>

bt get <device> <property>
bt set <device> <property> <value>
bt watch <device> <property>

bt gatt <device> services
bt gatt <device> characteristics [service-uuid]
bt gatt <device> read <characteristic-uuid>
bt gatt <device> write <characteristic-uuid> <hex>
bt gatt <device> watch <characteristic-uuid>

bt driver install|list|remove ...
bt driver export-state|import-state ...
```

The earlier `bt <device>` and `bt <device> battery` aliases were removed: they turned every mistyped command into a device lookup and a scan.

## Version 1 blockers

### 1. Minimal property API

- [x] Implement `inspect`, `get`, `set`, and property-level `watch` without adding vendor-specific CLI commands.
- [x] Extend the external-driver protocol beyond `inspect` with generic get and set operations. Property watch initially polls generic get, leaving native driver subscriptions optional.
- [x] Keep capability descriptions small: type, readable, writable, accepted values or numeric range, and availability.
- [x] Normalize shared properties: `battery`, `noise_control`, `transparency_level`, and `firmware_version` are defined in the protocol and the SDK; `wear_detection`, `microphone_mute`, and `codec` are reserved.
- Namespace only genuinely vendor-specific properties, for example `sony.speak_to_chat`.
- [x] Verify writes by reading state back when the protocol supports it; distinguish requested from verified state.
- [x] Initially support one property per watch process. Multi-property subscriptions can wait for a demonstrated need.

### 2. Stable automation behavior

- [x] Add `--non-interactive` for applications and scripts. Only the Linux pairing agent honors it so far; Windows and macOS can still show OS pairing dialogs.
- [x] Guarantee that `--json` success output is JSON-only on stdout; prompts, diagnostics, and JSON failures use stderr.
- [x] Define schema-version-1 envelopes for inspect, get, set, watch events, and errors.
- [x] Add structured errors for interaction required, disconnected devices, missing drivers, unsupported features, permissions, unavailable adapters, authentication, and timeout.
- At 1.0, freeze schema version 1: from then on, unknown JSON fields are safe for clients to ignore and existing fields do not change meaning within a schema version. Until then the JSON API and driver protocol may change incompatibly.
- [x] Test cancellation and timeout behavior at every process and Bluetooth resource boundary: backend contract tests, driver process tests, and socket close tests cover it.

### 3. Cross-platform generic hardware behavior

Validate on native macOS, Linux, and Windows:

- discovery and saved-device enumeration;
- deterministic device resolution and ambiguity handling;
- pairing, including operating-system confirmation and cancellation;
- paired-state verification after pairing and unpairing;
- connection-state verification after connect and disconnect where supported;
- GATT service and characteristic discovery;
- GATT reads and both supported write modes;
- notification subscription and Ctrl-C cleanup;
- standard Battery Service readings;
- adapter disabled, permission denied, timeout, reset, and unexpected disconnect behavior.

- [x] Document public-API limitations instead of working around them with private APIs. The README's platform section lists them.

### 4. Backend contracts and device identity

- [x] Add shared backend contract tests for adapter unavailability, permission denial, authentication cancellation, connection timeout, duplicate endpoints, and unexpected disconnection.
- Put native APIs behind narrow testable interfaces where practical.
- [x] Treat OS, Classic, and BLE endpoints as separate observations.
- [x] Never merge endpoints solely because their display names match.
- [x] Preserve uncertain relationships in `possible_matches`, and mark values copied across them as `correlated`.
- Use protocol keys or authenticated sessions as stronger identity evidence where available.
- Add fixtures for duplicate names, rotating BLE addresses, stale devices, and multiple identical nearby products.
- [x] Keep stable device IDs opaque and platform-local.

### 5. Linux Apple Audio transport privilege

- [x] Remove the incorrectly assigned `CAP_NET_RAW` capability and `libcap2-bin` dependency. Betty opens a connection-oriented L2CAP `SOCK_SEQPACKET`; the Linux kernel capability check applies only to L2CAP `SOCK_RAW`.
- [x] Keep both `bt` and optional drivers unprivileged. A helper is neither needed nor shipped.
- [ ] Validate channel cleanup, process termination, Bluetooth reset, denial by distribution security policy, and unexpected device disconnection on native Linux hardware.

### 6. Native distribution

- [x] License the project under the DFSG-compatible SPDX `MIT` license.
- [x] Generate SHA-256 checksums for every immutable tagged release artifact. GitHub supplies source archives for tags.
- Build releases natively on macOS, Linux, and Windows; cross-compilation remains supplementary validation.
- [x] Verify ordinary commands perform no network requests and leave no background process: the Linux end-to-end suite traces them.

#### macOS and Homebrew

- Create `VladCuciureanu/homebrew-tap`.
- [x] Add release templates for `betty-bluetooth`, `betty-apple-audio`, and `betty-sony`; render them into `VladCuciureanu/homebrew-tap` after a tag exists.
- [x] Use `betty-bluetooth` as the base formula name because Homebrew already has an unrelated `betty` formula.
- [x] Install the `bt` executable while building only `cmd/bt` in the base formula.
- [x] Keep Apple Audio and Sony in separate optional formulas discovered through exact, manifest-verified executable names on `PATH`.
- Test Apple Silicon and Intel builds, Bluetooth permissions, signing, and notarization as applicable.
- Run Homebrew audit, source installation, and formula tests.

#### Linux

- [x] Build and install Debian packages on Ubuntu and Debian, in clean containers.
- [x] Verify that `betty` contains no vendor protocol implementation.
- [x] Test `betty-apple-audio` version matching, upgrades, and clean uninstall.
- [x] Test `betty-sony` installation and clean uninstall.
- [x] Preserve per-user driver state during package removal.

#### Windows

- Build and test `betty-windows-x64.msi` on clean Windows 10 and Windows 11 systems.
- Build and test the optional `betty-apple-audio-advanced-windows-x64.msi` separately. It is an unsupported developer build and not part of releases (#8).
- Validate install, upgrade, rollback, repair, and uninstall ordering.
- [x] Ensure the base MSI contains no Apple Audio implementation or kernel payload.
- Complete security review and native hardware testing of the Apple Audio IOCTL bridge.
- Submit the profile driver through Microsoft Hardware Dev Center and validate the returned catalog under kernel signing policy.
- Sign user-space executables and MSI packages independently from Microsoft kernel signing. Releases ship unsigned until then (#74).
- Keep source releases and the base MSI publishable when optional Apple Audio signing is unavailable.

### 7. Release evidence

- [x] Add a compatibility-evidence table containing OS version, adapter, device model, tested operations, and limitations. Populate it only from native runs.
- Test at least one disposable Classic device and one generic BLE peripheral on every target OS.
- Keep vendor hardware validation separate from generic Bluetooth validation.
- [x] Require configured Classic and BLE devices on native self-hosted hardware runners instead of allowing an all-skipped green workflow.
- [x] Generate release checksums, an SPDX JSON SBOM, and signed SLSA build-provenance attestations.
- Test upgrades and uninstalls from the previous stable version once one exists.

## Driver platform

Third-party drivers are a first-class extension point. The protocol in [docs/DRIVER_PROTOCOL.md](docs/DRIVER_PROTOCOL.md) is the contract; the Go SDK is a convenience.

- [x] Publish a normative, language-neutral protocol specification with a worked transcript and a non-Go example driver.
- [x] Provide a Go SDK: `driver.Serve`, typed `Inspect`/`Get`/`Set` handlers, typed host calls, structured errors, and exported constants for every protocol string.
- [x] Port the Sony driver to the SDK as the reference implementation.
- [x] Port the Apple Audio driver to the SDK.
- [x] Add GATT host calls (discover, read, write, subscribe) so BLE-only devices, the most common third-party case, can have drivers. This is an additive protocol 1 change.
- [x] Add manifest match hints (company IDs, service UUIDs, names) so Betty starts only relevant drivers instead of every driver for every command.
- [x] Ship a conformance checker that runs a driver against scripted requests and a fake host: `bt driver check <executable>`.
- [x] Surface driver stderr and skipped-inspect failures under `--verbose`. Inspect failures are warnings in every mode.
- [x] Publish JSON Schemas for protocol messages, generated from or tested against the Go types.
- [x] Bound message sizes and test malformed, oversized, crashing, and hanging drivers.
- [x] Reuse one driver process across requests within a command once the per-request loop is proven by every official driver.

## Optional driver work required for supported claims

Optional drivers do not block a base Betty release unless their packages or features are advertised as supported release artifacts. A capability must remain `planned` or `unsupported` until it is implemented and hardware-validated.

### Apple Audio

#### Battery

- Validate exact AAP battery reports on real AirPods and Beats devices on Linux. Windows doesn't support the AAP channel: only a signed kernel-mode driver could open it.
- Validate authorized System Keychain acquisition and encrypted BLE battery reports on macOS.
- Compare headset, left, right, case, charging, disconnected, sleeping, and reconnect states against a trustworthy reference.
- [x] Add fixtures for fragmented packets, unrelated notifications, missing components, malformed lengths, invalid statuses, private-key responses, and timeouts.
- Validate IRK and advertisement-key persistence, address rotation, reboot, and state transfer.
- Continue marking unencrypted proximity values as estimated with 10% resolution.
- Never silently substitute an estimate when exact data was explicitly requested.

#### Device matching

- Do not treat matching AirPods or Beats names as proof of identity.
- Test multiple nearby Apple Audio devices and multiple saved devices.
- Use acquired keys or an authenticated AAP session as stronger correlation evidence.
- Expose ambiguity instead of attaching battery state to the wrong saved endpoint.

#### Controls

- [x] Implement AAP current-state reads and write/read-back verification, gated behind `BETTY_VALIDATE_APPLE_AUDIO_CONTROLS=1` until Betty-native hardware evidence exists.
- Hardware-validate `noise_control` on each claimed model; supported values are off, ANC, transparency, and adaptive only where the model profile advertises adaptive mode.
- Add Powerbeats Pro 2 standard-GATT heart-rate reading only after validating its fitness-equipment pairing mode.
- Keep conversation awareness, personalized volume, press behavior, and other settings deferred until hardware evidence justifies them.

### Sony headphones

- [x] Route existing exact v2 battery parsing through the generic `get` API.
- Hardware-test SDP discovery and RFCOMM lifecycle on Linux, macOS, and Windows.
- Validate v2 initialization, single and component battery values, charging state, fragmentation, and acknowledgements.
- Determine whether supported v1 models expose battery through the control protocol.
- [x] Implement model-scoped v2 current-state reads and write/read-back verification for WH-CH720N and ULT Wear packet layouts, gated behind `BETTY_VALIDATE_SONY_CONTROLS=1` until Betty-native hardware evidence exists.
- Hardware-validate noise control and transparency level on each claimed model before removing the validation gate.
- Keep EQ, DSEE, voice focus, and speak-to-chat planned until model-specific hardware tests pass.
- Maintain a tested-model table rather than treating a shared service UUID as proof of feature support.

## Hardening after the v1 API is stable

- [x] Test malformed or oversized driver messages, crashes, hangs, and leaked channels.
- [x] Define driver compatibility, upgrade, rollback, and deprecation policy: see the protocol's Stability section.
- Preserve namespaced, user-private driver state and migration behavior.
- Add cryptographic endpoint verification where supported by a protocol.
- Review the pinned `tinygo.org/x/bluetooth` version before upgrades.
- [x] Test characteristics with duplicate UUIDs under different services and define deterministic selection: such a UUID is ambiguous unless the service is named.
- [x] Complete Device Information Service coverage and consistent field naming.
- Expand native event consumption where it materially improves cancellation or state accuracy.

## Explicitly deferred

These are outside Betty v1 unless a concrete Bluetooth-device use case demonstrates otherwise:

- a Betty daemon;
- application workflows, favorites, shortcuts, or refresh policy;
- UI labels, layouts, and control-rendering metadata;
- automatic driver downloads or a package marketplace;
- historical device state;
- multi-property watch multiplexing;
- complex action parameter schemas;
- in-process plugins or a plugin runtime other than executable drivers;
- driver sandboxing;
- RPM, Arch, and portable package formats;
- automatic endpoint correlation without verifiable evidence;
- broad diagnostic or hardware-setup commands such as `bt doctor`;
- proprietary controls without explicitly selected hardware and captured protocol evidence.

Clients should remain responsible for presentation, workflows, polling decisions, shortcuts, and installation guidance. Betty should expose only Bluetooth device state and operations through its human and JSON interfaces.
