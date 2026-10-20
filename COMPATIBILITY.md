# Betty compatibility evidence

This table records native hardware results. A blank or `Not tested` cell is not
a support claim. Add the Betty commit, operating-system build, Bluetooth adapter,
device firmware, and observed limitation to every test record.

## Generic Bluetooth v1 matrix

| Platform | OS build | Adapter | BLE peripheral | Classic device | Scan/list | Pair/unpair | Connect/disconnect | GATT discover/read/write/watch | Battery | Evidence |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| macOS | Not tested | Not tested | Not tested | Not tested | Not tested | Not tested | Not tested | Not tested | Not tested | — |
| Linux / BlueZ | Not tested | Not tested | Not tested | Not tested | Not tested | Not tested | Not tested | Not tested | Not tested | — |
| Windows 10 | Not tested | Not tested | Not tested | Not tested | Not tested | Not tested | Not tested | Not tested | Not tested | — |
| Windows 11 | Not tested | Not tested | Not tested | Not tested | Not tested | Not tested | Not tested | Not tested | Not tested | — |

Known public-API limitations:

- macOS does not expose generic unpairing through a supported public API.
- Windows cannot generically force every saved Classic profile to connect or disconnect.
- Device IDs are opaque and platform-local; they are not portable Bluetooth addresses.

## Optional driver matrix

| Driver | Platform | OS build | Adapter | Device / firmware | Battery | Controls | Identity confidence | Evidence / limitation |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Apple Audio | macOS | Not tested | Not tested | Not tested | Not tested | Not supported | Not tested | Encrypted BLE requires authorized System-keychain keys; AAP PSM is unavailable to third-party apps. |
| Apple Audio | Linux | Not tested | Not tested | Not tested | Not tested | Planned | Not tested | Native AAP hardware validation required. |
| Apple Audio | Windows | Not tested | Not tested | Not tested | Not tested | Planned | Not tested | Exact active access requires the separately installed, Microsoft-signed profile driver. |
| Sony Headphones | macOS | Not tested | Not tested | Not tested | Not tested | Planned | Not tested | Native RFCOMM hardware validation required. |
| Sony Headphones | Linux | Not tested | Not tested | Not tested | Not tested | Planned | Not tested | Native RFCOMM hardware validation required. |
| Sony Headphones | Windows | Not tested | Not tested | Not tested | Not tested | Planned | Not tested | Native RFCOMM hardware validation required. |

## Emulated end-to-end runs

These come from [the end-to-end suite](e2e/README.md), which runs `bt` against
scripted devices. They show that Betty works through each OS's Bluetooth stack.
They are **not** support claims: the scripted devices implement Betty's own
reading of each protocol, so they cannot prove that real devices behave the
same way.

| Platform | Link | How | Result | Evidence |
| --- | --- | --- | --- | --- |
| Linux / BlueZ 5.72 | Virtual (`/dev/vhci`), Ubuntu generic kernel in a VM | CI, every pull request | Scan, GATT read/write/notify, property watch, link drop, ambiguity, pairing, refused pairing, unpairing pass. Classic connect and Sony battery are skipped: the virtual controller cannot re-authenticate a paired device. | `.github/workflows/e2e-linux.yml` |
| Windows | Over the air, built-in radio to a USB dongle | Manual | Not run yet | — |
| macOS | Over the air, built-in radio to a USB dongle | Manual | Not run yet | — |

## Recording a run

Attach the native hardware workflow log or a local transcript containing:

1. `bt --version` and the source commit;
2. OS version and Bluetooth adapter identity;
3. device model and firmware;
4. schema-version-1 output with stable IDs and private keys redacted;
5. each attempted operation, its exit code, and whether state was verified;
6. cleanup confirmation, including unpairing disposable devices and uninstalling packages.

Never publish exported driver state, IRKs, advertisement keys, passkeys, or PINs.
