#include "bridge.h"

typedef int (*host_call_fn)(void *, char *, uint8_t *, size_t, cliproxy_buffer *);
typedef void (*host_free_fn)(void *, size_t);

int bridge_host_call(cliproxy_host_api *host, char *method, uint8_t *request,
                     size_t length, cliproxy_buffer *response) {
    if (!host || !host->call) return 1;
    return ((host_call_fn)host->call)(host->host_ctx, method, request, length, response);
}

void bridge_host_free(cliproxy_host_api *host, cliproxy_buffer *buffer) {
    if (host && host->free_buffer && buffer && buffer->ptr) {
        ((host_free_fn)host->free_buffer)(buffer->ptr, buffer->len);
    }
}
