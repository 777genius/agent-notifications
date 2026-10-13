/* TEST-only Sway protocol observation. Never preload this into the selected client. */
#define _GNU_SOURCE
#include <dlfcn.h>
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/statfs.h>
#include <sys/statvfs.h>
#include <sys/socket.h>
#include <unistd.h>
#include <wayland-server-core.h>

#define CLIENT_LIMIT 128
#define EVENT_LIMIT 512
#define TOKEN_LIMIT 4096
#define RESOURCE_LIMIT 256
/* Linux UAPI, commit 7b26952a91cf65ff1cc867a2382a8964d8c0ee7d; fixed amd64 TEST guest. */
#ifndef SO_PEERPIDFD
#define SO_PEERPIDFD 77
#endif

struct observed_client {
    struct wl_listener destroy;
    struct wl_listener created;
    struct wl_client *client;
    uint32_t epoch;
    pid_t pid;
    int pidfd;
    unsigned long long birth;
    bool ended;
};

struct observed_resource {
    struct wl_listener destroy;
    struct wl_resource *resource;
    uint32_t connection, id;
    const char *interface;
    bool ended;
};

static struct observed_client clients[CLIENT_LIMIT];
static unsigned client_count, event_count;
static struct observed_resource resources[RESOURCE_LIMIT];
static unsigned resource_count;
static int output_fd = -1;
static bool failed;

static void emit(const char *format, ...) {
    if (failed || output_fd < 0) return;
    char buffer[10000];
    va_list arguments;
    va_start(arguments, format);
    int count = vsnprintf(buffer, sizeof buffer, format, arguments);
    va_end(arguments);
    if (count <= 0 || (size_t)count >= sizeof buffer || event_count >= EVENT_LIMIT) {
        const char fault[] = "{\"kind\":\"fault\",\"reason\":\"observer_bound_exceeded\"}\n";
        (void)write(output_fd, fault, sizeof fault - 1);
        failed = true;
        _exit(74);  /* A truncated valid prefix must never leave an apparently healthy Sway. */
    }
    event_count++;
    size_t offset = 0;
    while (offset < (size_t)count) {
        ssize_t written = write(output_fd, buffer + offset, (size_t)count - offset);
        if (written < 0 && errno == EINTR) continue;
        if (written <= 0) { failed = true; _exit(74); }
        offset += (size_t)written;
    }
}

static void fault(const char *reason) {
    emit("{\"kind\":\"fault\",\"reason\":\"%s\"}\n", reason);
    failed = true;
    _exit(74);
}

static unsigned long long birth(pid_t pid) {
    char path[64], buffer[4096];
    snprintf(path, sizeof path, "/proc/%d/stat", (int)pid);
    int fd = open(path, O_RDONLY | O_CLOEXEC);
    if (fd < 0) return 0;
    ssize_t length = read(fd, buffer, sizeof buffer - 1);
    close(fd);
    if (length <= 0 || length == (ssize_t)sizeof buffer - 1) return 0;
    buffer[length] = 0;
    char *cursor = strrchr(buffer, ')'), *saved = NULL;
    if (!cursor || cursor[1] != ' ') return 0;
    cursor += 2;
    for (unsigned field = 0; field <= 19; field++) {
        char *value = strtok_r(field == 0 ? cursor : NULL, " ", &saved);
        if (!value) return 0;
        if (field == 19) {
            char *end = NULL;
            errno = 0;
            unsigned long long result = strtoull(value, &end, 10);
            return errno || !end || *end ? 0 : result;
        }
    }
    return 0;
}

static bool live(const struct observed_client *client) {
    struct pollfd descriptor = { .fd = client->pidfd, .events = POLLIN };
    return !client->ended && poll(&descriptor, 1, 0) == 0 &&
        birth(client->pid) == client->birth;
}

static bool peer_pidfd_identity(int fd, pid_t expected) {
    char path[64], buffer[512];
    snprintf(path, sizeof path, "/proc/self/fdinfo/%d", fd);
    FILE *stream = fopen(path, "re");
    if (!stream) return false;
    bool matched = false;
    while (fgets(buffer, sizeof buffer, stream)) {
        int pid = -1;
        char extra = 0;
        if (sscanf(buffer, "Pid: %d %c", &pid, &extra) == 1) matched = pid == expected;
    }
    bool valid = matched && !ferror(stream);
    fclose(stream);
    return valid;
}

static void destroyed(struct wl_listener *listener, void *data) {
    struct observed_client *client = wl_container_of(listener, client, destroy);
    if (data != client->client || client->ended) { fault("client_destroy_identity_changed"); return; }
    emit("{\"kind\":\"client_destroy\",\"connection\":%u}\n", client->epoch);
    client->ended = true;
    /* Keep this pidfd until Sway exits; never recycle it into another connection. */
}

