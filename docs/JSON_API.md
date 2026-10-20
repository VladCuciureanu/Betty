# Betty JSON API

> [!WARNING]
> Betty is pre-1.0. The JSON API may change incompatibly in any release before
> 1.0, and `schema_version` may stay at `1` across those changes. Schema version
> 1 freezes at 1.0.

With `--json`, Betty is a process API for any local client. Every line it prints
is one JSON envelope:

```json
{"schema_version":1,"data":{}}
```

- **stdout** carries results: one envelope per command, or one per event for
  streaming commands such as `watch`.
- **stderr** carries everything else, also as envelopes: at most one `error`,
  any number of `warning`s, and `debug` diagnostics with `--verbose`.
- The exit status says whether the command succeeded; see the
  [README](../README.md#built-for-automation).

Clients should ignore fields they don't recognize.

## Errors

```json
{"schema_version":1,"error":{"code":"device_ambiguous","message":"2 devices match \"keyboard\"; use a device ID or address","retryable":false,"details":{"query":"keyboard","matches":[{"id":"windows:AA:BB:CC:DD:EE:01","name":"Office Keyboard","paired":true,"connected":false},{"id":"windows:AA:BB:CC:DD:EE:02","name":"Travel Keyboard","paired":true,"connected":true}]}}}
```

| Field | Meaning |
| --- | --- |
| `code` | Stable error code; branch on it. |
| `message` | Human-readable explanation; show it, but don't parse it. |
| `retryable` | Whether trying again might succeed. True for `bluetooth_unavailable`, `device_disconnected`, and `timeout`. |
| `details` | Optional structured context. For `device_ambiguous` it lists the matching devices. |

Codes: `usage`, `device_not_found`, `device_ambiguous`, `unsupported`,
`driver_required`, `permission_denied`, `bluetooth_unavailable`,
`device_disconnected`, `authentication_failed`, `interaction_required`,
`timeout`, `canceled`, and `internal`.

## Warnings and diagnostics

```json
{"schema_version":1,"warning":{"message":"BLE scan failed: Bluetooth permission denied"}}
{"schema_version":1,"debug":{"message":"\"Desk Speaker\" is not a known device; scanning"}}
```

A warning reports a problem that did not stop the command. The command's result
is still valid but may be incomplete; for example, a scan whose BLE half failed
lists only the devices the OS found.

## Results

### Devices: `scan`, `list`, `inspect`

`scan` and `list` return an array of devices, which is empty rather than `null`
when nothing is found. `inspect` returns one device.

```json
{
  "id": "windows:04:E4:B6:1F:F5:8D",
  "name": "WH-1000XM5",
  "paired": false,
  "connected": false,
  "rssi": -70,
  "endpoints": [
    {"id": "04:E4:B6:1F:F5:8D", "transport": "os", "address": "04:E4:B6:1F:F5:8D"},
    {"id": "04:E4:B6:1F:F5:8D", "transport": "ble", "address": "04:E4:B6:1F:F5:8D"}
  ],
  "identification": {"vendor": "Sony", "family": "Headphones", "model": "WH-1000XM5", "confidence": "protocol"},
  "properties": {"battery": {"value": {"level": 80}, "unit": "%", "display": "80%"}},
  "capabilities": {"noise_control": {"status": "planned", "type": "enum", "readable": true, "writable": true, "values": ["off", "anc", "ambient"], "reason": "..."}}
}
```

- `id` is opaque and platform-local. Don't parse it.
- Each endpoint is one way of reaching the device: `os` (the operating
  system's device record), `classic`, or `ble`. Entries that share a Bluetooth
  address are merged into one device; names are never used to merge.
- `identification`, `properties`, and `capabilities` come from drivers and
  from standard sources. A capability whose `status` is not `available` must
  not be offered to users.
- Optional fields, such as `rssi`, `services`, `manufacturer_data`,
  `service_data`, `information`, `possible_matches`, and `last_seen_at`, are
  omitted when unknown.

### Properties: `get`, `set`, `watch`

```json
{"device_id":"windows:AA:BB:CC:DD:EE:FF","property":"battery","value":{"level":80},"unit":"%","estimated":false}
```

| Field | Meaning |
| --- | --- |
| `value` | The canonical value, in the property's type. |
| `unit` | Present when the value has a unit, such as `%`. |
| `estimated` | Whether the value is approximate. |
| `resolution` | For an estimate, its step size in the value's unit. |
| `event` | `watch` only: `snapshot` for the first value, `changed` for each later one. |

`set` returns the value read back from the device after writing, not the value
requested.

`get --all <property>` returns an array with one entry per known device: the
fields above plus `name`, or, for a device whose property couldn't be read,
`device_id`, `name`, `property`, and an `error` object shaped like the
[error envelope](#errors)'s. The command itself succeeds unless listing the
devices fails.

#### Standard battery value

`battery.value` is always an object. A device with one battery reports its
`level` directly:

```json
{"level":80,"charging":true}
```

A device with independently reported parts reports them as `left`, `right`,
and `case` components instead:

```json
{"left":{"level":89,"status":"discharging"},"right":{"level":88,"charging":true,"status":"charging"},"case":{"level":42}}
```

Each `level` is an integer from 0 through 100, with optional `charging` and a
protocol-specific `status` beside it. Unavailable parts are omitted; never
treat an omitted part as 0%.

### Pairing and connections: `pair`, `unpair`, `connect`, `disconnect`

```json
{"device_id":"windows:AA:BB:CC:DD:EE:FF","name":"Desk Speaker","action":"connect","changed":true,"paired":true,"connected":true}
```

`paired` and `connected` are the state after the command. `changed` is `false`
when the device was already in the requested state and nothing was done.

### GATT: `gatt`

- `services` and `characteristics` return arrays.
- `read` returns
  `{"device_id":"...","characteristic":"2a19","value":"59"}`, where `value` is
  hexadecimal.
- `write` returns
  `{"device_id":"...","characteristic":"...","bytes":1,"with_response":true}`.
- `watch` prints one envelope per notification, with `"event":"notification"`
  and the fields of `read`.

### Drivers: `driver`

- `list` returns an array of driver manifests.
- `install` returns the installed driver's manifest.
- `remove`, `export-state`, and `import-state` return
  `{"id":"...","action":"remove"}`, plus `file` for the state operations.
- `check` returns an array of `{"name":"...","passed":true}`, with `detail`
  on a failed check. When any check fails, it then fails with `unsupported`.

### Other

- `capabilities` returns the platform's supported operations, each
  `supported`, `unsupported`, or `permission-required`.
- `version` returns `{"version":"..."}`.
- `help` returns `{"help":"..."}`.
