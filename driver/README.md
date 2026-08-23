# Betty driver protocol

Optional device support runs as trusted external executables. Drivers enrich
Betty's generic device model and property vocabulary; they do not add vendor
commands to the CLI.

## Boundary

Betty starts a driver as a child process and communicates through a versioned,
duplex JSON protocol over stdin and stdout. A driver may handle `inspect`, `get`,
and `set` requests and return identification, properties, capabilities, or a
structured error.

During that session, a driver may request only the host operations declared in
its manifest:

- Classic L2CAP open, read, write, and close;
- RFCOMM open, read, write, and close;
- namespaced persistent state reads and writes.

Host access is restricted to device IDs Betty supplied to the driver and to
channel IDs opened during the current session. Persistent state is isolated by
driver ID and written with user-only permissions.

## Capability model

A capability has a stable property name and reports:

- status: `available`, `planned`, or `unsupported`;
- type, such as `battery`, `enum`, `boolean`, or `integer`;
- whether it is readable or writable;
- accepted enum values or numeric minimum, maximum, and step;
- a reason when it is not currently available.

Shared concepts use normalized names such as `battery`, `noise_control`, and
`ambient_level`. Only genuinely vendor-specific behavior is namespaced, for
example `sony.speak_to_chat`.

## Discovery and installation

`bt driver install <executable>` reads and verifies the executable's manifest,
then installs it in Betty's per-user driver directory. Official package-manager
drivers can also be discovered by exact executable name on `PATH`; Betty checks
the manifest ID before using one.

```console
bt driver install ./bt-driver-example
bt driver list
bt driver remove example/vendor
```

## Security status

Executable drivers are currently trusted native code. Manifest permissions and
the host API reduce accidental authority, but they are not a process sandbox.
Install only drivers you trust. Sandboxing, cryptographic endpoint identity, and
formal protocol upgrade/deprecation rules remain roadmap work.

The public Go types and session implementation in this directory are the
protocol reference. Protocol version 1 clients should ignore unknown JSON
fields so compatible fields can be added without changing existing meaning.
