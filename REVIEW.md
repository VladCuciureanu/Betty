# Betty code and documentation review

Review of `feat/driver-sdk` at `ebfb94c`, October 2026. The uncommitted Apple
Audio SDK port is not included.

Five reviewers each read one area in full: CLI core, OS backends, transports
and the Windows kernel driver, the driver system, and the docs. I then checked
every high-severity finding against the source myself. A **✓** marks a finding
I confirmed by reading the code or running `bt`. **(hw)** marks a finding that
depends on OS API behavior and needs a hardware run to confirm.

`go vet` (linux, windows) and `go test ./...` pass. After stripping the CRLF
line endings, `gofmt` is clean.

## Bottom line

The architecture is sound. A small backend interface, structured errors with
stable exit codes, a versioned JSON envelope, and an out-of-process driver
boundary are the right foundations. Wherever a device-supplied byte is parsed,
the bounds checks are correct.

The problems are at the edges, where the code meets the OS, the user, and
third-party drivers:

- **Cancellation doesn't propagate.** Connections, pairing loops and child
  processes outlive the command that started them.
- **Failure is often reported as success, or as `internal`.** Most OS errors
  never reach their documented error codes.
- **The driver host trusts drivers more than the docs say.** One driver can
  read another driver's secrets.
- **Several platform paths probably can't work at all.** Windows RFCOMM is the
  clearest case. Nothing has been tested on hardware, which hid this.
- **About a third of the README's behavioral claims are false or overstated.**

## Fix first

These are security problems, or bugs that make a core feature wrong rather
than rough.