static void resource_destroyed(struct wl_listener *listener, void *data) {
    struct observed_resource *resource = wl_container_of(listener, resource, destroy);
    if (data != resource->resource || resource->ended) { fault("resource_destroy_identity_changed"); return; }
    emit("{\"kind\":\"resource_destroy\",\"connection\":%u,\"interface\":\"%s\",\"object\":%u}\n",
        resource->connection, resource->interface, resource->id);
    resource->ended = true;
}

static void observe_resource(struct observed_client *client, struct wl_resource *value) {
    const char *interface = wl_resource_get_class(value);
    const char *known = NULL;
    if (strcmp(interface, "wl_surface") == 0) known = "wl_surface";
    if (strcmp(interface, "xdg_surface") == 0) known = "xdg_surface";
    if (strcmp(interface, "xdg_toplevel") == 0) known = "xdg_toplevel";
    if (!known) return;
    if (wl_resource_get_client(value) != client->client) { fault("resource_connection_mismatch"); return; }
    for (unsigned index = 0; index < resource_count; index++) {
        if (resources[index].resource == value && !resources[index].ended) return;
    }
    if (resource_count >= RESOURCE_LIMIT) { fault("resource_bound_exceeded"); return; }
    struct observed_resource *resource = &resources[resource_count++];
    *resource = (struct observed_resource){ .resource = value, .connection = client->epoch,
        .id = wl_resource_get_id(value), .interface = known };
    resource->destroy.notify = resource_destroyed;
    wl_resource_add_destroy_listener(value, &resource->destroy);
    emit("{\"kind\":\"resource_live\",\"connection\":%u,\"interface\":\"%s\",\"object\":%u}\n",
        resource->connection, known, resource->id);
}

static void resource_created(struct wl_listener *listener, void *data) {
    struct observed_client *client = wl_container_of(listener, client, created);
    if (client->ended || !live(client)) { fault("resource_created_after_client_exit"); return; }
    observe_resource(client, data);
}

static struct observed_client *connection(struct wl_client *value) {
    for (unsigned index = 0; index < client_count; index++) {
        if (clients[index].client == value && !clients[index].ended) {
            if (!live(&clients[index])) { fault("client_incarnation_not_live"); return NULL; }
            return &clients[index];
        }
    }
    if (client_count >= CLIENT_LIMIT) { fault("connection_bound_exceeded"); return NULL; }
    pid_t pid = 0;
    uid_t uid = 0;
    gid_t gid = 0;
    wl_client_get_credentials(value, &pid, &uid, &gid);
    if (pid <= 0 || pid == getpid() || uid != 1000 || gid != 1000) {
        fault("actual_client_credentials_rejected"); return NULL;
    }
    /* Get the peer's retained kernel identity from the actual socket. Numeric
       pidfd_open could bind a reused PID after the original connector exited. */
    int pidfd = -1;
    socklen_t size = sizeof pidfd;
    if (getsockopt(wl_client_get_fd(value), SOL_SOCKET, SO_PEERPIDFD, &pidfd, &size) ||
        size != sizeof pidfd || pidfd < 0) {
        if (pidfd >= 0) close(pidfd);
        fault("actual_peer_pidfd_unavailable"); return NULL;
    }
    unsigned long long before = birth(pid);
    if (!before || !peer_pidfd_identity(pidfd, pid) || fcntl(pidfd, F_SETFD, FD_CLOEXEC)) {
        close(pidfd); fault("actual_peer_pidfd_identity_unbound"); return NULL;
    }
    struct observed_client *client = &clients[client_count];
    *client = (struct observed_client){ .client = value, .epoch = client_count + 1,
        .pid = pid, .pidfd = pidfd, .birth = before };
    if (!live(client)) { close(pidfd); fault("client_birth_raced"); return NULL; }
    client_count++;
    client->destroy.notify = destroyed;
    wl_client_add_destroy_listener(value, &client->destroy);
    client->created.notify = resource_created;
    wl_client_add_resource_created_listener(value, &client->created);
    emit("{\"kind\":\"connection\",\"connection\":%u,\"pid\":%d,\"uid\":%u,\"gid\":%u,\"birth\":%llu,\"pidfd\":%d}\n",
        client->epoch, (int)pid, (unsigned)uid, (unsigned)gid, before, pidfd);
    return client;
}

