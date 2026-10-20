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

int betty_connect(const char *address);
int betty_disconnect(const char *address);
int betty_is_connected(const char *address);
int betty_is_paired(const char *address);

// betty_pair runs the pairing on the calling thread's run loop until it
// finishes, timeout_ms elapses (0 means no limit) or *cancel becomes non-zero.
// agent is passed back to the Go pairing callbacks.
int betty_pair(const char *address, int timeout_ms, int *cancel, uintptr_t agent);

#endif
