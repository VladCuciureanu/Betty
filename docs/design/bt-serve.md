# Design: `bt serve`

Status: proposal for review ([#26](https://github.com/VladCuciureanu/Betty/issues/26)).

## Problem

Apps that use Betty run `bt` once per operation. That costs a process start,
a device lookup, and driver processes every time; polling `bt get` or keeping
`bt watch` processes per property is clumsy; and nothing tells an app that a
device appeared, connected, or changed unless it asks again.

## Proposal

`bt serve` is a foreground process that an app starts and owns. It speaks
newline-delimited JSON on stdin and stdout, like the driver protocol, and ends
when its stdin closes. It is not a daemon: there is no socket, no system
service, and no state that outlives the process, which keeps the "no
background process" rule.

### Framing

Each line is one JSON object. Every message has `"schema_version": 1` and a
`type`:

| `type` | Direction | Meaning |
| --- | --- | --- |
| `request` | app → Betty | Do something. |
| `response` | Betty → app | The final answer to one request. |
| `event` | Betty → app | Something changed, for a subscription. |

Requests carry an app-chosen `id`; Betty echoes it on the response and on
every event for that request. Unlike the driver protocol, a session is
concurrent: an app may send a request before earlier ones are answered, and
responses arrive in completion order.

### Requests

Methods mirror the CLI, so the JSON API stays one vocabulary:

| Method | Parameters | Result |
| --- | --- | --- |
| `list`, `scan` | `duration_ms` for scan | array of devices, as `bt --json list` |
| `inspect` | `device` | device |
| `get` | `device`, `property` | property result, as `bt --json get` |
| `set` | `device`, `property`, `value` | property result with `verified` |
| `pair`, `unpair`, `connect`, `disconnect` | `device` | mutation result |
| `gatt.services`, `gatt.characteristics`, `gatt.read`, `gatt.write`, `gatt.descriptors` | as the CLI | as the CLI |
| `adapter`, `adapter.set` | `power`, `discoverable`, `name` | adapter state |
| `subscribe` | `device`, `property`, or `characteristic` | a `subscription` ID; events follow |
| `cancel` | `request` (an ID) | ends that request or subscription |

`device` is a query as on the command line (ID, address or name), so
ambiguity is reported the same way.

```json
{"schema_version":1,"type":"request","id":"7","method":"get","params":{"device":"headphones","property":"battery"}}
{"schema_version":1,"type":"response","id":"7","data":{"device_id":"bluez:AA:BB:CC:DD:EE:FF","property":"battery","value":{"level":80},"unit":"%","estimated":false}}
```

### Errors

A failed request gets a response with `error`, shaped exactly like the CLI's
error envelope (`code`, `message`, `retryable`, `details`). Errors never end
the session; only a malformed line does, with a final `error` response whose
`id` is empty.

### Subscriptions

`subscribe` replaces `bt watch` and `bt gatt watch` for apps:

- **Properties:** events carry the property result with `event` set to
  `snapshot` first, then `changed`. Drivers that implement `watch` push
  changes; otherwise Betty polls `get` at `interval_ms` (default 2000).
- **Characteristics:** events carry each notification, as `bt gatt watch`.
- A subscription ends with a response: `device_disconnected` when the link
  drops, or success after `cancel`.

A later version could add device-level events (appeared, connected,
disconnected, paired) from the OS backends' own notifications. They are out
of scope here, because only BlueZ exposes them cheaply today.

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

## Implementation sketch

- `internal/serve`: the session loop, a request table keyed by ID, and one
  goroutine per request with its own context, cancelled by `cancel` or EOF.
- Command handlers are refactored to return results instead of rendering
  them, so the CLI and the session share one implementation per method.
- Output goes through one writer with a mutex, so concurrent responses never
  interleave within a line.
- A test harness drives a session over pipes against the fake backend and
  BLE client the app tests already use.

## Open questions

1. Should requests be concurrent, or strictly sequential like the driver
   protocol? Concurrent is more useful for UIs, sequential is simpler for
   clients and for Betty.
2. Should `subscribe` deduplicate unchanged values, as `bt watch` does, or
   report every reading?
3. Is a device-event stream (appeared, connected, removed) needed for the
   first version?
