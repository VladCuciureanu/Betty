# Betty driver protocol, version 1

A Betty driver is an executable that teaches Betty about one family of devices.
It identifies the devices it recognizes, describes their capabilities, and reads
and writes their properties. Betty starts it as a child process and talks to it
over standard input and output, so you can write a driver in any language that
can read and write JSON.

> [!WARNING]
> Betty is pre-1.0. This protocol may change incompatibly in any release before
> 1.0, without a version bump or a migration path. Expect to rebuild drivers
> against each release until then.

This document is the normative specification. The key words MUST, MUST NOT,
SHOULD, and MAY are used as in RFC 2119. Go drivers can use the SDK in
[`driver`](../driver) (`driver.Serve`), which implements everything below.

## Contents

1. [Overview](#1-overview)
2. [The executable](#2-the-executable)
3. [Manifest](#3-manifest)
4. [Framing and session lifecycle](#4-framing-and-session-lifecycle)
5. [Requests from Betty](#5-requests-from-betty)
6. [Responses to Betty](#6-responses-to-betty)
7. [Data types](#7-data-types)
8. [Host calls](#8-host-calls)
9. [Errors](#9-errors)
10. [How Betty uses driver results](#10-how-betty-uses-driver-results)
11. [Installation and discovery](#11-installation-and-discovery)
12. [Security model](#12-security-model)
13. [Stability](#13-stability)
14. [Example transcript](#14-example-transcript)
15. [Minimal driver in Python](#15-minimal-driver-in-python)

## 1. Overview

```text
   bt get headphones battery
            │
            ▼
   ┌──────────────────┐   request (JSON line)     ┌────────────────┐
   │       Betty      │ ────────────────────────▶ │     driver     │
   │  (host process)  │ ◀──────────────────────── │ (child process)│
   │                  │   host_request            │                │
   │ owns Bluetooth,  │ ────────────────────────▶ │ owns the device│
   │ permissions,     │   host_response           │ protocol       │
   │ storage          │ ◀──────────────────────── │                │
   └──────────────────┘   response                └────────────────┘
```

Betty handles discovery, device resolution, the operating-system Bluetooth
APIs, and the public CLI. A driver handles the device-specific protocol. A
driver never opens Bluetooth sockets itself: it asks Betty to do so through
host calls, and Betty checks each call against the driver's manifest.

The public CLI never grows vendor-specific commands. A driver only adds
properties and capabilities to Betty's generic `inspect`, `get`, `set`, and
`watch` commands.

## 2. The executable

A driver is a single executable file.

- Invoked with exactly one argument, `--manifest`, it MUST print its manifest
  ([section 3](#3-manifest)) as one JSON object on stdout and exit with status 0.
  It MUST NOT perform Bluetooth I/O in this mode, and it SHOULD answer
  immediately: Betty may allow as little as 2 seconds.
- Invoked with no arguments, it MUST speak the session protocol
  ([section 4](#4-framing-and-session-lifecycle)).
- Stdout is reserved for protocol messages. A driver MUST NOT write anything
  else to stdout.
- A driver MAY write diagnostics to stderr. Betty discards them unless `bt` runs with `--verbose`, which relays each line as a diagnostic prefixed with the driver ID, up to 64 KiB per driver process.
- Betty sets no particular working directory or environment. A driver MUST NOT
  rely on one.

Official drivers are named `bt-driver-<name>` (`.exe` on Windows). Third-party
drivers can use any file name; Betty identifies them by manifest ID.

## 3. Manifest

```json
{
  "protocol": 1,
  "id": "example/heart-rate",
  "name": "Example Heart Rate",
  "version": "0.3.0",
  "permissions": ["device-metadata", "driver-storage"],
  "match": {"service_uuids": ["180d"]}
}
```

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `protocol` | integer | yes | Protocol version. MUST be `1`. |
| `id` | string | yes | Stable driver ID, `publisher/name`. Each part starts with `[a-z0-9]` and continues with `[a-z0-9._-]`. Betty refuses an install whose ID does not match. |
| `name` | string | yes | Human-readable name. |
| `version` | string | yes | Driver version. Semantic versioning is RECOMMENDED. |
| `permissions` | array of strings | no | Host capabilities the driver may use ([section 8](#8-host-calls)). |
| `match` | object | no | Hints about the devices the driver recognizes, described below. |

The output of `--manifest` MUST NOT exceed 64 KiB.

`match` lets Betty skip starting a driver that can't recognize any device in a
request. A device matches when any one hint does:

| Hint | Type | Matches a device with |
| --- | --- | --- |
| `company_ids` | array of integers | manufacturer data from one of these Bluetooth SIG company IDs |
| `service_uuids` | array of strings | one of these UUIDs among its services or service data, as 16-bit (`"180f"`) or 128-bit UUIDs |
| `name_contains` | array of strings | a name containing one of these, ignoring case |

Betty sends `inspect` only when some device in the request matches, and `get`
or `set` only when the selected device or one of its possible matches does.
The request still carries every device, so a driver can correlate endpoints.
A driver without `match` receives every request. Hints are a filter, not
identification: a driver MUST still check each device itself.

A driver's stored state belongs to its ID. Renaming a driver starts it with
empty state; users can carry state over with `bt driver export-state` and
`bt driver import-state`.

The `official/` publisher is used by drivers shipped by the Betty project. Betty
does not verify publishers, but it refuses to install a driver whose ID is
already provided by any installed driver ([section 11](#11-installation-and-discovery)).

Permissions:

| Permission | Grants |
| --- | --- |
| `device-metadata` | Nothing extra. Every driver receives device metadata; declaring it documents that the driver reads it. |
| `l2cap` | `l2cap.*` host calls. |
| `rfcomm` | `rfcomm.*` host calls. |
| `gatt` | `gatt.*` host calls. |
| `driver-storage` | `storage.*` host calls. |

Betty accepts other permission names without granting anything for them. The
official Apple Audio driver declares `advertisements` this way to document that
it reads advertisement data.

## 4. Framing and session lifecycle

Every message in either direction is one JSON object encoded as UTF-8 and
followed by a newline (`\n`). A message MUST NOT contain a raw newline; JSON
string escaping guarantees this. Readers SHOULD skip blank lines between
messages. A message from a driver MUST NOT exceed 4 MiB; Betty ends the session
with an `internal` error when one does.

All messages carry `"protocol": 1` and a `type`:

| `type` | Direction | Purpose |
| --- | --- | --- |
| `request` | Betty → driver | Ask the driver to do something. |
| `response` | driver → Betty | Final answer to a `request`. |
| `host_request` | driver → Betty | Ask Betty to do Bluetooth or storage I/O. |
| `host_response` | Betty → driver | Answer to a `host_request`. |

A session is strictly sequential:

1. Betty starts the driver and writes one `request`.
2. While handling it, the driver MAY send any number of `host_request`
   messages. After each one, it MUST wait for the matching `host_response`
   before sending anything else.
3. The driver ends the request with exactly one message of type `response`
   whose `id` equals the request's `id`. Any other message type, or a response
   with a different `id`, ends the session with an `internal` error.
4. Betty either sends another `request` or closes the driver's stdin.
5. When stdin reaches end-of-file, the driver MUST exit promptly with status 0.
   Betty waits 2 seconds, then kills the driver; the response it already sent
   still counts. A non-zero exit status after a response is reported as an
   `internal` error.

Betty currently sends one request per process and starts a new process for the
next one. Drivers MUST still be written as a loop that handles requests until
end-of-file, because Betty may reuse a process for several requests in a later
release without changing the protocol version.

Deadlines:

- Each driver gets at most 5 seconds for an `inspect`. Betty kills a driver
  that misses it, reports nothing from that driver, and moves on to the next
  one. Every scan and list runs every installed driver, so inspect SHOULD
  return in well under a second.
- A `get` or `set` is bounded only by the command's `--timeout` (10 seconds by
  default), which the user can raise for slow devices.
- `--manifest` gets 5 seconds.

When a driver misses its deadline, Betty kills it and reports `timeout`.

## 5. Requests from Betty

```json
{"protocol":1,"type":"request","id":"1","method":"get","device_id":"opaque-id","property":"battery","devices":[...]}
```

| Field | Type | Present for | Meaning |
| --- | --- | --- | --- |
| `id` | string | all | Request ID to echo in the response. Treat as opaque. |
| `method` | string | all | `inspect`, `get`, or `set`. |
| `purpose` | string | `inspect` | `list` when `devices` holds only the devices the OS already knows, or `scan` when it also holds devices found by an active scan. Drivers MUST treat unknown values as `scan`. |
| `device_id` | string | `get`, `set` | ID of the target device. |
| `property` | string | `get`, `set` | Property name. |
| `value` | any JSON | `set` | Value to write. |
| `devices` | array of [Device](#device) | all | Every device Betty currently knows. For `get` and `set` it includes the target. |

### `inspect`

Identify the devices you recognize. Respond with an enrichment for each one,
keyed by device ID, and omit the rest. Inspect SHOULD NOT open connections or
change device state: it runs for every `bt scan`, `bt list`, and device lookup.

### `get`

Read `property` from `device_id`. If you don't handle this device or property,
respond with no enrichments, not an error, so another driver can try.
Otherwise respond with an enrichment for `device_id` whose `properties`
contains `property`.

### `set`

Write `value` to `property` on `device_id`, then read the state back from the
device. Respond with an enrichment for `device_id` whose `properties` contains
the verified value. If you cannot verify the write, respond with an error. Do
not echo the requested value. Leave sets you don't handle unanswered by
responding with no enrichments.

Betty validates `value` against the capability you reported before it sends a
`set`: the capability must be `available` and `writable`, and the value must be
in `values` or within `minimum`, `maximum`, and `step`. Values for `integer`
and `number` capabilities arrive as JSON numbers; all other values arrive as
JSON strings.

## 6. Responses to Betty

Success:

```json
{"protocol":1,"type":"response","id":"1","enrichments":{"opaque-id":{"properties":{"battery":{"value":{"level":80},"unit":"%"}}}}}
```

Failure:

```json
{"protocol":1,"type":"response","id":"1","error":"Sony battery requires a connected device","error_code":"device_disconnected","details":{"device_id":"opaque-id"}}
```

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | string | The request's `id`. |
| `enrichments` | object | Map from device ID to [Enrichment](#enrichment). Omit it or leave it empty when nothing applies. |
| `error` | string | Non-empty on failure. A response with an `error` is a failure regardless of other fields. |
| `error_code` | string | One of the [error codes](#9-errors). Unknown codes are treated as `internal`. |
| `details` | any JSON | Optional structured context for the error. |

## 7. Data types

Unknown fields MUST be ignored by both sides. Optional fields may be omitted.

[`docs/schema/driver-protocol.schema.json`](schema/driver-protocol.schema.json)
is a JSON Schema (draft 2020-12) of every message and value below, for
validating a driver's output or generating types in other languages. It is
generated from the Go SDK's types, and a test keeps the two in step. It allows
unknown fields, as this section requires.

### Device

What Betty tells a driver about a device.

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | string | Opaque, platform-local stable ID. It is not a Bluetooth address. Use it in host calls and enrichment keys. |
| `name` | string | Display name. Names are not unique and are not proof of identity. |
| `paired` | boolean | Paired with this computer. |
| `connected` | boolean | Connected through the operating system. |
| `rssi` | integer | Signal strength in dBm, when recently seen. |
| `services` | array of strings | Advertised or SDP service UUIDs. Letter case and hyphenation vary; normalize before comparing. |
| `manufacturer_data` | array of `{company_id, data}` | Advertisement manufacturer data. `company_id` is the Bluetooth SIG company identifier as an integer; `data` is lowercase hex. |
| `service_data` | array of `{uuid, data}` | Advertisement service data; `data` is lowercase hex. |
| `endpoints` | array of `{id, transport, address}` | Observations of this device. `transport` is `os`, `ble`, or `classic`. |
| `possible_matches` | array of strings | IDs of other devices that might be the same physical device. |

### Enrichment

What a driver tells Betty about a device. Every field is optional.

| Field | Type | Meaning |
| --- | --- | --- |
| `identification` | [Identification](#identification) | What the device is. |
| `properties` | object of [Property](#property) | Current values, keyed by property name. |
| `capabilities` | object of [Capability](#capability) | What can be read or written, keyed by property name. |
| `possible_matches` | array of strings | Replaces the device's possible matches. |

### Identification

| Field | Type | Meaning |
| --- | --- | --- |
| `vendor` | string | For example `Sony`. |
| `family` | string | For example `Headphones`. |
| `model` | string | For example `WH-1000XM5`. |
| `confidence` | string | Evidence strength: `name` (name pattern only), `model` (model identifier from the device), or `protocol` (protocol-specific service or authenticated session). |

### Property

| Field | Type | Meaning |
| --- | --- | --- |
| `value` | any JSON | Canonical value. Required. |
| `unit` | string | For example `%`. |
| `display` | string | Short human rendering for terminals. Clients must not parse it. |
| `estimated` | boolean | The value is approximate. |
| `resolution` | integer | Step size of an estimated value, in the value's unit. |

### Capability

| Field | Type | Meaning |
| --- | --- | --- |
| `status` | string | `available`, `planned`, or `unsupported`. Only `available` may be read or written. |
| `type` | string | `battery`, `enum`, `boolean`, `integer`, `number`, or `string`. |
| `readable` | boolean | Supports `get`. |
| `writable` | boolean | Supports `set`. |
| `values` | array of strings | Accepted values for `enum` and `boolean`. |
| `minimum`, `maximum`, `step` | number | Accepted range for `integer` and `number`. |
| `reason` | string | Why the capability is not `available`. |

### Property names

Use a shared name whenever the concept is shared, so that clients work across
vendors:

| Name | Type | Value |
| --- | --- | --- |
| `battery` | `battery` | An object. A device with one battery sets `level` (0–100), with optional `charging` (boolean) and `status` (string). A device with independently reported parts sets `left`, `right`, and `case` components instead, each `{"level": 0-100, "charging": bool, "status": string}`. Omit unavailable parts; never report them as 0. Betty turns a bare integer percentage into `{"level": n}`. |
| `noise_control` | `enum` | `off`, `anc`, `ambient`, `transparency`, `adaptive`, as supported by the model. |
| `ambient_level` | `integer` | Model-specific range. |
| `wear_detection` | `boolean` | `off`, `on`. |
| `microphone_mute` | `boolean` | `off`, `on`. |
| `codec` | `string` | Active audio codec. |
| `firmware_version` | `string` | Firmware version. |

Name genuinely vendor-specific properties `<vendor>.<name>`, for example
`sony.speak_to_chat`. New shared names are added to this table through the
Betty project rather than invented by individual drivers.

Report a capability as `planned` until it is implemented and validated on real
hardware. Never return an estimate when an exact value was requested and is
unavailable; return an error instead.

## 8. Host calls

```json
{"protocol":1,"type":"host_request","id":"host-1","method":"rfcomm.open","params":{"device_id":"opaque-id","service_uuid":"956c7b26-d49a-4ba8-b03f-b17d393cb6e2"}}
{"protocol":1,"type":"host_response","id":"host-1","result":{"channel_id":"rfcomm-1"}}
```

A `host_request` has a driver-chosen `id`, unique within the session, a
`method`, and `params`. Betty answers with a `host_response` carrying the same
`id` and either a `result` or `error`, `error_code`, and `details` with the same
meaning as in a [response](#6-responses-to-betty).

Betty enforces these rules, returning `permission_denied` when they are broken:

- the method's permission must be declared in the manifest;
- `device_id` must be one of the devices in the current request;
- `channel_id` must have been opened by this driver in the current request, on
  the same transport;
- `subscription_id` must have been returned to this driver in the current
  request.

Betty closes every channel, subscription and GATT connection still open when
the request ends.

| Method | Permission | `params` | `result` |
| --- | --- | --- | --- |
| `l2cap.open` | `l2cap` | `device_id`, `psm` (integer) | `channel_id` |
| `l2cap.read` | `l2cap` | `channel_id`, `max_bytes` (1–65535), `timeout_ms` (optional, at most 30000) | `data` (lowercase hex) |
| `l2cap.write` | `l2cap` | `channel_id`, `data` (hex) | `written` (integer) |
| `l2cap.close` | `l2cap` | `channel_id` | `closed` (true) |
| `rfcomm.open` | `rfcomm` | `device_id`, `service_uuid` (SDP service UUID) | `channel_id` |
| `rfcomm.read` | `rfcomm` | `channel_id`, `max_bytes` (1–65535), `timeout_ms` (optional, at most 30000) | `data` (lowercase hex) |
| `rfcomm.write` | `rfcomm` | `channel_id`, `data` (hex) | `written` (integer) |
| `rfcomm.close` | `rfcomm` | `channel_id` | `closed` (true) |
| `storage.get` | `driver-storage` | `key` | `value` (string), `found` (boolean) |
| `storage.set` | `driver-storage` | `key`, `value` (string, at most 64 KiB) | `stored` (true) |
| `gatt.services` | `gatt` | `device_id` | `services`: array of `{uuid, characteristics}`, where `characteristics` is an array of UUIDs |
| `gatt.read` | `gatt` | `device_id`, `characteristic_uuid`, `service_uuid` (optional) | `data` (lowercase hex) |
| `gatt.write` | `gatt` | `device_id`, `characteristic_uuid`, `service_uuid` (optional), `data` (hex), `without_response` (optional boolean) | `written` (true) |
| `gatt.subscribe` | `gatt` | `device_id`, `characteristic_uuid`, `service_uuid` (optional) | `subscription_id` |
| `gatt.next` | `gatt` | `subscription_id`, `timeout_ms` (optional, at most 30000) | `data` (lowercase hex) |
| `gatt.unsubscribe` | `gatt` | `subscription_id` | `unsubscribed` (true) |

Notes:

- A read without `timeout_ms` waits until the session deadline. A read that
  times out fails with `timeout`.
- L2CAP channels are Classic, connection-oriented channels. Availability
  depends on the platform; an unavailable transport fails with `unsupported`.
- Storage is private to the driver ID and written with user-only permissions.
  Keys are 1–128 characters from `[A-Za-z0-9._/-]`. A store holds at most 256
  keys and 1 MiB of JSON; a `storage.set` past either limit fails with `usage`.
  Writes from concurrent `bt` processes are serialized, so none is lost. Users
  can move a store between machines with `bt driver export-state` and
  `bt driver import-state`. Store keys and other secrets only here.

GATT calls:

- `device_id` must be a device with a BLE endpoint; otherwise the call fails
  with `unsupported`. Betty connects on the first GATT call for a device and
  shares that connection across the request's GATT calls, so a read doesn't
  tear down a subscription.
- A characteristic UUID that appears in more than one service fails with
  `device_ambiguous` unless `service_uuid` selects one.
- A subscription queues at most 64 values and drops the oldest beyond that.
  `gatt.next` returns the oldest queued value, fails with `timeout` when none
  arrives in time, and fails with `device_disconnected` once the link drops
  and the queue is empty.

## 9. Errors

| Code | Use when | `bt` exit status |
| --- | --- | --- |
| `usage` | The request or value is invalid. | 2 |
| `device_not_found` | The device cannot be found. | 3 |
| `device_ambiguous` | Several devices match. | 4 |
| `unsupported` | The device, model, platform, or protocol does not support the operation. | 5 |
| `driver_required` | Another driver is needed. | 5 |
| `permission_denied` | The OS or Betty refused access. | 6 |
| `bluetooth_unavailable` | The adapter or link is unavailable. Retryable. | 7 |
| `device_disconnected` | The device must be connected first, or the link dropped. Retryable. | 7 |
| `authentication_failed` | Pairing or protocol authentication failed. | 8 |
| `interaction_required` | The user must act, for example on an OS prompt. | 8 |
| `timeout` | The device did not answer in time. Retryable. | 9 |
| `internal` | A driver bug or anything else. | 1 |

When a host call fails, propagate its `error_code` unless you have a more
precise one. A driver that crashes, exits without responding, or writes invalid
JSON is treated as `internal`.

## 10. How Betty uses driver results

- Betty runs drivers in ascending order of manifest ID and merges their
  enrichments into its device list. When two drivers report the same property
  or capability, the later driver wins.
- If a driver fails or times out during an `inspect`, Betty ignores that
  driver's result for the command and continues with the next driver.
- For `get` and `set`, Betty asks every driver. The request counts as handled
  if any driver returns an enrichment for the target device. Betty reports a
  driver's error only when no driver handled the request.
- After a `get`, Betty reads the property from the merged device. If no driver
  supplied it, Betty falls back to standard sources, such as the GATT Battery
  Service and Device Information Service.
- If a driver identifies a device whose name is only its address, Betty uses
  the driver's `model` as the device name.
- Betty first tries a command with the devices the OS already knows, with an
  `inspect` of purpose `list`. Only when that is not enough does it scan and
  repeat the `inspect`, now with purpose `scan`, and then the `get` or `set`.
  It scans when the device is not among the known devices, when no driver
  supplies the property, or when a `get` fails, so a driver that needs
  advertisement data, which only a scan provides, receives it on the second
  attempt. A failed `set` is never repeated.
- `bt watch` resolves the device once, then repeats the `get` every 2 seconds
  by default. A retryable error is reported as a warning and polling continues;
  any other error ends the watch. Drivers do not need to implement
  subscriptions.

## 11. Installation and discovery

```console
bt driver install ./bt-driver-example
bt driver list
bt driver remove example/heart-rate
```

`bt driver install` runs `--manifest`, validates the manifest, and copies the
executable into the user's driver directory. It refuses a driver whose ID is
already provided by any of the locations below, because a user-installed copy
would take precedence over a bundled or package-managed driver and inherit its
stored state. Remove the existing driver first to replace it. Betty looks for
drivers in this order, and the first match for an ID wins:

1. the user directory: `<user config dir>/betty/drivers/`, or `$BETTY_DRIVER_DIR`;
2. a `drivers/` directory next to the `bt` executable, or `$BETTY_BUNDLED_DRIVER_DIR`;
3. on Linux, `/usr/lib/betty/drivers/`;
4. for official drivers only, `bt-driver-apple-audio` and `bt-driver-sony` on
   `PATH`, after Betty checks the manifest ID.

Each installed driver lives in a subdirectory named by the unpadded base64url
encoding of its ID, containing `driver` (`driver.exe` on Windows) and
`manifest.json`.

## 12. Security model

Drivers are trusted native code. They run with the user's privileges and are
not sandboxed. The manifest and host-call checks prevent accidental access, not
malicious access: they stop a driver from opening channels it did not declare
or touching devices Betty did not hand it, but a driver can still use the
operating system directly. Install only drivers you trust.

Within those limits, Betty keeps one driver from interfering with another or
with Betty:

- each driver's stored state is private to its ID;
- an install cannot shadow a driver that is already provided;
- deadlines, message-size limits, and a kill on every exit path stop a broken
  driver from hanging Betty, exhausting its memory, or outliving the command.

Drivers SHOULD treat device data as untrusted input, bound every buffer and
loop, and never log keys, PINs, or exported state.

## 13. Stability

Until Betty 1.0, any part of this protocol may change in an incompatible way:
fields, methods, host calls, error codes, and limits. The `protocol` number may
stay at `1` across such changes. Betty does not keep migration paths for
earlier driver IDs or stored state before 1.0.

Drivers SHOULD still be written defensively, because these rules will become
guarantees at 1.0:

- ignore unknown fields;
- answer an unknown `method` with an `unsupported` error and keep running;
- answer a request whose `protocol` is not `1` with an `unsupported` error.

At 1.0, protocol version 1 freezes. After that, only additive changes are made
within a version, and anything else requires protocol version 2.

## 14. Example transcript

`bt get headphones battery` with the Sony driver, a connected WH-1000XM5, and
some fields omitted. `>` is Betty to driver, `<` is driver to Betty.

```text
> {"protocol":1,"type":"request","id":"1","method":"get","device_id":"dev-7","property":"battery","devices":[{"id":"dev-7","name":"WH-1000XM5","paired":true,"connected":true,"services":["956c7b26-d49a-4ba8-b03f-b17d393cb6e2"],"endpoints":[{"id":"dev-7","transport":"classic"}]}]}
< {"protocol":1,"type":"host_request","id":"host-1","method":"rfcomm.open","params":{"device_id":"dev-7","service_uuid":"956c7b26d49a4ba8b03fb17d393cb6e2"}}
> {"protocol":1,"type":"host_response","id":"host-1","result":{"channel_id":"rfcomm-1"}}
< {"protocol":1,"type":"host_request","id":"host-2","method":"rfcomm.write","params":{"channel_id":"rfcomm-1","data":"3e0c…3c"}}
> {"protocol":1,"type":"host_response","id":"host-2","result":{"written":11}}
< {"protocol":1,"type":"host_request","id":"host-3","method":"rfcomm.read","params":{"channel_id":"rfcomm-1","max_bytes":4096,"timeout_ms":1500}}
> {"protocol":1,"type":"host_response","id":"host-3","result":{"data":"3e0c…3c"}}
  ... more reads and writes ...
< {"protocol":1,"type":"host_request","id":"host-9","method":"rfcomm.close","params":{"channel_id":"rfcomm-1"}}
> {"protocol":1,"type":"host_response","id":"host-9","result":{"closed":true}}
< {"protocol":1,"type":"response","id":"1","enrichments":{"dev-7":{"identification":{"vendor":"Sony","family":"Headphones","model":"WH-1000XM5","confidence":"protocol"},"properties":{"battery":{"value":{"level":80},"unit":"%","display":"80%"}}}}}
  (Betty closes stdin; the driver exits 0)
```

## 15. Minimal driver in Python

This complete driver identifies devices whose names start with `ACME-` and
reports a fixed battery level. It shows the whole protocol surface a driver
needs; replace the battery value with real host calls.

```python
#!/usr/bin/env python3
import json
import sys

MANIFEST = {"protocol": 1, "id": "example/acme", "name": "ACME", "version": "0.1.0"}


def send(message):
    message["protocol"] = 1
    sys.stdout.write(json.dumps(message) + "\n")
    sys.stdout.flush()


def ours(device):
    return device.get("name", "").startswith("ACME-")


def handle(request):
    devices = {d["id"]: d for d in request.get("devices", [])}
    method = request.get("method")
    if method == "inspect":
        return {"enrichments": {
            d["id"]: {
                "identification": {"vendor": "ACME", "family": "Gadget", "confidence": "name"},
                "capabilities": {"battery": {"status": "available", "type": "battery", "readable": True}},
            }
            for d in devices.values() if ours(d)
        }}
    if method in ("get", "set"):
        device = devices.get(request.get("device_id"))
        if method == "get" and device and ours(device) and request.get("property") == "battery":
            return {"enrichments": {device["id"]: {"properties": {"battery": {"value": {"level": 80}, "unit": "%"}}}}}
        return {}  # not ours: let another driver answer
    return {"error": f"unsupported method {method!r}", "error_code": "unsupported"}


def main():
    if sys.argv[1:] == ["--manifest"]:
        print(json.dumps(MANIFEST))
        return
    for line in sys.stdin:  # ends when Betty closes stdin
        if not line.strip():
            continue
        request = json.loads(line)
        if request.get("protocol") != 1:
            response = {"error": "unsupported protocol", "error_code": "unsupported"}
        else:
            response = handle(request)
        response.update(type="response", id=request.get("id"))
        send(response)


if __name__ == "__main__":
    main()
```

To make a host call from Python, write a `host_request` with `send` and read the
next line from stdin before doing anything else; it is the `host_response`.

On Windows, `bt driver install` expects a native executable, so package a script
with a launcher such as PyInstaller.
