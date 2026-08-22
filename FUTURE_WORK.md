# Betty Future Work

This document collects the known work remaining before Betty can be considered a dependable cross-platform Bluetooth CLI. It includes implementation gaps, validation requirements, packaging work, and platform limitations discussed during development.

## Release definition

Betty v1 is not ready for release until the documented generic command set works on native macOS, Linux, and Windows hardware:

- discovery and saved-device enumeration;
- pairing and unpairing where the operating system exposes a supported API;
- OS-managed connection and disconnection where the operating system exposes a generic operation;
- generic GATT service and characteristic discovery;
- GATT reads, writes, and notification cancellation;
- standard Battery Service readings;
- correct structured errors, cancellation, and timeouts;
- installation and clean removal using the native package for each platform.

Platform limitations must be reported as `unsupported`; Betty must never report success merely because it opened a temporary BLE object or submitted an asynchronous request.

## Cross-platform core

### Backend contract coverage

- Create shared backend contract tests for adapter unavailability, permission denial, authentication cancellation, duplicate endpoints, connection timeout, and unexpected disconnection.
- Make native platform APIs testable behind narrow internal interfaces instead of requiring live Bluetooth hardware for every failure path.
- Verify state transitions after every asynchronous pair, unpair, connect, and disconnect request.
- Confirm stable error categories and exit codes across all backends.
- Test cancellation at every resource boundary, including active discovery, GATT connections, notifications, pairing agents, and L2CAP channels.

### Device identity and endpoint correlation

- Continue treating OS, Classic, and BLE endpoints as separate observations.
- Never merge devices based only on a shared display name.
- Preserve ambiguous correlations in `possible_matches` rather than claiming certainty.
- Investigate stronger platform-neutral correlation signals when available, including advertised identifiers, model data, stored cryptographic identity, and protocol confirmation.
- Add fixtures for duplicate names, rotating BLE addresses, multiple identical AirPods nearby, and stale saved devices.
- Document that stable Betty IDs are opaque and local to one operating system.

### Discovery

- Consume native device-added, device-removed, and property-change events where practical instead of relying only on final snapshots.
- Deduplicate OS and BLE observations without assuming that one address identifies one physical device.
- Preserve RSSI, advertised services, manufacturer data, service data, and last-seen timestamps consistently.
- Verify that scan cancellation always stops underlying platform discovery.

### Pairing interaction

- Define and test consistent interactive and non-interactive behavior.
- Ensure JSON output remains machine-readable while pairing prompts are written separately.
- Cover PIN entry, passkey display, passkey confirmation, authorization, cancellation, and operating-system-owned confirmation UI.
- Fail clearly when input is required but unavailable.

### CLI and capability model

- Add generic capability commands without changing grammar for individual vendors, for example:

  ```text
  bt <device> get <capability>
  bt <device> set <capability> <value>
  ```

- Validate readable, writable, accepted-value, unavailable, and planned capability states.
- Add `bt doctor` to report adapter availability, permissions, platform transports, optional drivers, and actionable setup problems.
- Add a safe hardware-validation command that gathers structured diagnostics and enables destructive pairing tests only with explicit consent.
- Review GATT write-mode selection against characteristic properties and test both response modes.
- Add more UUID, hexadecimal input, output-schema, and signal-cancellation tests.

## Generic BLE and GATT

- Validate scanning, service discovery, characteristic discovery, reads, writes, and notifications using a generic BLE peripheral on all three operating systems.
- Confirm that Ctrl-C unsubscribes, disconnects, and releases every native resource.
- Test characteristics with duplicate UUIDs under different services and define deterministic selection behavior.
- Validate standard Battery Service values and malformed responses.
- Complete Device Information Service coverage and consistent field naming.
- Track the pinned `tinygo.org/x/bluetooth` release and review upstream API or behavioral changes before upgrades.

## Optional driver system

