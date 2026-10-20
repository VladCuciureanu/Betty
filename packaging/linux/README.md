# Betty Linux packages

Build the Debian packages natively on Linux:

```console
./packaging/linux/build-deb.sh 1.0.0
```

This produces three packages:

- `betty`, containing only the device-generic `bt` executable;
- `betty-apple-audio`, containing the optional AirPods and Beats driver;
- `betty-sony`, containing the optional Sony headphones driver, which recognizes protocol v1 and v2 devices and reads battery from v2 devices.

The optional packages depend on the matching Betty version. Classic L2CAP uses a
connection-oriented `SOCK_SEQPACKET` socket, not a raw socket, so neither `bt` nor
the optional driver receives `CAP_NET_RAW` or runs as root. Linux reserves that
capability check for `SOCK_RAW` L2CAP sockets. Per-user driver keys in the user's
configuration directory are retained as user data when a package is removed.
