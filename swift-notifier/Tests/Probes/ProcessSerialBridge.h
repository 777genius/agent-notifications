#include <stdint.h>
#include <sys/types.h>
// Swift deliberately does not import pre-10.9 deprecated APIs. This test-only
// C bridge calls the public SDK function without a private ABI declaration.
int32_t nav_get_process_serial(pid_t pid, uint32_t serial[2]);
int32_t nav_send_process_serial(const uint32_t serial[2], const char *url,
                              int32_t *permission, int32_t *reply_error);
