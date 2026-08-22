# Betty Linux packages

Build the Debian packages natively on Linux:

```console
./packaging/linux/build-deb.sh 1.0.0
```

This produces three packages:

- `betty`, containing only the device-generic `bt` executable;
- `betty-apple-audio`, containing the optional AirPods and Beats driver;
- `betty-sony`, containing the optional Sony v1/v2 headphones driver.

The optional package depends on the matching Betty version and gives only its
driver executable `CAP_NET_RAW`, which is required to open the Apple Audio Classic
L2CAP channel. Betty itself is never run as root and receives no capability.
Removing `betty-apple-audio` removes the capable executable. Per-user driver keys in
the user's configuration directory are retained as user data.
