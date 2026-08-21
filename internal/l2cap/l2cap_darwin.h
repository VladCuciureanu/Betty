#ifndef BETTY_L2CAP_DARWIN_H
#define BETTY_L2CAP_DARWIN_H
#include <stdint.h>
long long betty_l2cap_open(const char *address, unsigned short psm, int *error);
int betty_l2cap_read(long long channel_id, void *buffer, int maximum, int timeout_ms, int *error);
int betty_l2cap_write(long long channel_id, void *data, int length);
int betty_l2cap_close(long long channel_id);
#endif
