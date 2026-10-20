# End-to-end tests

These tests run the real `bt` binary and drivers against **scripted Bluetooth
devices**. Nothing between `bt` and the device is mocked: commands go through
the operating system's Bluetooth stack, across a Bluetooth link, to a device
simulated by [Bumble](https://github.com/google/bumble).

| | `e2e/` (this directory) | [`hardware/`](../hardware) |
| --- | --- | --- |
| The other end | A scripted device | A real device you own |
| Repeatable | Yes; failures can be injected | Depends on the device |
| Proves real-device compatibility | No | Yes |

The scripted devices implement Betty's own understanding of each protocol,
so a passing run shows that Betty works end to end, not that real Sony or Apple
firmware behaves the same way. Record results under "Emulated" in
[COMPATIBILITY.md](../COMPATIBILITY.md), never as native evidence.

## Where they run

| OS | Link | How it runs |
| --- | --- | --- |
| Linux | Fully virtual: no radio | In CI on every pull request (`.github/workflows/e2e-linux.yml`), or locally in a VM |
| Windows | Over the air, between the built-in radio and a USB dongle | Manually |
| macOS | Over the air, between the built-in radio and a USB dongle | Manually |

On Linux, the peer creates the computer's Bluetooth controller itself: a
software controller attached to the kernel through `/dev/vhci`, sharing a
virtual link with the scripted devices. The kernel and BlueZ treat it as real
hardware. Windows and macOS cannot attach virtual controllers, so there the
scripted device runs on a dedicated USB dongle and talks to the computer's
built-in radio over the air.

## The scripted devices

`peer/` is a small Python package, `betty_peer`, with these personas:

| Persona | What it is |
| --- | --- |
| `sensor` | A BLE peripheral: Battery Service (87%), Device Information (firmware 1.2.3), and a test service with a writable characteristic and a notifying one. |
| `speaker` | A Classic device that pairs with Just Works. It can be told to refuse pairing. |
| `keyboard` | A Classic HID keyboard. BlueZ connects it with its own input plugin, so it exercises `connect` and `disconnect`. |
| `sony` | A Classic Sony v2 headset: SDP record and RFCOMM control channel, answering session setup and battery queries (73%). |

The tests start `python -m betty_peer` and control it with one JSON command
per line on stdin, such as `{"cmd":"drop","device":0}`; it reports events
such as `connected` on stdout. `python -m betty_peer --help` documents the
protocol.

## Running on Linux

You need KVM, [virtme-ng](https://github.com/arighi/virtme-ng), a stock
Ubuntu-style `generic` kernel with its modules installed (it doesn't need to be
the running kernel), BlueZ, and dbus. On Ubuntu:

```console
sudo apt-get install linux-image-generic virtme-ng qemu-system-x86 bluez dbus strace
sudo apt-get install linux-modules-extra-$(ls /lib/modules | grep -- -generic | sort -V | tail -n 1)
sudo chmod a+r /boot/vmlinuz-*
python3 -m venv e2e/peer/.venv
e2e/peer/.venv/bin/pip install ./e2e/peer
e2e/linux/run-vm.sh -v
```

The VM keeps the test run away from your own Bluetooth adapter and from the
host's BlueZ. Arguments after `run-vm.sh` go to `go test`, for example
`-run TestGATT`. When the suite fails, the script prints the end of
bluetoothd's log and the discovery, pairing, and connection traffic from an
HCI trace. Set `BETTY_PEER_LOG=DEBUG` to also trace the scripted devices' own
Bluetooth stack.

### What the virtual link cannot do

The Sony scenario stalls on the virtual link after its RFCOMM channel is
encrypted, so it is skipped there and runs only over the air
([#100](https://github.com/VladCuciureanu/Betty/issues/100)). Everything else,
Classic `connect` and `disconnect` included, runs on both.

The scripted devices also run on a patched controller
(`peer/betty_peer/controller.py`). It reports dual-mode support, answers Classic
inquiry and the setup commands Linux sends, re-authenticates paired Classic
devices with their stored link key, reports Classic encryption, and keeps the
link working when Linux changes its private address. Each patch is
commented with the gap it fills.

## Running on Windows or macOS

You need a USB Bluetooth dongle that Bumble can drive, dedicated to the tests,
in addition to the computer's own Bluetooth. Dongles with Realtek's RTL8761B
chip, such as the TP-Link UB500 or UGREEN CM390, work; see
[Bumble's hardware list](https://google.github.io/bumble/hardware/index.html).
The built-in Bluetooth stays with the operating system; the dongle belongs to
the scripted device.

1. Give the dongle to Bumble.
   - **Windows:** use [Zadig](https://zadig.akeo.ie) to switch the dongle's
     driver to WinUSB. This takes the dongle away from Windows' Bluetooth stack
     until you switch it back in Device Manager.
   - **macOS:** nothing to do; macOS has no driver for these dongles.
2. Install the peer and find the dongle:

   ```console
   python -m venv e2e/peer/.venv
   e2e/peer/.venv/bin/pip install ./e2e/peer            # Windows: e2e\peer\.venv\Scripts\pip
   e2e/peer/.venv/bin/bumble-usb-probe
   ```

   The probe lists USB Bluetooth controllers. Use the index, as in `usb:0`, or
   the `usb:VENDOR:PRODUCT` form it prints. Realtek dongles need their
   firmware, which Bumble loads at startup: fetch it once with
   `bumble-rtk-fw-download` from the same environment.
3. Run the suite:

   ```console
   BETTY_E2E_TRANSPORT=usb:0 BETTY_E2E_PYTHON=e2e/peer/.venv/bin/python go test -tags=e2e -count=1 -v ./e2e
   ```

   On Windows, set the variables with `$env:BETTY_E2E_TRANSPORT = "usb:0"` and
   point `BETTY_E2E_PYTHON` at `e2e\peer\.venv\Scripts\python.exe`.

Expect Windows to show its own pairing notifications during the pairing tests.

### What runs where

- Tests that need several scripted devices at once, such as duplicate names,
  run only on Linux, because a dongle is one device.
- The Sony test runs only over the air; see
  [what the virtual link cannot do](#what-the-virtual-link-cannot-do).
- The Sony test needs the headset to connect back to the computer. Over the
  air, set `BETTY_E2E_HOST_ADDRESS` to the computer's own Bluetooth address.
  On Windows, `ipconfig /all` shows it as the physical address of the
  Bluetooth network adapter; on macOS, `system_profiler SPBluetoothDataType`
  shows it.
- macOS skips the Classic pairing tests: Betty cannot yet discover new Classic
  devices there.
- Windows expects `connect` and `disconnect` to fail with `unsupported`.
- The isolation test, which traces `bt` and its drivers with `strace` to check
  that ordinary commands open no network socket and leave no process behind,
  runs only on Linux, and only where `strace` is installed.

`e2e/drivers/gattprobe` is a test driver that reads the sensor through
Betty's GATT host calls, so the suite also covers the driver protocol's
`gatt.*` calls against BlueZ.

## Adding a scenario

Add or extend a persona in `peer/betty_peer/personas.py`, and add any new
command to `run_command` in `peer/betty_peer/__main__.py`. Then write the test
in `e2e_test.go`, using the helpers in `harness_test.go`:

- `startPeer` runs scripted devices.
- `ok` and `fails` run `bt` and check the result.
- `stream` runs a long-lived command such as `watch`.
- `peer.do` and `peer.wait` drive the device and wait for its events.

Pass names through `startPeer`: it adds a per-run suffix, and the peer varies
addresses per run, so the host's Bluetooth stack never confuses a device with
one it cached in an earlier test.
