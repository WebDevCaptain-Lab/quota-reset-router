#ifndef QUOTA_RESET_ROUTER_BRIDGE_H
#define QUOTA_RESET_ROUTER_BRIDGE_H

#include <stdint.h>
#include <stdlib.h>

typedef struct { void *ptr; size_t len; } cliproxy_buffer;
typedef struct {
    uint32_t abi_version;
    void *host_ctx;
    void *call;
    void *free_buffer;
} cliproxy_host_api;
typedef int (*plugin_call_fn)(char *, uint8_t *, size_t, cliproxy_buffer *);
typedef void (*plugin_free_fn)(void *, size_t);
typedef void (*plugin_shutdown_fn)(void);
typedef struct {
    uint32_t abi_version;
    plugin_call_fn call;
    plugin_free_fn free_buffer;
    plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char *, uint8_t *, size_t, cliproxy_buffer *);
extern void cliproxyPluginFree(void *, size_t);
extern void cliproxyPluginShutdown(void);
int bridge_host_call(cliproxy_host_api *, char *, uint8_t *, size_t, cliproxy_buffer *);
void bridge_host_free(cliproxy_host_api *, cliproxy_buffer *);
#endif