- Treat executable drivers as trusted native code until a real isolation boundary exists.
- Design sandboxing for optional drivers without giving them unrestricted filesystem or network access.
- Add cryptographic endpoint verification so a driver can confirm that an OS and BLE observation refer to the same physical device.
- Define driver protocol compatibility, upgrades, rollback, and deprecation policy.
- Test malicious or malformed driver responses, oversized messages, crashes, hangs, and resource leaks.
- Keep driver state namespaced and user-private.
- Preserve the rule that the base Betty executable contains no proprietary device parser or model table.
- Consider built-in or sandboxed providers only if they preserve the same CLI capability grammar.

## Apple Audio support

### Battery readings

- Validate exact AAP battery reads against real AirPods and Beats models on Linux and Windows.
- Compare left, right, case, charging, and headset values against each operating system where a trustworthy reference is available.
- Record which Apple Audio models expose headset versus left/right/case components and under what lid, charging, worn, or connection states.
- Add AAP stream fixtures for fragmented packets, unrelated notifications, missing components, malformed lengths, invalid statuses, key responses, and timeouts.
- Validate acquisition and persistence of the IRK and advertisement-encryption key.
- Validate encrypted BLE advertisement decoding after reconnects, address rotation, reboot, and state transfer.
- Continue labeling proximity-pairing advertisement values as estimated with 10% resolution.
- Never silently substitute estimated values when exact values were explicitly promised.

### Device matching

- Do not treat matching AirPods or Beats names as proof of endpoint identity.
- Use protocol-acquired keys or an authenticated AAP session as stronger evidence when possible.
- Test behavior with multiple nearby Apple Audio devices and multiple saved devices.
- Expose uncertainty rather than attaching battery values to an ambiguous Classic device.

### Noise control and other features

- Implement and validate AAP reads and writes for noise control only against explicitly selected hardware.
- Do not guess ANC packets or enable writes before hardware validation.
- Validate `off`, `anc`, `transparency`, and `adaptive` per model; not every model supports every value.
- Add current-state reads before enabling writes.
- Investigate transparency or ambient level, conversational awareness, equalizer, multipoint, wear detection, auto-pause, microphone mute, codec state, firmware version, and vendor-specific battery components.
- Keep unsupported capabilities explicit and model-specific.

### Protocol provenance

- Continue implementing protocol behavior independently rather than copying GPL implementation code.
- Preserve references to public protocol documentation and investigations used for behavioral understanding.
- Add packet-level comments and fixtures that record evidence without importing incompatible source.

## Linux

### BlueZ backend

- Add a fakeable BlueZ transport and native contract tests.
- Test the process-scoped `KeyboardDisplay` pairing agent on real PIN, passkey, confirmation, and authorization devices.
- Improve discovery using BlueZ object and property events.
- Verify behavior when BlueZ is absent, stopped, blocked, powered off, or controlled by another discovery client.
- Translate more BlueZ errors into stable Betty categories, including in-progress and already-completed operations.
- Decide and document idempotent behavior for already-paired, already-connected, and already-disconnected devices.
- Test systems with multiple adapters even though v1 intentionally uses the default adapter.

### Linux Apple Audio transport

- Hardware-test the native `AF_BLUETOOTH`/L2CAP channel to PSM `0x1001`.
- Confirm the minimum required capability on supported distributions and kernels.
- Verify the actual process that opens L2CAP has only the minimum required capability; never grant broad privileges to ordinary `bt` commands.
- Produce actionable diagnostics when the filesystem cannot store capabilities or the capability was stripped during copying/upgrading.
- Test channel cleanup on timeout, process termination, Bluetooth reset, and unexpected AirPods disconnection.

### Linux packaging

- Build and install the Debian packages on native Ubuntu and Debian runners.
- Verify that `betty` contains no Apple Audio files.
- Verify `betty-apple-audio` dependency/version matching, replacement of `betty-airpods`, driver discovery, exact file capabilities, upgrades, and clean uninstall.
- Preserve per-user keys during package removal and document how users can remove them explicitly.
- Add RPM packages for Fedora-family systems.
- Consider a portable tarball and an Arch `PKGBUILD` after the primary packages are stable.
- Generate checksums, an SBOM, and provenance for release artifacts.

