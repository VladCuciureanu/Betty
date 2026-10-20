# Betty Windows package

`build.ps1` produces two independent Windows release artifacts:

- `betty-windows-x64.msi`, the device-generic Betty CLI;
- `betty-apple-audio-advanced-windows-x64.msi`, optional AirPods and Beats Advanced Support.

The normal package contains only `bt.exe` and a system `PATH` entry. It has no Apple Audio parser, model data, setup helper, or kernel payload.

The optional Advanced Support package contains:

- the Apple Audio executable driver in Betty's read-only bundled-driver layout;
- `BettyAAP.sys`, its INF, and signed catalog;
- a small elevated setup helper that stages/binds the driver during install and unbinds/removes it during uninstall.

The KMDF driver is a narrow bridge. It binds only to the Apple AAP SDP service UUID, opens the requested encrypted/authenticated Classic L2CAP channel, and exposes connect, disconnect, send, and bounded receive operations through a Betty-owned device-interface GUID. All AAP parsing and feature behavior remains in the optional Go driver.

## Build requirements

- x64 Windows 11
- Visual Studio with C++ build tools
- Windows Driver Kit 10.0.28000.2526 (restored through its pinned NuGet package)
- Go 1.24
- .NET SDK capable of building the pinned WiX Toolset 7.0.0 SDK
  (the project owner has accepted the WiX v7 [Open Source Maintenance Fee EULA](https://docs.firegiant.com/wix/osmf/);
  both `.wixproj` files set `AcceptEula` to `wix7`)

Run:

```powershell
./packaging/windows/build.ps1 -Version 1.0.0
```

Use `-BaseOnly` to build only `betty-windows-x64.msi`; this path does not compile or require the Apple Audio userspace driver, setup helper, WDK project, or kernel package.

Release signing credentials are described in the root README. Windows 10/11 requires Microsoft Hardware Dev Center signing for new kernel drivers. Prepare the submission with:

```powershell
./packaging/windows/build-driver-submission.ps1 -Version 1.0.0
```

Upload the signed CAB to Hardware Dev Center for attestation or WHCP signing. After downloading Microsoft's signed package, build both release MSIs with:

```powershell
./packaging/windows/build.ps1 -Version 1.0.0 `
  -MicrosoftSignedDriverDir C:\signed\BettyAAP `
  -RequireMicrosoftDriverSignature -RequireSigning
```

The first requirement verifies the returned catalog using kernel signing policy. The second requires Authenticode credentials for Betty's executables and MSI. Tag releases enforce both and fetch the Microsoft-signed package from the protected `BETTY_MICROSOFT_SIGNED_DRIVER_URL` secret.

## Lifecycle

Both installations are per-machine and elevated. The normal MSI installs only Betty. Advanced Support requires the normal MSI, copies its payload, stages the driver package with `SetupCopyOEMInfW`, asks Windows to bind matching devices with `DiInstallDriverW`, and records the exact published OEM INF name. If a later MSI action fails, rollback uninstalls that package.

Advanced Support uninstall runs while the INF and setup helper still exist. It calls `DiUninstallDriverW`, removes the recorded package with `SetupUninstallOEMInfW`, removes only its own application files and machine state, and leaves Betty installed. The inverse rollback action reinstalls the package if MSI removal fails later. The normal Betty uninstall is blocked until Advanced Support is removed, after which it removes `bt.exe`, its `PATH` entry, and its remaining machine state.

Per-user state is outside MSI ownership and is preserved. This prevents one account uninstalling the application from silently destroying another account's exported or acquired Apple Audio keys.
