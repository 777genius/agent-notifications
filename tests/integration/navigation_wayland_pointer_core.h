/* Shared TEST effect; callers must establish their own private-session authority first. */
#include <errno.h>
#include <poll.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>
#include <wayland-client.h>
#include "wlr-virtual-pointer-client-protocol.h"

static struct zwlr_virtual_pointer_manager_v1 *manager;
static struct wl_output *output;
static unsigned outputs;

static void global(void *data, struct wl_registry *registry, uint32_t name,
                   const char *interface, uint32_t version) {
    (void)data;
    if (!strcmp(interface, "zwlr_virtual_pointer_manager_v1") && version >= 2 && !manager)
        manager = wl_registry_bind(registry, name, &zwlr_virtual_pointer_manager_v1_interface, 2);
    if (!strcmp(interface, "wl_output")) {
        ++outputs;
        if (!output) output = wl_registry_bind(registry, name, &wl_output_interface, 1);
    }
}
static void removed(void *data, struct wl_registry *registry, uint32_t name) {
    (void)data; (void)registry; (void)name;
    /* A changing global inventory cannot qualify this fixed-output fixture. */
    _exit(3);
}
static uint32_t millis(void) {
    struct timespec value;
    if (clock_gettime(CLOCK_MONOTONIC, &value)) _exit(4);
    return (uint32_t)((uint64_t)value.tv_sec * 1000 + value.tv_nsec / 1000000);
}

static int navigation_pointer_test_run(struct wl_display *display, unsigned long y) {
    struct wl_registry *registry = wl_display_get_registry(display);
    const struct wl_registry_listener listener = {global, removed};
    if (wl_registry_add_listener(registry, &listener, NULL) ||
        wl_display_roundtrip(display) < 0 || !manager || !output || outputs != 1) return 3;
    struct zwlr_virtual_pointer_v1 *pointer =
        zwlr_virtual_pointer_manager_v1_create_virtual_pointer_with_output(manager, NULL, output);
    if (!pointer) return 3;
    zwlr_virtual_pointer_v1_motion_absolute(pointer, millis(), 640, (uint32_t)y, 1280, 720);
    zwlr_virtual_pointer_v1_frame(pointer);
    if (wl_display_roundtrip(display) < 0) return 3;
    puts("MOVED"); fflush(stdout);
    /* Parent must observe native pointer.enter before authorizing this single click. */
    struct pollfd input = {STDIN_FILENO, POLLIN, 0};
    char command[7];
    if (poll(&input, 1, 10000) != 1 || !(input.revents & POLLIN) ||
        read(STDIN_FILENO, command, sizeof(command)) != 6 || memcmp(command, "CLICK\n", 6)) return 5;
    zwlr_virtual_pointer_v1_button(pointer, millis(), 0x110, WL_POINTER_BUTTON_STATE_PRESSED);
    zwlr_virtual_pointer_v1_frame(pointer);
    if (wl_display_roundtrip(display) < 0) return 3;
    zwlr_virtual_pointer_v1_button(pointer, millis(), 0x110, WL_POINTER_BUTTON_STATE_RELEASED);
    zwlr_virtual_pointer_v1_frame(pointer);
    if (wl_display_roundtrip(display) < 0) return 3;
    puts("CLICKED"); fflush(stdout);
    /* Retain the input device until the collector observes the native token chain. */
    if (poll(&input, 1, 10000) != 1 || !(input.revents & POLLIN) ||
        read(STDIN_FILENO, command, sizeof(command)) != 5 || memcmp(command, "DONE\n", 5)) return 5;
    zwlr_virtual_pointer_v1_destroy(pointer);
    zwlr_virtual_pointer_manager_v1_destroy(manager);
    wl_output_destroy(output); wl_registry_destroy(registry);
    wl_display_disconnect(display);
    return 0;
}
