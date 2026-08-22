#ifndef BETTY_AIRPODS_SYSTEM_KEYS_DARWIN_H
#define BETTY_AIRPODS_SYSTEM_KEYS_DARWIN_H

#include <stddef.h>

// Returns a malloc-owned copy of the MobileBluetooth password data.
// The caller must free *data. A zero status indicates success.
int betty_airpods_system_keys(const char *account, void **data, size_t *length);

#endif
