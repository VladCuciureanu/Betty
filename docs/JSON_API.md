# Betty JSON API

Betty's JSON output is a process API for any local client. It is not tailored
to a particular UI. Every response uses a versioned envelope:

```json
{"schema_version":1,"data":{}}
```

Failures use the same version and contain a stable `code`, a human-readable
`message`, `retryable`, and optional `details`. Clients should branch on the
code, display the message when useful, and ignore unknown fields.

## Standard battery property

`battery.value` is either an integer percentage for a single battery:

```json
{"value":90,"unit":"%"}
```

or an object for devices with independently reported components:

```json
{
  "value": {
    "left": {"level":89,"status":"discharging"},
    "right":{"level":88,"charging":true,"status":"charging"},
    "case":{"level":42}
  },
  "unit":"%"
}
```

The allowed component keys in schema version 1 are `headset`, `left`, `right`,
and `case`. Each component has an integer `level` from 0 through 100 and may
include `charging` and a protocol-derived `status`.

When `estimated` is true, the reading is approximate. `resolution` gives its
step size in percentage points. Clients own presentation, including whether to
render an approximation marker. `display` is only a terminal-friendly hint and
is not the canonical value.

Unavailable components are omitted. Clients must not interpret omission as
zero percent.
