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
| 1 | **Fixed.** ✓ **A driver can read another driver's private storage.** A manifest may claim any `legacy_ids`; the host then serves that driver's storage as a fallback and hides the real driver. A third-party driver declaring `"legacy_ids":["official/apple-audio"]` reads the AirPods encryption keys. | `internal/driver/manager.go` `storageGet`, `storageSet`, `List`, `validateManifest` |
| 2 | ✓ **The Windows kernel driver has no access control.** All IOCTLs are `FILE_ANY_ACCESS`, there's no SDDL, and `IOCTL_BETTY_CONNECT` accepts any address. Any local user can send raw AAP traffic to any device in range. Don't ship it as is. | `packaging/windows/driver/BettyAAP.h`, `Driver.c` |
| 3 | ✓ **Same-name devices resolve at random.** The exact-name pass returns the first match and never reports ambiguity. The scan merge never joins a device's OS entry with its BLE entry, so on this machine `55" OLED` appears as both `windows:04:E4:…` and `ble:04:E4:…`. Which one `bt get`/`connect` acts on depends on goroutine timing. | `internal/resolver/resolver.go:19`, `internal/app/app.go` `merge`/`sameEndpoint` |
| 4 | ✓ **Windows RFCOMM uses the wrong socket-address layout.** `SOCKADDR_BTH` is a packed 30-byte struct with the address at offset 2. The Go struct pads the address to offset 8, and a unit test locks that in. Every connect goes to the wrong address. (hw) | `internal/rfcomm/rfcomm_windows.go:61` |
| 5 | ✓ **The macOS transports have a use-after-free on cancel.** `defer C.free(cAddress)` runs while the open goroutine is still inside the C call that reads the string. | `internal/l2cap/l2cap_darwin.go:30`, `internal/rfcomm/rfcomm_darwin.go` |
| 6 | ✓ **macOS pairing can spin forever.** The run-loop wait has no timeout and never calls `[pair stop]`. A nil device makes `start` return success and the loop never ends. Passkey/confirmation callbacks aren't implemented. (hw) | `internal/backend/bridge_darwin.m:27` |
| 7 | ✓ **A cancelled BLE connect leaks a live connection.** The connect goroutine keeps running and nobody disconnects its result. | `internal/ble/ble.go:124` |
| 8 | **Fixed.** ✓ **The driver host can leak or hang child processes.** Early returns skip `cmd.Wait()`. The host never drains stdout or sets `WaitDelay`. There's no per-driver timeout, so one slow driver uses up the whole command's time. Reads from drivers have no size limit. (A reviewer also said `bt watch` has no deadline; that was wrong, because each watch round runs under `--timeout`.) | `internal/driver/manager.go` `call` |
| 9 | **Fixed.** ✓ **The driver host accepts any non-host message as the final response.** `type` and `id` are never checked, so a stray line ends the session as an empty success. | `internal/driver/manager.go` `call` |
| 10 | ✓ **Apple encrypted-battery decryption accepts wrong keys** about 2 times in 256 per key. Keys saved with `ModelID` 0 match every model, so a nearby stranger's AirPods can show an "exact" random battery value. | `cmd/bt-driver-apple-audio/keys.go:83` |

## Findings by area

### CLI core (`internal/app`, `output`, `resolver`, `domain`)

High:

- ✓ `--verbose` does nothing. `Renderer.Verbose` is never read.
- ✓ `bt -h` and `bt --json --help` fail with the usage error "flag: help requested".
- ✓ Ctrl-C mid-operation is reported as an `internal` error with exit 1, because only `DeadlineExceeded` is translated and `context.Canceled` isn't.
- Successful `pair`, `connect`, `gatt write` and similar commands print nothing, even with `--json`, so a script can't tell success from silence.
- In human mode, ambiguity errors drop the list of candidate devices, so the user has no IDs to retry with.

Medium:

