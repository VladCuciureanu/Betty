# Betty Apple Audio driver

Optional AirPods and Beats support for [Betty](../../README.md). The driver is a
separate executable, `bt-driver-apple-audio`; none of its protocol code or model
data is linked into the base `bt` binary.

## Capabilities

| Property | State | Values / shape | Requirements |
| --- | --- | --- | --- |
| `battery` | Available | one percentage, or left/right/case percentages, including charging state when reported | A recognized Continuity advertisement for an estimated reading, or keys/AAP transport for an exact reading |
| `noise_control` | Implemented, validation-gated | `off`, `anc`, `transparency`; `adaptive` only for profiles known to support it | Connected ANC-capable device and AAP transport; Linux only |
| `heart_rate` | Planned | beats per minute | Powerbeats Pro 2 in its fitness-equipment pairing mode; standard GATT path still needs hardware validation |

Noise-control reads and writes are disabled in normal use until the model and
transport have Betty-native hardware evidence. Maintainers can expose the
implementation for same-state validation with:

```console
BETTY_VALIDATE_APPLE_AUDIO_CONTROLS=1 bt get "AirPods Pro" noise_control
BETTY_VALIDATE_APPLE_AUDIO_CONTROLS=1 bt set "AirPods Pro" noise_control anc
```

A write is not considered successful when bytes are merely accepted by the
transport. The driver waits for the device to report the requested mode back.
Generation-free endpoint names receive only the modes common to that family.

## Battery behavior

The driver recognizes known AirPods and Beats model IDs in Apple Continuity
advertisements. Unknown model IDs remain unclaimed.

Three battery paths are supported:

1. An unencrypted Continuity advertisement provides passive 10% buckets. Betty
   marks these values `estimated` with a resolution of 10.
2. A stored advertisement-encryption key allows exact BLE advertisement
   decoding, without keeping an AAP channel open.
3. An authenticated Apple Accessory Protocol session can report exact headset,
   left, right, case, and charging state and provision the private advertisement
   keys.

A nearby BLE advertisement is not proof that it belongs to a saved Classic
endpoint, so the driver reports the relationship as `possible_matches` and
never uses it to authorize a control write.

One heuristic goes further: when exactly one recognized AirPods or Beats
advertisement is nearby and exactly one Apple audio device is connected, the
driver also copies the advertisement's battery reading onto the connected
device. That is usually right, but it is a correlation, not proof: with a
second set nearby that is not advertising, the reading can belong to the wrong
device.

## Platform behavior

| Platform | Passive battery | Exact battery | Noise control |
| --- | --- | --- | --- |
| Linux | Continuity advertisement | Native connection-oriented L2CAP to AAP PSM `0x1001`, or encrypted BLE after key acquisition | AAP implementation available behind validation gate |
| macOS | Continuity advertisement | Encrypted BLE using explicitly authorized `MobileBluetooth` keys from the System keychain | Unavailable: macOS does not expose the reserved AAP PSM to third-party processes |
| Windows | Continuity advertisement | Encrypted BLE after key import | Unsupported: Windows exposes the AAP channel only to kernel-mode drivers |

Linux uses an unprivileged `SOCK_SEQPACKET` L2CAP socket. It does not require
`CAP_NET_RAW`, `sudo`, or a helper service.

Windows desktop applications cannot open this Classic L2CAP channel through
any user-mode API; only a kernel-mode profile driver can. Such a driver must be
Microsoft-signed to load, and none is: Betty's own driver in
`packaging/windows/driver` is unsigned, and so are the third-party ones
(MagicAAP, WinPods). On Windows the driver therefore never opens the AAP
channel. `noise_control` reports `unsupported`, and battery comes from
advertisements, exactly once keys are imported. The kernel driver's future is
tracked in [#8](https://github.com/VladCuciureanu/Betty/issues/8).

## Supported model identification

The current model table covers known generations of AirPods, AirPods Pro,
AirPods Max, Powerbeats, Beats Fit, Beats Solo, Beats Studio, BeatsX, and Beats
Flex. Identification comes from a recognized Continuity model ID when present;
saved endpoint names are lower-confidence observations.

Model recognition does not imply that every capability has been hardware
validated. Release evidence is recorded separately in
[COMPATIBILITY.md](../../COMPATIBILITY.md).

## Driver permissions and state

Manifest ID: `official/apple-audio`

Declared host permissions:

- `device-metadata`
- `advertisements`
- `l2cap`
- `driver-storage`

The keyring is private per-driver state. It can be transferred explicitly:

```console
bt driver export-state official/apple-audio apple-audio-state.json
bt driver import-state official/apple-audio apple-audio-state.json
```

Exports contain device identity and encryption keys, are created with mode
`0600`, and should be handled like credentials.

## Protocol research and provenance

The implementation was written independently in Go. Public projects were used
to understand observable protocol behavior and model metadata; their source is
not linked into or copied into Betty.

- [LibrePods AAP definitions](https://github.com/librepods-org/librepods/blob/main/docs/AAP%20Definitions.md): handshake, notification, battery, private-key, and noise-control packet behavior.
- [LibrePods Windows investigation](https://github.com/librepods-org/librepods/pull/716): Windows profile-driver constraints and experiments.
- [MagicPodsCore](https://github.com/steam3d/MagicPodsCore): model identifiers, private-key provisioning, and encrypted advertisement behavior.
- [BeatsBar](https://github.com/mahdi-salmanzade/BeatsBar): Powerbeats Pro 2 transport and heart-rate observations.
- [airpods-ctl](https://github.com/BorgoGustavo/airpods-ctl): comparison point for macOS-facing AirPods control experiments.

These sources are reverse-engineering references, not proof of Betty hardware
support. Only results entered in the compatibility evidence table count as a
support claim.

## Build and validate

```console
go build -o bt-driver-apple-audio .
./bt-driver-apple-audio --manifest
```

With Betty and explicitly selected disposable hardware:

```console
PATH="$PWD:$PATH" BETTY_BINARY=/path/to/bt \
BETTY_HARDWARE_APPLE_AUDIO='device id' \
BETTY_VALIDATE_APPLE_AUDIO_CONTROLS=1 \
go test -tags=hardware -run TestAppleAudioControlReadWriteReadback -v ../../hardware
```
