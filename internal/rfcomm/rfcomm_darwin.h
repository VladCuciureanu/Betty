#ifndef BETTY_RFCOMM_DARWIN_H
#define BETTY_RFCOMM_DARWIN_H
#include <stdint.h>
long long betty_rfcomm_open(const char *address, const char *service_uuid, int *error);
int betty_rfcomm_read(long long channel_id, void *buffer, int maximum, int timeout_ms, int *error);
int betty_rfcomm_write(long long channel_id, void *data, int length);
int betty_rfcomm_close(long long channel_id);
#endif