- ✓ Every `get` and `set` runs a discovery scan of at least ~4s first. `watch` repeats it on every poll, so the scans run back to back. Any transient error ends the watch.
- ✓ Extra arguments are silently ignored. `bt --json scan extra` succeeds.
- An unknown command, such as the typo `bt lsit`, runs a ~4s scan and then fails with `device_not_found` (exit 3) instead of `usage` (exit 2).
- Scan errors are dropped whenever the other source returned devices.
- An empty scan prints `"data":null` instead of `[]`.
- `requiresDeviceDriver` hardcodes vendor property names in the core, which crosses the driver boundary.
- Capability status and kind are untyped strings, while `domain.Support` is typed.
- `--timeout 0` and negative timeouts aren't validated.
- `validateRange` rejects valid negative values when `step` is set without `minimum`.
- `pair` spends up to 4s of its 10s budget scanning before the user can enter a PIN.

Idiom:

- A local `min` shadows the builtin, and `contains` duplicates `slices.Contains`.
- Many functions have no blank line between them.
- `parseGlobal` has an unused `stderr` parameter.
- `fmt.Errorf` is called with no format verbs.
- Human output sorts the caller's slice in place, with an unstable sort.
- Help and version are handled in two places.

Tests miss the resolver's duplicate exact names, `mutate`, `set`, `watch`, `gatt`, cancellation, and exit codes from `Main`.

### OS backends and BLE (`internal/backend`, `ble`, `hidbattery`)

High:

- ✓ Windows classifies errors from a possibly stale `GetLastError` instead of the API's return code. For example, unpairing an unknown device reports `bluetooth_unavailable`.
- ✓ A malformed BLE address is silently parsed as `00:00:00:00:00:00`, because `addr.Set` reports no error and none is checked.
- tinygo errors are never mapped to domain codes, so every BLE failure becomes `internal`.
- On macOS, messages to a nil device return 0, which reads as success. `disconnect` "succeeds" on a nonexistent device.
- Most BlueZ/D-Bus errors fall through to `internal`: bluetoothd not running, stale object, access denied, "host is down". Cancelling pairing doesn't call `CancelPairing`, so BlueZ keeps going.

Medium:

- macOS `Scan` only lists paired devices, yet `Capabilities` says scan is supported.
- `hidbattery/provider_darwin.m` is compiled without ARC and leaks `+1` objects; `bridge_darwin.m` uses manual release, so memory management is inconsistent across files.
- Opening `IOHIDManager` with NULL matching may trigger the macOS Input Monitoring prompt on a plain `bt list`. (hw)
- Windows `Pair` blocks with no way to cancel. `bthprops.cpl` is loaded lazily and panics if missing. The struct padding is only correct on 64-bit. `Paired` is set from `Remembered`, which doesn't mean paired.
- BLE scan keeps only the last advertisement per device, so a packet without a name resets the name to the address. GATT `watch` never notices a disconnect.
- On Linux, the retry branch in the state-polling loop can never run, and `errorAs` reimplements `errors.As`. The adapter is chosen in random map order.

### Transports and kernel driver (`internal/l2cap`, `rfcomm`, `packaging/windows/driver`)

High, beyond the "Fix first" items:

- Windows RFCOMM detects connect failure only through `writefds`, but Winsock reports a failed non-blocking connect in `exceptfds`. A refused connect hangs until the deadline.
- Windows RFCOMM reads `WSAGetLastError` in a separate call, so the goroutine can move to another OS thread and get a stale value.
- ✓ On Linux, L2CAP treats a zero-byte read (the peer closed) as an empty packet, so the AAP driver spins. RFCOMM handles this case correctly.
- macOS RFCOMM probably always fails its first SDP lookup, because `performSDPQuery:` is asynchronous. Delegates may dangle after close, and nothing pumps the run loop for data callbacks. (hw)
- The kernel driver leaks the L2CAP channel on remote disconnect and clears it without taking the lock. (hw)

Medium:

- Sub-millisecond deadlines round down to 0. A socket that is still connecting can then be registered as open.
- Windows uses `SO_RCVTIMEO` and retries, but the socket is in an indeterminate state after such a timeout.
- Windows L2CAP reads ignore the context and hold the channel lock for up to 5s.
- The Go side sends `CONNECT` to every kernel-driver instance, so it can connect through an instance bound to a different headset.
- On Linux, the fd is read outside the lock; this is a latent reuse race.
- The `.m` files are written as single lines of 400–900 characters.
- `waitFD`, the address parsers and `maxDuration` are copied between packages.
- The macOS L2CAP hardware test opens a PSM that the code says macOS always rejects, so it can never pass.

