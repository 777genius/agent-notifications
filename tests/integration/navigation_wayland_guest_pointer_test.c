/* Guest effect library: the Python entry validates TEST authority before loading it.
 * The already-connected fd is transferred by that entry, never resolved from env. */
#define _POSIX_C_SOURCE 200809L
#include "navigation_wayland_pointer_core.h"

int navigation_guest_pointer_test(int fd, unsigned long y) {
    if (fd < 0 || y >= 720) return 2;
    alarm(20);
    struct wl_display *display = wl_display_connect_to_fd(fd);
    if (!display) return 3;
    return navigation_pointer_test_run(display, y);
}