| # | Problem | Where |
| --- | --- | --- |
| 1 | **Fixed** (legacy IDs removed entirely). ✓ **A driver can read another driver's private storage.** A manifest may claim any `legacy_ids`; the host then serves that driver's storage as a fallback and hides the real driver. A third-party driver declaring `"legacy_ids":["official/apple-audio"]` reads the AirPods encryption keys. | `internal/driver/manager.go` `storageGet`, `storageSet`, `List`, `validateManifest` |
| 2 | **Fixed** (INF security descriptor allowing only SYSTEM, administrators and interactive users; read/write IOCTL access bits; CONNECT limited to the PDO's BTHENUM address; needs hardware validation). ✓ **The Windows kernel driver has no access control.** All IOCTLs are `FILE_ANY_ACCESS`, there's no SDDL, and `IOCTL_BETTY_CONNECT` accepts any address. Any local user can send raw AAP traffic to any device in range. Don't ship it as is. | `packaging/windows/driver/BettyAAP.h`, `Driver.c` |
| 3 | **Fixed.** ✓ **Same-name devices resolve at random.** The exact-name pass returns the first match and never reports ambiguity. The scan merge never joins a device's OS entry with its BLE entry, so on this machine `55" OLED` appears as both `windows:04:E4:…` and `ble:04:E4:…`. Which one `bt get`/`connect` acts on depends on goroutine timing. | `internal/resolver/resolver.go:19`, `internal/app/app.go` `merge`/`sameEndpoint` |
| 4 | **Fixed** (hw: the packed layout is unit-tested, a real connect is not). ✓ **Windows RFCOMM uses the wrong socket-address layout.** `SOCKADDR_BTH` is a packed 30-byte struct with the address at offset 2. The Go struct pads the address to offset 8, and a unit test locks that in. Every connect goes to the wrong address. (hw) | `internal/rfcomm/rfcomm_windows.go:61` |
| 5 | **Fixed.** ✓ **The macOS transports have a use-after-free on cancel.** `defer C.free(cAddress)` runs while the open goroutine is still inside the C call that reads the string. | `internal/l2cap/l2cap_darwin.go:30`, `internal/rfcomm/rfcomm_darwin.go` |
| 6 | **Fixed** (hw): the loop honors the deadline and cancellation and calls `[pair stop]`, a nil device reports `device_not_found`, and PIN, confirmation and passkey callbacks prompt through the pairing agent. ✓ **macOS pairing can spin forever.** The run-loop wait has no timeout and never calls `[pair stop]`. A nil device makes `start` return success and the loop never ends. Passkey/confirmation callbacks aren't implemented. (hw) | `internal/backend/bridge_darwin.m:27` |
| 7 | **Fixed.** ✓ **A cancelled BLE connect leaks a live connection.** The connect goroutine keeps running and nobody disconnects its result. | `internal/ble/ble.go:124` |
| 8 | **Fixed.** ✓ **The driver host can leak or hang child processes.** Early returns skip `cmd.Wait()`. The host never drains stdout or sets `WaitDelay`. There's no per-driver timeout, so one slow driver uses up the whole command's time. Reads from drivers have no size limit. (A reviewer also said `bt watch` has no deadline; that was wrong, because each watch round runs under `--timeout`.) | `internal/driver/manager.go` `call` |
| 9 | **Fixed.** ✓ **The driver host accepts any non-host message as the final response.** `type` and `id` are never checked, so a stray line ends the session as an empty success. | `internal/driver/manager.go` `call` |
| 10 | **Fixed** (a key applies only if its IRK resolves the advertiser's private address, where visible, and its decrypted levels agree with the advertisement's unencrypted estimates). ✓ **Apple encrypted-battery decryption accepts wrong keys** about 2 times in 256 per key. Keys saved with `ModelID` 0 match every model, so a nearby stranger's AirPods can show an "exact" random battery value. | `cmd/bt-driver-apple-audio/keys.go:83` |

## Findings by area

### CLI core (`internal/app`, `output`, `resolver`, `domain`)

High:

- **Fixed.** ✓ `--verbose` does nothing. `Renderer.Verbose` is never read.
- **Fixed.** ✓ `bt -h` and `bt --json --help` fail with the usage error "flag: help requested".
- **Fixed.** ✓ Ctrl-C mid-operation is reported as an `internal` error with exit 1, because only `DeadlineExceeded` is translated and `context.Canceled` isn't.
- **Fixed.** Successful `pair`, `connect`, `gatt write` and similar commands print nothing, even with `--json`, so a script can't tell success from silence.
- **Fixed.** In human mode, ambiguity errors drop the list of candidate devices, so the user has no IDs to retry with.

Medium:

- **Fixed.** ✓ Every `get` and `set` runs a discovery scan of at least ~4s first. `watch` repeats it on every poll, so the scans run back to back. Any transient error ends the watch.
- **Fixed.** ✓ Extra arguments are silently ignored. `bt --json scan extra` succeeds.
- **Fixed.** An unknown command, such as the typo `bt lsit`, runs a ~4s scan and then fails with `device_not_found` (exit 3) instead of `usage` (exit 2).
- **Fixed.** Scan errors are dropped whenever the other source returned devices.
- **Fixed.** An empty scan prints `"data":null` instead of `[]`.
- **Fixed.** `requiresDeviceDriver` hardcodes vendor property names in the core, which crosses the driver boundary.
- **Fixed.** Capability status and kind are untyped strings, while `domain.Support` is typed.
- **Fixed.** `--timeout 0` and negative timeouts aren't validated.
- **Fixed.** `validateRange` rejects valid negative values when `step` is set without `minimum`.
- **Fixed.** `pair` spends up to 4s of its 10s budget scanning before the user can enter a PIN.

Idiom:

- **Fixed.** A local `min` shadows the builtin, and `contains` duplicates `slices.Contains`.
- **Fixed.** Many functions have no blank line between them.
- **Fixed.** `parseGlobal` has an unused `stderr` parameter.
- **Fixed.** `fmt.Errorf` is called with no format verbs.
- **Fixed.** Human output sorts the caller's slice in place, with an unstable sort.
- **Fixed.** Help and version are handled in two places.

**Fixed.** Tests miss the resolver's duplicate exact names, `mutate`, `set`, `watch`, `gatt`, cancellation, and exit codes from `Main`.

### OS backends and BLE (`internal/backend`, `ble`, `hidbattery`)

High:

- **Fixed.** ✓ Windows classifies errors from a possibly stale `GetLastError` instead of the API's return code. For example, unpairing an unknown device reports `bluetooth_unavailable`.
- **Fixed** (now a `usage` error). ✓ A malformed BLE address is silently parsed as `00:00:00:00:00:00`, because `addr.Set` reports no error and none is checked.
- **Fixed.** tinygo errors are never mapped to domain codes, so every BLE failure becomes `internal`.
- **Fixed.** On macOS, messages to a nil device return 0, which reads as success. `disconnect` "succeeds" on a nonexistent device.
- **Fixed** (hw: the table is unit-tested against error names, not a live bluetoothd). Most BlueZ/D-Bus errors fall through to `internal`: bluetoothd not running, stale object, access denied, "host is down". Cancelling pairing doesn't call `CancelPairing`, so BlueZ keeps going.

Medium:

- **Fixed** (`Capabilities` now reports scan as unsupported). macOS `Scan` only lists paired devices, yet `Capabilities` says scan is supported.
- **Fixed** (every `.m` file now uses manual retain/release and refuses to build under ARC). `hidbattery/provider_darwin.m` is compiled without ARC and leaks `+1` objects; `bridge_darwin.m` uses manual release, so memory management is inconsistent across files.
- Opening `IOHIDManager` with NULL matching may trigger the macOS Input Monitoring prompt on a plain `bt list`. (hw)
- **Partly fixed:** `bthprops.cpl` now loads without panicking, the search parameters are laid out correctly on 32-bit, and `Paired` comes from `Authenticated` only. Still open: `Pair` can't be cancelled, and `BLUETOOTH_DEVICE_INFO` is misaligned on 32-bit x86, where Go aligns `uint64` to 4. Windows `Pair` blocks with no way to cancel. `bthprops.cpl` is loaded lazily and panics if missing. The struct padding is only correct on 64-bit. `Paired` is set from `Remembered`, which doesn't mean paired.
- **Fixed** (hw for the disconnect watch). BLE scan keeps only the last advertisement per device, so a packet without a name resets the name to the address. GATT `watch` never notices a disconnect.
- **Fixed.** On Linux, the retry branch in the state-polling loop can never run, and `errorAs` reimplements `errors.As`. The adapter is chosen in random map order.

### Transports and kernel driver (`internal/l2cap`, `rfcomm`, `packaging/windows/driver`)

High, beyond the "Fix first" items:

- **Fixed** (hw). Windows RFCOMM detects connect failure only through `writefds`, but Winsock reports a failed non-blocking connect in `exceptfds`. A refused connect hangs until the deadline.
- **Fixed.** Windows RFCOMM reads `WSAGetLastError` in a separate call, so the goroutine can move to another OS thread and get a stale value.
- **Fixed** (now `device_disconnected`). ✓ On Linux, L2CAP treats a zero-byte read (the peer closed) as an empty packet, so the AAP driver spins. RFCOMM handles this case correctly.
- macOS RFCOMM probably always fails its first SDP lookup, because `performSDPQuery:` is asynchronous. Delegates may dangle after close, and nothing pumps the run loop for data callbacks. (hw)
- **Fixed** (a passive-level work item closes the channel with `BRB_L2CA_CLOSE_CHANNEL` under the lock). The kernel driver leaks the L2CAP channel on remote disconnect and clears it without taking the lock. (hw)

Medium:

- **Fixed** (Linux L2CAP and RFCOMM). Sub-millisecond deadlines round down to 0. A socket that is still connecting can then be registered as open.
- Windows uses `SO_RCVTIMEO` and retries, but the socket is in an indeterminate state after such a timeout.
- Windows L2CAP reads ignore the context and hold the channel lock for up to 5s.
- **Fixed** (the host reads each instance's bound address and connects only through the matching one). The Go side sends `CONNECT` to every kernel-driver instance, so it can connect through an instance bound to a different headset.
- On Linux, the fd is read outside the lock; this is a latent reuse race.
- The `.m` files are written as single lines of 400–900 characters.
- `waitFD`, the address parsers and `maxDuration` are copied between packages.
- The macOS L2CAP hardware test opens a PSM that the code says macOS always rejects, so it can never pass.

### Driver system (`internal/driver`, `driver`, the drivers)

Beyond the "Fix first" items:

- **Fixed** (an advisory lock file in the driver root serializes storage writes, installs and removals). Storage read-modify-write had no file lock, so concurrent `bt` processes lost updates. The Apple driver also no longer saves over a keyring it failed to load.
- **Fixed** (the check is repeated under the lock). Two concurrent installs could both pass the "already installed" check. Install also refuses an ID that a bundled or package-managed driver already provides, so it can't shadow one.
- **Fixed** (one stream handler for both transports and a namespace-to-permission table). `handleHost` was 176 lines of near-identical L2CAP and RFCOMM branches, with permissions checked by hand-sliced string prefixes.
- **Fixed** (channels are keyed by transport and ID; each cleanup step has its own 2s deadline). L2CAP and RFCOMM channels shared one map keyed by a bare ID.
- **Fixed.** ✓ Sony `get battery` reports `sonyCapabilities` instead of `sonyCapabilitiesFor`. With controls enabled, `noise_control` reverts to "planned". The bug predates the SDK port, which carried it over.
- The Sony frame stream gives up at the first bad frame, and its ACK handling recurses.
- **Fixed.** The Apple driver duplicated its AAP handshake across two functions; both now use one session type. The driver is on the SDK. A noise-control write also waits for the device to report the requested mode instead of failing on the first report of the old one, and no longer reports success without writing when a device skips feature negotiation.
- **Fixed.** The darwin build needs cgo: the stub for `systemAppleAudioKeys` should be tagged `!darwin || !cgo`.
- **Fixed** (256 keys and 1 MiB per driver). The storage-key regex was recompiled on every call, the number of storage keys was unlimited, and `ImportState` read the whole file before checking its size.

### Still open after the backend fixes

- On Windows, tinygo's connection-status handler already calls
  `Disconnect`, which releases the device's COM objects, when the link
  drops. A GATT `watch` that ends that way then calls `Disconnect` again.
  (hw)
- On Linux, tinygo's `Connect` waits for the `Connected` property with no
  timeout, so the goroutine of a cancelled BLE connect can stay blocked.
  Betty now disconnects the result if it ever arrives, but can't abort the
  attempt itself.
- This pass didn't touch the `IOHIDManager` Input Monitoring prompt, the
  Windows `SO_RCVTIMEO` retries, macOS RFCOMM's asynchronous SDP query or
  the duplicated `waitFD` helpers. They remain as listed above.

### Found by the end-to-end suite

Running `bt` against scripted devices on Linux (see [e2e/README.md](e2e/README.md))
found four bugs that unit tests could not, all fixed with the suite:

- **Fixed.** The BLE half of every Linux scan failed with "Operation already
  in progress": the backend and the BLE library shared godbus's system-bus
  connection, and BlueZ allows one discovery per D-Bus client.
- **Fixed.** `bt pair` could not find a new Classic device on Linux: the
  lookup scan stopped after 4s, before BlueZ's discovery reaches Classic
  inquiry. It now scans for up to 8s.
- **Fixed.** Every RFCOMM connection on Linux went to the byte-reversed
  address: `unix.SockaddrRFCOMM` expects little-endian order, while
  `unix.SockaddrL2` reverses display order itself. The Sony driver could not
  reach real headphones on Linux.
- **Fixed.** The Sony driver reported only its last failed RFCOMM attempt,
  hiding the informative one.

The suite also confirms on Linux, through BlueZ: GATT reads, writes in both
modes, and notifications; property `watch`; a dropped link ending a GATT watch
with `device_disconnected`; duplicate names being ambiguous; and a refused
pairing reporting `authentication_failed`. Classic `connect` and the Sony
driver still need an over-the-air run (Windows, macOS) or real hardware.

## Documentation claims

FALSE or misleading claims that users or integrators would act on:

| Doc | Claim | Reality |
| --- | --- | --- |
| README | "never silently chooses between ambiguous matches" | **Corrected.** ✓ False for duplicate exact names (finding 3). |
| README | `--verbose` "includes additional diagnostics" | **Corrected.** ✓ The flag does nothing. |
| README | GATT writes "choose a mode supported by the characteristic" | **Corrected.** ✓ Writes always use with-response unless `--without-response` is passed. |
| README | "Successful mutating commands are silent" | **Corrected.** `set` and `driver install` print output. |
| README | Windows connect/disconnect "not reliably available for every profile" | **Corrected.** ✓ Always `unsupported`. |
| README | macOS connect/disconnect "Implemented" | **Corrected.** It opens a baseband link, not profiles. |
| README | macOS pair "Implemented" | **Corrected.** Unpaired Classic devices are never discovered, so they can't be paired. |
| README | `--non-interactive` "fails clearly" | **Corrected.** Only Linux honors it; Windows and macOS can still show OS pairing UI. |
| README | host access limited to "selected device IDs" | **Corrected.** Drivers can reach every device in the request, which means every known device. |
| README / JSON_API | JSON contract | **Corrected.** Neither says that errors go to stderr. `bt --json --quiet` prints a plain-text error, and `bt --json --version` returns a usage error. |
| DRIVER_PROTOCOL | Drivers MUST loop until EOF and answer unknown methods with `unsupported`; Apple Audio is a reference driver | **Corrected.** Apple Audio handles one request and sends errors without `error_code`. The SDK port fixes this. |
| DRIVER_PROTOCOL | `bt watch` polls "about every 2 seconds" | **Corrected.** ✓ The real cadence is ≥4s, because each poll rescans, and any error ends the watch. |
| DRIVER_PROTOCOL | The `official/` publisher is "reserved" | **Corrected.** Nothing enforces it. |
| DRIVER_PROTOCOL | Permission table | **Corrected.** Omits `advertisements`, which the Apple manifest declares. Unknown permissions are accepted silently. |
| DRIVER_PROTOCOL | Betty "kills the driver" on timeout | **Fixed.** It surfaced as `internal` (exit 1); it now reports `timeout`. |
| Apple README | A nearby advertisement "is not proof" of identity | **Corrected.** With one advertisement and one connected device, its battery is copied onto the saved endpoint anyway. |
| Sony README | Battery "available for v2" | **Corrected.** v1 and name-only devices also report battery as `available`, then fail on `get`. |
| packaging/windows | "Signing credentials are described in the root README" | **Corrected.** They aren't. |
| packaging/windows | "Tag releases enforce both" MSIs | **Corrected.** The Advanced MSI is skipped when its secret is unset. |
| packaging/homebrew | The tap "lives in" `homebrew-tap` | **Corrected.** The roadmap says it hasn't been created. |
| COMPATIBILITY | Driver matrix | **Corrected.** ✓ The header has 9 columns and the delimiter row 10, so GitHub won't render the table. |
| FUTURE_WORK | Sony "route v2 battery through get" is unchecked | **Corrected.** Already done. |

Four of these are mine, from the driver-SDK work: the Apple reference-driver
claim, the watch cadence, `official/` "reserved", and the missing
`advertisements` permission.

Claims that hold up:

- the exit codes and retryability;
- the error envelope shape;
- `--quiet`;
- the snapshot/changed watch events;
- no network calls;
- address-only HID correlation on macOS;
- the driver discovery order and install layout;
- the protocol limits;
- the host-call permission and channel-ownership checks;
- the Debian package relationships;
- the Homebrew formula names;
- the release checksums, SBOM and attestation steps;
- the hardware workflow refusing to run with unconfigured devices;
- MSI contents;
- every relative link.

## Suggested order

1. **Security:** finding 1 (legacy-ID storage) and finding 2 (kernel driver
   access control). Keep the Advanced MSI out of releases until 2 is fixed.
2. **Driver host robustness (findings 8 and 9):** per-driver timeout, kill
   and wait, `WaitDelay`, bounded reads, and checking the response's type and
   ID. Third-party drivers depend on this.
3. **Correctness people will hit first:**
   - resolver ambiguity and endpoint merge (3);
   - `-h`, Ctrl-C handling and `--verbose`;
   - JSON output for successful mutations;
   - a per-platform error-translation table.
4. **Memory safety and leaks:**
   - the darwin use-after-free (5);
   - the BLE connect leak (7);
   - the macOS pairing loop (6);
   - the Linux L2CAP peer-close spin.
5. **Docs:** correct every FALSE row above. That's an afternoon's work, and it
   stops the README overpromising.
6. **Hardware pass** to confirm or rule out the (hw) items, starting with
   Windows RFCOMM (4) and macOS RFCOMM SDP.
7. **Cleanup:**
   - collapse the duplicated L2CAP/RFCOMM code in the host and the
     transports;
   - type the capability strings;
   - reformat the `.m` files;
   - add the missing tests.