## macOS

### Generic Bluetooth behavior

- Complete native hardware validation for Classic inquiry, pairing, connection, disconnection, BLE scanning, and GATT.
- Verify state transitions rather than relying only on native request completion.
- Test Bluetooth privacy permission denial and recovery.
- Record that unpairing is unavailable through the supported public API unless a safe supported route is found.
- Test Magic Keyboard, Magic Trackpad, controllers, generic BLE peripherals, and AirPods separately.

### AirPods limitations

- Preserve the explicit limitation that third-party processes cannot open the reserved AirPods AAP PSM through the available public APIs.
- Continue using authorized System Keychain access only for explicit AirPods battery reads.
- Never request System Keychain authorization during ordinary scanning.
- Validate key lookup across AirPods generations, multiple accounts, reconnects, and macOS upgrades.
- Ensure denial or missing keys falls back clearly to estimated advertisements rather than reporting an exact value.
- Revisit IOBluetooth and CoreBluetooth only when supported APIs or new evidence provide a safe transport; do not build around private frameworks casually.

### macOS distribution

- Create native packaging, likely a signed and notarized `.pkg` or a Homebrew formula for the CLI.
- Sign and notarize release executables as appropriate.
- Test Apple Silicon and Intel builds if both remain supported targets.
- Document terminal Bluetooth and Keychain permission behavior.

## Windows

### Generic backend

- Complete native Windows 10 and Windows 11 hardware validation.
- Test Windows-owned pairing confirmation UI, cancellation, and authentication errors.
- Expand WinRT or Win32 enumeration coverage where current Classic APIs omit devices.
- Keep generic OS profile connect and disconnect explicitly unsupported unless a reliable public API is found.
- Validate BLE scanning and GATT using real hardware.

### Apple Audio profile driver

- Build the KMDF project with the pinned WDK on native Windows.
- Validate binding only to the intended Apple AAP SDP service for both AirPods and Beats hardware IDs.
- Test connect, disconnect, send, bounded receive, cancellation, Bluetooth reset, surprise removal, sleep, and upgrade behavior.
- Fuzz and validate every userspace-to-kernel input length and state transition.
- Complete security review of the IOCTL surface, access control, concurrency, and teardown.
- Submit the driver through Microsoft Hardware Dev Center for the required Microsoft signature.
- Do not treat an ordinary Authenticode PFX as sufficient for kernel loading.
- Validate the returned catalog with kernel signing policy before packaging.

### Windows packages

- Build both MSIs natively:
  - `betty-windows-x64.msi`, containing only the generic CLI;
  - `betty-apple-audio-advanced-windows-x64.msi`, containing optional AirPods and Beats support.
- Test install, upgrade, rollback, repair, and uninstall on clean Windows 10 and Windows 11 machines.
- Verify that the base MSI never discovers or contains the Apple Audio driver.
- Verify that Advanced Support requires Betty and that Betty cannot be removed underneath it.
- Verify that Advanced Support removes its published OEM INF, kernel files, helper, registry state, and userspace driver without deleting Betty or per-user keys.
- Sign the executables and both MSI packages separately from Microsoft kernel signing.
- Decide whether the base MSI should be released even when an Advanced Support driver build or signing job is unavailable.

## Homebrew

### Naming and repository setup

- Do not use the formula name `betty`; Homebrew already has an unrelated formula with that name.
- Use `betty-bluetooth` as the formula name while continuing to install the `bt` executable.
- Create a public `VladCuciureanu/homebrew-tap` repository with a `Formula` directory.
- Initially support installation with:

  ```text
  brew install VladCuciureanu/tap/betty-bluetooth
  ```

- Consider submission to `homebrew/core` only after the project has stable releases, demonstrated public interest, and satisfies the current acceptance policy.

### Licensing and releases

