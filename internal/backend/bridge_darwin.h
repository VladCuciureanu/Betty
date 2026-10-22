#ifndef BETTY_BRIDGE_DARWIN_H
#define BETTY_BRIDGE_DARWIN_H

#include <stdint.h>

typedef struct {
	char *name;
	char *address;
	int connected;
	int paired;
} betty_device;

// betty_copy_paired_devices returns a malloc'd snapshot of the paired devices
// and stores its length in count. Free it with betty_free_devices.
betty_device *betty_copy_paired_devices(int *count);
void betty_free_devices(betty_device *devices, int count);

// betty_inquiry runs a Classic device inquiry on the calling thread's run
// loop until it completes, timeout_ms elapses or *cancel becomes non-zero, and
// returns a malloc'd array of the devices found, like
// betty_copy_paired_devices. *status receives the inquiry's IOReturn.
betty_device *betty_inquiry(int timeout_ms, int *cancel, int *count, int *status);

int betty_connect(const char *address);
int betty_disconnect(const char *address);
int betty_is_connected(const char *address);
int betty_is_paired(const char *address);

// betty_unpair removes the device's pairing through the private
// -[IOBluetoothDevice remove] (#14). It returns kIOReturnUnsupported when
// macOS no longer has it.
int betty_unpair(const char *address);

// betty_pair runs the pairing on the calling thread's run loop until it
// finishes, timeout_ms elapses (0 means no limit) or *cancel becomes non-zero.
// agent is passed back to the Go pairing callbacks.
int betty_pair(const char *address, int timeout_ms, int *cancel, uintptr_t agent);

// betty_adapter reports the default host controller: malloc'd address and
// name strings, which the caller frees, and whether it is powered on.
int betty_adapter(char **address, char **name, int *powered);

// betty_set_adapter_power asks macOS to turn the controller on or off
// through the private IOBluetoothPreferenceSetControllerPowerState (#15),
// and betty_adapter_power_state reads the setting back: 1 on, 0 off. Both
// return kIOReturnUnsupported (as an int) when macOS no longer has them.
int betty_set_adapter_power(int on);
int betty_adapter_power_state(int *on);

#endif
