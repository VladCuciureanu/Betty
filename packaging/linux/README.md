# Betty Linux packages

Build the Debian packages natively on Linux:

```console
./packaging/linux/build-deb.sh 1.0.0
```

This produces two packages:

- `betty`, containing only the device-generic `bt` executable;
- `betty-airpods`, containing the optional AirPods driver.
- `betty-sony`, containing the optional Sony v1/v2 headphones driver.

The optional package depends on the matching Betty version and gives only its
driver executable `CAP_NET_RAW`, which is required to open the AirPods Classic
L2CAP channel. Betty itself is never run as root and receives no capability.
Removing `betty-airpods` removes the capable executable. Per-user driver keys in
the user's configuration directory are retained as user data.