- Choose and add an open-source, DFSG-compatible `LICENSE` file.
- Add the corresponding SPDX identifier to every formula.
- Publish an immutable stable tag and source archive.
- Never move or recreate a published release tag.
- Generate SHA-256 checksums for formula sources or artifacts.
- Decouple source release publication from Windows driver signing so a missing signed kernel package cannot prevent Homebrew from consuming a valid source release.

### Formula design

- Make `betty-bluetooth` build `cmd/bt` from source with Go as a build dependency.
- Do not compile or install the Apple Audio executable from the base formula.
- Add a separate `betty-apple-audio` formula in the personal tap.
- Make independently installed formulae share a Homebrew-safe driver discovery location such as the active prefix's `lib/betty/drivers` directory.
- Do not require `sudo`, silently add Linux capabilities, or mutate another formula's Cellar directory.
- Decide whether Linuxbrew AirPods support is passive-only by default with an explicit capability setup step for exact AAP access.
- Add a hardware-independent functional formula test such as JSON capability reporting.

### Homebrew CI

- Test source installation on supported Apple Silicon and Intel macOS runners.
- Test Linuxbrew if Betty claims Homebrew support on Linux.
- Run `brew audit --strict --online`, `brew install --build-from-source`, and `brew test` for each formula.
- Add automated formula updates only after release naming and checksums are stable.

## Sony headphones

- Hardware-test SDP service discovery and RFCOMM connection lifecycle on Linux, macOS, and Windows.
- Validate Sony v2 initialization and exact single, left/right, case, and charging battery reports.
- Capture and verify whether any v1 models expose battery through the Sony control protocol.
- Add Betty's generic capability get/set grammar before enabling Sony control writes.
- Hardware-validate noise control, ambient level, voice focus, EQ, DSEE, speak-to-chat, and model-specific features before changing their status from `planned`.
- Build an explicit tested-model table for v1 and v2 rather than treating a shared service UUID as proof that every feature is supported.
- Add native package lifecycle tests for the optional `betty-sony` package and ensure the base package contains no Sony protocol code.

## Release engineering

- Separate source releases from platform package/signing jobs so one optional platform artifact does not block all releases.
- Build releases natively on macOS, Linux, and Windows; cross-compilation is supplementary only.
- Add checksums for every artifact.
- Generate an SBOM and build provenance.
- Sign and notarize artifacts according to each platform's requirements.
- Test upgrade and uninstall paths from the previous stable version.
- Publish a compatibility table covering tested operating systems, adapters, Bluetooth devices, AirPods models, profiles, and known limitations.
- Verify ordinary commands make no network requests and leave no Betty daemon behind.

## Hardware validation matrix

Each supported operating system needs at least one disposable Classic device and one generic BLE peripheral. AirPods testing is additional.

### Generic hardware

- discovery and saved-device enumeration;
- pair and confirm paired state;
- connect and confirm connected state;
- disconnect and confirm disconnected state;
- unpair where supported;
- service and characteristic discovery;
- characteristic read and write in both supported modes;
- notification subscription and Ctrl-C cancellation;
- standard battery reading;
- adapter reset and unexpected disconnection.

### AirPods hardware

- passive estimated battery advertisement;
- exact AAP battery where the platform transport permits it;
- exact encrypted-advertisement battery after key acquisition or authorized import;
- left, right, case, headset, and charging states;
- closed case, open case, worn, charging, sleeping, and reconnect scenarios;
- multiple nearby AirPods and ambiguous endpoint matching;
- secure export/import of private driver state;
- noise-control reads and writes only after the packet protocol is implemented safely.

## Deferred ideas

- Multi-adapter selection.
- A long-running Betty daemon.
- Installable third-party plugin runtime.
- Proprietary protocols beyond explicitly supported, hardware-validated drivers.
- Sandboxed drivers with a stable public SDK.
- Automatic endpoint correlation without verifiable evidence.

These remain deferred unless a concrete use case justifies expanding the v1 scope.
