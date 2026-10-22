# Betty PlayStation Controllers driver

Optional support for Sony's PlayStation controllers in [Betty](../../README.md).
The driver is a separate executable, `bt-driver-playstation`; the report
decoding is not linked into the base `bt` binary.

## Capabilities

| Property | State | Values / shape | Current scope |
| --- | --- | --- | --- |
| `battery` | Available | one percentage in 10% steps (`estimated`), with charging state | Connected over Bluetooth, on Windows |

## Devices

The driver confirms a controller by the vendor and product IDs of its HID
gamepad collection, not by its name:

| Controller | Product ID | Hardware tested |
| --- | --- | --- |
| DualSense | `0x0CE6` | Yes |
| DualSense Edge | `0x0DF2` | No |
| DualShock 4 (both versions) | `0x05C4`, `0x09CC` | No: decoding is covered by unit tests only |

A disconnected controller is identified by name when its name says DualSense.

## Reading the battery switches the controller's reports

The controllers report battery only in an extended input report, which they
send over Bluetooth after a host asks for a calibration feature report. They
keep sending it until they are turned off. Games that read the controller
through DirectInput may then stop seeing it until it is turned off and on;
Steam and SDL-based games switch it themselves and are unaffected.

So the driver never switches the controller during `bt list`, `bt scan` or
`bt inspect`: those show the battery only when the controller is already
sending extended reports, for example because Steam is running. An explicit
`bt get <controller> battery` switches it when needed, and says so on stderr.

## Platforms

The driver uses Betty's `hid` host calls, which only Windows provides so far.
On Linux the kernel's `hid-playstation` driver already reports the battery,
and on macOS Apple's GameController framework does; Betty doesn't read either
yet.

## Protocol references

Report layouts follow Linux's `hid-playstation` driver:

- DualSense: extended report `0x31` (78 bytes with a CRC-32), switched by
  feature report `0x05`. The status byte is at offset 54: the low nibble is the
  level in tenths, the high nibble the charging state.
- DualShock 4: extended report `0x11` (78 bytes with a CRC-32), switched by
  feature report `0x02`. The status byte is at offset 32: the low nibble is the
  level in tenths, 11 meaning full, and bit 4 is set while a cable is in.
