# Design: `bt serve`

Status: implemented ([#26](https://github.com/VladCuciureanu/Betty/issues/26)).

## Problem

Apps that use Betty run `bt` once per operation. That costs a process start,
a device lookup, and driver processes every time; polling `bt get` or keeping
`bt watch` processes per property is clumsy; and nothing tells an app that a
device connected, disconnected or changed unless it asks again.

## Proposal

`bt serve` is a foreground process that an app starts and owns. It speaks
newline-delimited JSON on stdin and stdout, like the driver protocol, and ends
when its stdin closes. It is not a daemon: there is no socket, no system
service, and no state that outlives the process (see
[Why not a daemon](#why-not-a-daemon)).

### Framing

Each line is one JSON object. Every message has `"schema_version": 1` and a
`type`:

| `type` | Direction | Meaning |
| --- | --- | --- |
| `request` | app → Betty | Do something. |
| `response` | Betty → app | The final answer to one request. |
| `event` | Betty → app | Something changed, for a subscription. |

Requests carry an app-chosen `id`; Betty echoes it on the response and on
every event for that request, and refuses an `id` that is still in use.

### Concurrency

Requests run concurrently: an app may send a request before earlier ones are
answered, and responses arrive in completion order, so one slow scan or
pairing never stalls the rest. Bluetooth stacks handle parallel operations on
one device badly, so Betty queues work per device: operations on one device
run one at a time, in arrival order, while different devices proceed in
parallel. Scans run one at a time across the session. An app that prefers
sequential behavior simply waits for each response.

### Requests

Methods mirror the CLI, and each result is the `data` that `bt --json` prints
for the same command, so the JSON API stays one vocabulary:

| Method | Parameters | Result |
| --- | --- | --- |
| `list`, `scan`, `capabilities` | none | as the CLI |
| `inspect` | `device` | device |
| `get` | `device` and `property`, or `all: true` and `property` | property result, or one entry per device |
| `set` | `device`, `property`, `value` (any JSON value) | property result with `verified` |
| `pair`, `unpair`, `connect`, `disconnect` | `device` | mutation result |
| `adapter` | none | adapters |
| `adapter.set` | `setting` (`power`, `discoverable` or `name`), `value` (`true`/`false`, `"on"`/`"off"`, or a name) | adapter state |
| `gatt.services`, `gatt.mtu` | `device` | as the CLI |
| `gatt.characteristics` | `device`, `service` (optional) | as the CLI |
| `gatt.read`, `gatt.descriptors` | `device`, `characteristic`, `service` (optional) | as the CLI |
| `gatt.write` | `device`, `characteristic`, `data` (hex), `service` and `without_response` (optional) | as the CLI |
| `gatt.read_descriptor`, `gatt.write_descriptor` | `device`, `characteristic`, `descriptor`, and `data` (hex) to write | as the CLI |
| `subscribe` | one of `device` + `property`, `device` + `characteristic`, or `devices: true`; `interval_ms` (optional) | events, then a response when it ends |
| `cancel` | `request` (an ID) | `cancelled: true`; the cancelled request then responds with `canceled` |

Every request may set `timeout_ms`; the default is `--timeout`, 10 seconds.
`device` is a query as on the command line (ID, address or name), so
ambiguity is reported the same way.

```json
{"schema_version":1,"type":"request","id":"7","method":"get","params":{"device":"headphones","property":"battery"}}
{"schema_version":1,"type":"response","id":"7","data":{"device_id":"bluez:AA:BB:CC:DD:EE:FF","property":"battery","value":{"level":80},"unit":"%","estimated":false}}
```

### Errors and warnings

A failed request gets a response with `error`, shaped exactly like the CLI's
error envelope (`code`, `message`, `retryable`, `details`). A capability that
needs a connection is refused for a disconnected device with
`device_disconnected`, as on the command line. Errors never end the session;
only a malformed line does, with a final `error` response whose `id` is empty.

What the CLI prints as warnings on stderr, such as a driver's note that it
switched a controller's reports, goes in the response's `warnings` array.

### Subscriptions

`subscribe` replaces `bt watch` and `bt gatt watch` for apps, and adds device
events. Its events carry the subscribe request's `id`, and `cancel` with that
ID ends it. A warning while it runs, such as a failed poll it will retry,
arrives as an event whose data is `{"warning": "..."}`.

- **Properties** (`device` + `property`): events carry the property result
  with `event` set to `snapshot` first, then `changed`. Unchanged values are
  skipped. Drivers that implement `watch` push changes; otherwise Betty polls
  `get` at `interval_ms` (default 2000). While the device is disconnected and
  the capability needs a connection, Betty waits and resumes on reconnect.
- **Characteristics** (`device` + `characteristic`): every notification is an
  event, as in `bt gatt watch`, never deduplicated: each is a reading.
- **Devices** (`devices`: `true`): events describe changes to the devices the
  OS knows, found by polling the known-device list every `interval_ms`. The
  first is `{"event":"snapshot","devices":[...]}`; then each change is
  `{"event":...,"device":{...}}` with `added`, `removed`, `paired`,
  `unpaired`, `connected`, `disconnected` or `renamed`. This needs no
  scanning, so it costs no radio time. Nearby unpaired devices appearing are
  out of scope; an app scans for those explicitly.
- A property or characteristic subscription ends with a response:
  `device_disconnected` when the link drops for a characteristic, or success
  after `cancel`.

### Lifetime and resources

- Driver processes are started once and reused for the whole session, which
  driver reuse already does within one command.
- BLE connections are opened per operation, as now. Keeping links open
  between requests is a possible later optimisation, but it changes device
  behaviour (some devices stay awake), so it would be opt-in.
- `--timeout` becomes the default per-request deadline; a request may set
  `timeout_ms`.
- On stdin EOF Betty cancels everything, closes channels and connections,
  stops drivers, and exits 0.

### Interaction

`bt serve` always runs non-interactively: pairing that needs a person fails
with `interaction_required`, as `--non-interactive` does. A later version
could forward pairing prompts as events that the app answers with a request.

## Why not a daemon

A system service or daemon would let several apps share state and make the
CLI start instantly, but nothing in Betty needs that yet, and it would cost:

- **Permissions.** macOS grants Bluetooth to the app that runs Betty; a
  launchd daemon needs its own grant and prompts. A Windows service runs as
  SYSTEM in session 0, where pairing and per-user Bluetooth state behave
  differently. The Apple Audio driver's keys come from the user's keychain.
  `bt serve` runs as the user with exactly the CLI's permissions.
- **Operations.** Service installation on three platforms, autostart,
  upgrades, and keeping the CLI and daemon versions in step.
- **Security.** A socket or named pipe needs access control, or any local
  process could pair and unpair devices. Only the app that started
  `bt serve` can talk to it.
- **Arbitration.** Several clients competing for one radio is a design
  problem of its own; with `bt serve` each app owns its session.

The session protocol is independent of its transport, so a later
`bt serve --listen` on a user-level socket could serve several clients with
the same messages. That would extend this design, not replace it.

## Implementation

- `internal/app/serve.go` holds the session loop, a request table keyed by
  ID, one goroutine per request with its own context, cancelled by `cancel`
  or EOF, and per-device queues keyed by the device query.
- Each request runs the CLI command it mirrors on a copy of the application
  whose renderer captures the result and warnings, so the CLI and the session
  share one implementation per method. Driver warnings reach the right
  request through its context.
- A reused driver process serves one request at a time; a concurrent request
  starts another, which stops when it is no longer needed.
- Scans take a session-wide slot, held until the platform scan really
  finishes, since a Windows inquiry overruns its deadline.
- Output goes through one writer with a mutex, so concurrent responses never
  interleave within a line.
- `serve_test.go` drives sessions over pipes against the fake backend and BLE
  client the app tests use.

## Decisions

1. **Requests are concurrent,** with work queued per device and scans
   serialized, because sequential requests would let one slow operation stall
   a UI.
2. **Property events skip unchanged values; characteristic notifications are
   never skipped,** because a repeated notification is a new reading.
3. **The first version includes device events** for OS-known devices, by
   polling, because connection state is what UIs show most.
4. **`bt serve` comes before any daemon,** for the reasons above.
