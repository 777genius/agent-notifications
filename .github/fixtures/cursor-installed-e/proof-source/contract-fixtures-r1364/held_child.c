/* Isolated TEST syscall effects. No product/helper code or syscall records. */
#define _GNU_SOURCE
#include <unistd.h>
#include <sys/prctl.h>
#include <sys/wait.h>
#include <sys/syscall.h>
#include <stdlib.h>
#include <stdio.h>
#include <string.h>
#include <pthread.h>
#include <fcntl.h>

static void *thread_effect(void *unused) {
    (void) unused;
    if (write(1, "TEST_CHILD_RAN\n", 15) != 15) _exit(14);
    return NULL;
}
static void allocation_receipt(const char *mode) {
    char raw[4096];
    FILE *statfile = fopen("/proc/self/stat", "r");
    if (!statfile || !fgets(raw, sizeof(raw), statfile)) _exit(17);
    if (fclose(statfile)) _exit(17);
    char *tail = strrchr(raw, ')');
    if (!tail) _exit(17);
    tail += 2;
    char *save = NULL, *token = strtok_r(tail, " ", &save);
    for (int index = 0; index < 19 && token; ++index) token = strtok_r(NULL, " ", &save);
    if (!token) _exit(17);
    unsigned long long birth = strtoull(token, NULL, 10);
    FILE *receipt = fopen("fixture-allocation.json", "wx");
    if (!receipt) _exit(17);
    if (fprintf(receipt, "{\"pid\":%ld,\"birth\":%llu,\"mode\":\"%s\",\"prctlReady\":true}\n",
                (long)getpid(), birth, mode) < 0 || fclose(receipt)) _exit(17);
}
int main(int argc, char **argv) {
    char go;
    if (argc != 2) return 9;
    FILE *pidfile = fopen("fixture.pid", "wx");
    if (pidfile) { fprintf(pidfile, "%ld\n", (long)getpid()); fclose(pidfile); }
    if (prctl(PR_SET_PTRACER, PR_SET_PTRACER_ANY, 0, 0, 0)) return 10;
    allocation_receipt(argv[1]);
    if (!strcmp(argv[1], "stall-ready")) { for (;;) pause(); }
    if (!strcmp(argv[1], "ready-eof")) return 0;
    puts(!strcmp(argv[1], "bad-ready") ? "TEST_BAD" : "TEST_READY");
    fflush(stdout);
    if (read(0, &go, 1) != 1) return 11;
    if (!strcmp(argv[1], "thread")) {
        pthread_t tid;
        if (pthread_create(&tid, NULL, thread_effect, NULL)) return 15;
        return pthread_join(tid, NULL) ? 16 : 0;
    }
    pid_t child = argv[1][0] == 'f' ? fork() : vfork();
    if (child < 0) return 12;
    if (!child) {
        if (write(1, "TEST_CHILD_RAN\n", 15) != 15) _exit(14);
        if (argv[1][0] == 'e') execl("/usr/bin/true", "true", NULL);
        _exit(0);
    }
    int status;
    if (waitpid(child, &status, 0) != child || status) return 13;
    return 0;
}
