#ifndef BETTY_BRIDGE_DARWIN_H
#define BETTY_BRIDGE_DARWIN_H
int betty_paired_count(void);
void betty_refresh(void);
char *betty_paired_name(int index);
char *betty_paired_address(int index);
int betty_paired_connected(int index);
int betty_device_paired(int index);
int betty_pair(const char *address);
int betty_connect(const char *address);
int betty_disconnect(const char *address);
#endif