### Driver system (`internal/driver`, `driver`, the drivers)

Beyond the "Fix first" items:

- Storage read-modify-write has no file lock, so concurrent `bt` processes lose updates. The Apple driver also ignores a failed keyring load and then overwrites every stored key.
- Two concurrent installs can both pass the "already installed" check. (Install now also refuses an ID that a bundled or package-managed driver already provides, so it can't shadow one; the concurrency race remains.)
- `handleHost` is 176 lines of near-identical L2CAP and RFCOMM branches, with permissions checked by hand-sliced string prefixes.
- L2CAP and RFCOMM channels share one map keyed by a bare ID. Cleanup uses `context.Background()`, so it can block forever.
- ✓ Sony `get battery` reports `sonyCapabilities` instead of `sonyCapabilitiesFor`. With controls enabled, `noise_control` reverts to "planned". The bug predates the SDK port, which carried it over.
- The Sony frame stream gives up at the first bad frame, and its ACK handling recurses.
- The Apple driver duplicates its AAP handshake across two functions. It isn't on the SDK yet; the port is done in a separate session.
- The darwin build needs cgo: the stub for `systemAppleAudioKeys` should be tagged `!darwin || !cgo`.
- The storage-key regex is recompiled on every call. The number of storage keys is unlimited. `ImportState` reads the whole file before checking its size.

## Documentation claims

FALSE or misleading claims that users or integrators would act on:

| Doc | Claim | Reality |
| --- | --- | --- |
| README | "never silently chooses between ambiguous matches" | ✓ False for duplicate exact names (finding 3). |
| README | `--verbose` "includes additional diagnostics" | ✓ The flag does nothing. |
| README | GATT writes "choose a mode supported by the characteristic" | ✓ Writes always use with-response unless `--without-response` is passed. |
| README | "Successful mutating commands are silent" | `set` and `driver install` print output. |
| README | Windows connect/disconnect "not reliably available for every profile" | ✓ Always `unsupported`. |
| README | macOS connect/disconnect "Implemented" | It opens a baseband link, not profiles. |
| README | macOS pair "Implemented" | Unpaired Classic devices are never discovered, so they can't be paired. |
| README | `--non-interactive` "fails clearly" | Only Linux honors it; Windows and macOS can still show OS pairing UI. |
| README | host access limited to "selected device IDs" | Drivers can reach every device in the request, which means every known device. |
| README / JSON_API | JSON contract | Neither says that errors go to stderr. `bt --json --quiet` prints a plain-text error, and `bt --json --version` returns a usage error. |
| DRIVER_PROTOCOL | Drivers MUST loop until EOF and answer unknown methods with `unsupported`; Apple Audio is a reference driver | Apple Audio handles one request and sends errors without `error_code`. The SDK port fixes this. |
| DRIVER_PROTOCOL | `bt watch` polls "about every 2 seconds" | **Corrected.** ✓ The real cadence is ≥4s, because each poll rescans, and any error ends the watch. |
| DRIVER_PROTOCOL | The `official/` publisher is "reserved" | **Corrected.** Nothing enforces it. |
| DRIVER_PROTOCOL | Permission table | **Corrected.** Omits `advertisements`, which the Apple manifest declares. Unknown permissions are accepted silently. |
| DRIVER_PROTOCOL | Betty "kills the driver" on timeout | **Fixed.** It surfaced as `internal` (exit 1); it now reports `timeout`. |
| Apple README | A nearby advertisement "is not proof" of identity | With one advertisement and one connected device, its battery is copied onto the saved endpoint anyway. |
| Sony README | Battery "available for v2" | v1 and name-only devices also report battery as `available`, then fail on `get`. |
| packaging/windows | "Signing credentials are described in the root README" | They aren't. |
| packaging/windows | "Tag releases enforce both" MSIs | The Advanced MSI is skipped when its secret is unset. |
| packaging/homebrew | The tap "lives in" `homebrew-tap` | The roadmap says it hasn't been created. |
| COMPATIBILITY | Driver matrix | ✓ The header has 9 columns and the delimiter row 10, so GitHub won't render the table. |
| FUTURE_WORK | Sony "route v2 battery through get" is unchecked | Already done. |

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