static void protocol(void *data, enum wl_protocol_logger_type direction,
                     const struct wl_protocol_logger_message *message) {
    (void)data;
    if (failed || direction != WL_PROTOCOL_LOGGER_REQUEST) return;
    const char *interface = wl_resource_get_class(message->resource);
    const char *method = message->message->name;
    bool surface = strcmp(interface, "xdg_wm_base") == 0 && strcmp(method, "get_xdg_surface") == 0;
    bool toplevel = strcmp(interface, "xdg_surface") == 0 && strcmp(method, "get_toplevel") == 0;
    bool activation = strcmp(interface, "xdg_activation_v1") == 0 && strcmp(method, "activate") == 0;
    bool destroy = strcmp(method, "destroy") == 0 &&
        (strcmp(interface, "wl_surface") == 0 || strcmp(interface, "xdg_surface") == 0 || strcmp(interface, "xdg_toplevel") == 0);
    if (!surface && !toplevel && !activation && !destroy) return;
    struct observed_client *client = connection(wl_resource_get_client(message->resource));
    if (!client) return;
    uint32_t object = wl_resource_get_id(message->resource);
    if (surface) {
        if (message->arguments_count != 2 || !message->arguments[1].o) { fault("surface_arguments_invalid"); return; }
        struct wl_resource *target = (struct wl_resource *)message->arguments[1].o;
        if (wl_resource_get_client(target) != client->client || strcmp(wl_resource_get_class(target), "wl_surface")) {
            fault("surface_connection_mismatch"); return;
        }
        observe_resource(client, target);
        emit("{\"kind\":\"xdg_surface\",\"connection\":%u,\"xdgSurface\":%u,\"surface\":%u}\n",
            client->epoch, message->arguments[0].n, wl_resource_get_id(target));
    } else if (toplevel) {
        if (message->arguments_count != 1) { fault("toplevel_arguments_invalid"); return; }
        emit("{\"kind\":\"toplevel\",\"connection\":%u,\"xdgSurface\":%u,\"toplevel\":%u}\n",
            client->epoch, object, message->arguments[0].n);
    } else if (activation) {
        if (message->arguments_count != 2 || !message->arguments[0].s || !message->arguments[1].o) { fault("activation_arguments_invalid"); return; }
        const unsigned char *token = (const unsigned char *)message->arguments[0].s;
        size_t length = strnlen((const char *)token, TOKEN_LIMIT + 1);
        struct wl_resource *target = (struct wl_resource *)message->arguments[1].o;
        if (!length || length > TOKEN_LIMIT || wl_resource_get_client(target) != client->client || strcmp(wl_resource_get_class(target), "wl_surface")) {
            fault("activation_token_or_connection_invalid"); return;
        }
        char hex[TOKEN_LIMIT * 2 + 1];
        const char digits[] = "0123456789abcdef";
        for (size_t index = 0; index < length; index++) {
            hex[index * 2] = digits[token[index] >> 4]; hex[index * 2 + 1] = digits[token[index] & 15];
        }
        hex[length * 2] = 0;
        emit("{\"kind\":\"activate\",\"connection\":%u,\"surface\":%u,\"tokenHex\":\"%s\"}\n",
            client->epoch, wl_resource_get_id(target), hex);
    } else {
        observe_resource(client, message->resource);
        emit("{\"kind\":\"destroy\",\"connection\":%u,\"interface\":\"%s\",\"object\":%u}\n",
            client->epoch, interface, object);
    }
}

static bool authority(void) {
    const char *value = getenv("NAVIGATION_TEST_SERVER_PROTOCOL_FD");
    if (getuid() != 1000 || !value || !*value) return false;
    char *end = NULL;
    errno = 0;
    long candidate = strtol(value, &end, 10);
    if (errno || !end || *end || candidate < 3 || candidate > 1024) return false;
    struct stat file;
    if (fstat((int)candidate, &file) || !S_ISREG(file.st_mode) || file.st_uid != 0 ||
        (file.st_mode & 0777) != 0600 || file.st_size != 0 || file.st_nlink != 1) return false;
    int flags = fcntl((int)candidate, F_GETFL);
    if (flags < 0 || (flags & O_ACCMODE) != O_WRONLY || !(flags & O_APPEND)) return false;
    const char seed[] = "/mnt/navigation-handoff-test-seed";
    struct statfs filesystem;
    struct statvfs mount;
    if (statfs(seed, &filesystem) || filesystem.f_type != 0x9660 ||
        statvfs(seed, &mount) || !(mount.f_flag & ST_RDONLY)) return false;
    char marker[64];
    int fd = open("/mnt/navigation-handoff-test-seed/navigation.marker", O_RDONLY | O_CLOEXEC | O_NOFOLLOW);
    if (fd < 0) return false;
    ssize_t count = read(fd, marker, sizeof marker);
    close(fd);
    const char expected[] = "Linux selected-client handoff TEST only\n";
    if (count != (ssize_t)sizeof expected - 1 || memcmp(marker, expected, sizeof expected - 1)) return false;
    output_fd = (int)candidate;
    if (fcntl(output_fd, F_SETFD, FD_CLOEXEC)) fault("private_output_CLOEXEC_failed");
    return true;
}

struct wl_display *wl_display_create(void) {
    struct wl_display *(*original)(void) = dlsym(RTLD_NEXT, "wl_display_create");
    if (!original) abort();
    struct wl_display *display = original();
    if (display && output_fd >= 0) fault("multiple_TEST_displays_unsupported");
    if (!display || !authority()) return display;
    if (!wl_display_add_protocol_logger(display, protocol, NULL)) { fault("logger_registration_failed"); return display; }
    emit("{\"kind\":\"ready\",\"compositorPID\":%d}\n", (int)getpid());
    return display;
}
