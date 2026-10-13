#define _GNU_SOURCE
#include <errno.h>
#include <sched.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/prctl.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

/* TEST-only prerequisite observation; no notification or application launch. */
static int child(void *unused) {
    (void)unused;
    return 0;
}

static double monotonic(void) {
    struct timespec ts;
    if (clock_gettime(CLOCK_MONOTONIC, &ts)) exit(70);
    return ts.tv_sec + ts.tv_nsec / 1000000000.0;
}

int main(void) {
    const char *test = getenv("NAVIGATION_NAMESPACE_TEST");
    if (!test || strcmp(test, "1") || getuid() != 1000 ||
        access("/.dockerenv", F_OK) || prctl(PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0) != 1 ||
        prctl(PR_GET_SECCOMP, 0, 0, 0, 0) != 2) {
        fputs("explicit_restricted_TEST_container_required\n", stderr);
        return 64;
    }
    char *stack = malloc(1024 * 1024);
    if (!stack) return 70;
    struct sigaction disposition = {.sa_handler = SIG_DFL};
    sigemptyset(&disposition.sa_mask);
    if (sigaction(SIGCHLD, &disposition, NULL)) { free(stack); return 70; }
    errno = 0;
    pid_t pid = clone(child, stack + 1024 * 1024, CLONE_NEWUSER | SIGCHLD, NULL);
    int clone_errno = pid < 0 ? errno : 0;
    int status = 0, collected = 0, timed_out = 0, wait_errno = 0, kill_errno = 0;
    if (pid >= 0) {
        double deadline = monotonic() + 3;
        for (;;) {
            pid_t waited = waitpid(pid, &status, WNOHANG);
            if (waited == pid) { collected = 1; break; }
            if (waited < 0 && errno != EINTR) { wait_errno = errno; break; }
            if (monotonic() >= deadline) { timed_out = 1; break; }
            struct timespec pause = {.tv_sec = 0, .tv_nsec = 10000000};
            nanosleep(&pause, NULL);
        }
        if (!collected && wait_errno != ECHILD) {
            if (kill(pid, SIGKILL)) kill_errno = errno;
            double stop_deadline = monotonic() + 2;
            do {
                pid_t waited = waitpid(pid, &status, WNOHANG);
                if (waited == pid) { collected = 1; break; }
                if (waited < 0 && errno != EINTR) { if (!wait_errno) wait_errno = errno; break; }
                struct timespec pause = {.tv_sec = 0, .tv_nsec = 10000000};
                nanosleep(&pause, NULL);
            } while (monotonic() < stop_deadline);
        }
    }
    int succeeded = pid >= 0 && collected && !timed_out && !wait_errno && !kill_errno &&
        WIFEXITED(status) && WEXITSTATUS(status) == 0;
    printf("{\"scope\":\"restricted_TEST_user_namespace_prerequisite\","
        "\"cloneNewUserReturned\":%s,\"cloneErrno\":%d,\"childCollected\":%s,"
        "\"timedOut\":%s,\"waitErrno\":%d,\"killErrno\":%d,\"namespacePrerequisitePassed\":%s,"
        "\"clientSandboxQualified\":false,\"notificationAttempted\":false,"
        "\"applicationLaunchAttempted\":false}\n",
        pid >= 0 ? "true" : "false", clone_errno, collected ? "true" : "false",
        timed_out ? "true" : "false", wait_errno, kill_errno, succeeded ? "true" : "false");
    free(stack);
    return succeeded ? 0 : 1;
}
