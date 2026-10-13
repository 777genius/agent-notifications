/* TEST actors only. All trace text comes from the reviewed observer/kernel. */
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <pthread.h>
#include <sched.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/syscall.h>
#include <sys/wait.h>
#include <unistd.h>

static const char *primary, *selector, *mode;
static int stage;
static void checked_write(int fd, const char *s) {
    size_t n = strlen(s);
    if (write(fd, s, n) != (ssize_t)n) _exit(81);
}
static void helper_exec(void) {
    char *const args[] = {(char *)primary, "cursor-event", "stop", "--binding", (char *)selector, NULL};
    if (!strcmp(mode, "bad-fd0")) {
        int fd = open("/dev/null", O_RDONLY);
        if (fd < 0 || dup2(fd, 0) != 0) _exit(92);
        if (fd != 0) close(fd);
    }
    if (!strcmp(mode, "execveat") || !strcmp(mode, "bad-exe")) {
        const char *executable = !strcmp(mode, "bad-exe") ? getenv("TEST_OTHER_EXE") : primary;
        if (!executable) _exit(95);
        int fd = open(executable, O_RDONLY | O_CLOEXEC);
        if (fd < 0) _exit(82);
        syscall(SYS_execveat, fd, "", args, environ, AT_EMPTY_PATH);
    } else {
        execve(primary, args, environ);
    }
    _exit(83);
}
static void *thread_exit(void *unused) {
    (void)unused;
    checked_write(stage, "THREAD\n");
    return NULL;
}
static void *thread_exec(void *unused) {
    (void)unused;
    helper_exec();
    return NULL;
}
int main(int argc, char **argv) {
    if (argc != 5) return 80;
    primary = argv[1]; selector = argv[2]; mode = argv[3]; stage = atoi(argv[4]);
    checked_write(stage, "LEADER\n");
    char start;
    if (read(stage, &start, 1) != 1 || start != 'G') return 84;
    if (!strcmp(mode, "oom-read")) {
        size_t size = 1024 * 1024;
        void *data = malloc(size);
        int fd = open("/dev/zero", O_RDONLY);
        if (!data || fd < 0) return 96;
        ssize_t count = read(fd, data, size);
        close(fd); free(data);
        return count == (ssize_t)size ? 0 : 97;
    }
    if (!strcmp(mode, "clone-orders")) {
        /* Each child executes real syscalls and is joined before the next
         * allocation. Actual wait notifications, never this loop index, decide
         * the order. The driver refuses if both orders were not witnessed.
         */
        for (int iteration = 0; iteration < 64; ++iteration) {
            pid_t p = fork();
            if (p < 0) return 93;
            if (!p) { checked_write(stage, "CHILD\n"); _exit(0); }
            int status;
            if (waitpid(p, &status, 0) != p || !WIFEXITED(status) || WEXITSTATUS(status)) return 94;
        }
        checked_write(stage, "COMPLETE\n");
        return 0;
    }
    if (!strcmp(mode, "thread")) {
        pthread_t tid;
        if (pthread_create(&tid, NULL, thread_exit, NULL)) return 85;
        return pthread_join(tid, NULL) ? 86 : 0;
    }
    if (!strcmp(mode, "remap")) {
        /* Exec is in an unselected child thread, not the marked leader. */
        pid_t p = fork();
        if (p < 0) return 87;
        if (!p) {
            pthread_t tid;
            if (pthread_create(&tid, NULL, thread_exec, NULL)) _exit(88);
            for (;;) pause();
        }
        int status;
        return waitpid(p, &status, 0) == p && WIFEXITED(status) && !WEXITSTATUS(status) ? 0 : 89;
    }
    pid_t child = (!strcmp(mode, "vfork-exit") || !strcmp(mode, "vfork-exec")) ? vfork() : fork();
    if (child < 0) return 90;
    if (!child) {
        if (!strcmp(mode, "fork") || !strcmp(mode, "vfork-exit")) {
            checked_write(stage, "CHILD\n");
            _exit(0);
        }
        helper_exec();
    }
    checked_write(stage, "PARENT_RETURN\n");
    int status;
    if (waitpid(child, &status, 0) != child || !WIFEXITED(status) || WEXITSTATUS(status)) return 91;
    checked_write(stage, "COMPLETE\n");
    return 0;
}
