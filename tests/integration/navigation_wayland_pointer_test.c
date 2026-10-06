/* Container entry: keep the existing explicit TEST-container authority. */
#define _POSIX_C_SOURCE 200809L
#include "navigation_wayland_pointer_core.h"

int main(int argc, char **argv) {
    if (!getenv("NAVIGATION_TEST_CONTAINER") ||
        strcmp(getenv("NAVIGATION_TEST_CONTAINER"), "1") ||
        !getenv("NAVIGATION_WAYLAND_TEST") ||
        strcmp(getenv("NAVIGATION_WAYLAND_TEST"), "1") ||
        access("/.dockerenv", F_OK) || argc != 2) return 2;
    char *end = NULL; errno = 0;
    unsigned long y = strtoul(argv[1], &end, 10);
    if (errno || argv[1][0] < '0' || argv[1][0] > '9' || *end || y >= 720) return 2;
    alarm(20); /* Bounds even a hung compositor roundtrip; no global process control. */
    struct wl_display *display = wl_display_connect(NULL);
    if (!display) return 3;
    return navigation_pointer_test_run(display, y);
}
