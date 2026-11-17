/* TEST-only observation; never grants OpenCode admission. */
#include <arpa/inet.h>
#include <errno.h>
#include <libproc.h>
#include <netinet/in.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/proc_info.h>
#include <sys/socket.h>
#include <unistd.h>

static long number(const char *s, long max) {
    char *end = NULL;
    errno = 0;
    long value = strtol(s, &end, 10);
    return errno || end == s || *end || value <= 0 || value > max ? -1 : value;
}
static int identity(int pid, struct proc_bsdinfo *info) {
    int got = proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, info, sizeof(*info));
    return got == sizeof(*info) && info->pbi_pid == (uint32_t)pid &&
        info->pbi_uid == geteuid() && info->pbi_start_tvsec > 0 && !(info->pbi_flags & PROC_FLAG_INEXIT);
}
static int same_identity(const struct proc_bsdinfo *a, const struct proc_bsdinfo *b) {
    return a->pbi_pid == b->pbi_pid && a->pbi_uid == b->pbi_uid &&
        a->pbi_start_tvsec == b->pbi_start_tvsec && a->pbi_start_tvusec == b->pbi_start_tvusec;
}
int main(int argc, char **argv) {
    if (argc == 3 && strcmp(argv[1], "--identity") == 0) {
        long query_pid = number(argv[2], 2147483647);
        struct proc_bsdinfo info = {0};
        if (query_pid < 0 || !identity((int)query_pid, &info)) return 2;
        printf("%llu %llu\n", (unsigned long long)info.pbi_start_tvsec, (unsigned long long)info.pbi_start_tvusec);
        return 0;
    }
    if (argc != 6) return 2;
    long pid = number(argv[1], 2147483647), server = number(argv[2], 65535), client = number(argv[3], 65535);
    long expected_sec = number(argv[4], 9223372036854775807L);
    char *end = NULL;
    errno = 0;
    long expected_usec = strtol(argv[5], &end, 10);
    if (pid < 0 || server < 0 || client < 0 || expected_sec < 0 || errno || end == argv[5] || *end || expected_usec < 0 || expected_usec >= 1000000) return 2;
    struct proc_bsdinfo before = {0}, after = {0};
    if (!identity((int)pid, &before)) return 2;
    if (before.pbi_start_tvsec != (uint64_t)expected_sec || before.pbi_start_tvusec != (uint64_t)expected_usec) return 1;
    enum { CAP = 65536 };
    struct proc_fdinfo *fds = calloc(CAP, sizeof(*fds));
    if (!fds) return 2;
    int len = proc_pidinfo((int)pid, PROC_PIDLISTFDS, 0, fds, CAP * (int)sizeof(*fds));
    if (len <= 0 || len >= CAP * (int)sizeof(*fds) || len % sizeof(*fds)) { free(fds); return 2; }
    int matched = 0;
    for (int i = 0; i < len / (int)sizeof(*fds); ++i) {
        if (fds[i].proc_fdtype != PROX_FDTYPE_SOCKET) continue;
        struct socket_fdinfo socket_info = {0};
        int got = proc_pidfdinfo((int)pid, fds[i].proc_fd, PROC_PIDFDSOCKETINFO, &socket_info, sizeof(socket_info));
        /* A changing descriptor table cannot establish complete observation. */
        if (got != sizeof(socket_info)) { free(fds); return 2; }
        struct socket_info *so = &socket_info.psi;
        if (so->soi_family != AF_INET || so->soi_protocol != IPPROTO_TCP || so->soi_kind != SOCKINFO_TCP) continue;
        struct tcp_sockinfo *tcp = &so->soi_proto.pri_tcp;
        struct in_sockinfo *in = &tcp->tcpsi_ini;
        if (tcp->tcpsi_state == TSI_S_ESTABLISHED && (in->insi_vflag & INI_IPV4) &&
            ntohs((uint16_t)in->insi_lport) == server && ntohs((uint16_t)in->insi_fport) == client &&
            ntohl(in->insi_laddr.ina_46.i46a_addr4.s_addr) == INADDR_LOOPBACK &&
            ntohl(in->insi_faddr.ina_46.i46a_addr4.s_addr) == INADDR_LOOPBACK) {
            struct socket_fdinfo again = {0};
            if (!so->soi_so || !so->soi_pcb ||
                proc_pidfdinfo((int)pid, fds[i].proc_fd, PROC_PIDFDSOCKETINFO, &again, sizeof(again)) != sizeof(again) ||
                again.psi.soi_so != so->soi_so || again.psi.soi_pcb != so->soi_pcb ||
                again.psi.soi_family != AF_INET || again.psi.soi_protocol != IPPROTO_TCP || again.psi.soi_kind != SOCKINFO_TCP ||
                again.psi.soi_proto.pri_tcp.tcpsi_state != TSI_S_ESTABLISHED) { free(fds); return 2; }
            struct in_sockinfo *check = &again.psi.soi_proto.pri_tcp.tcpsi_ini;
            if (check->insi_lport != in->insi_lport || check->insi_fport != in->insi_fport ||
                check->insi_laddr.ina_46.i46a_addr4.s_addr != in->insi_laddr.ina_46.i46a_addr4.s_addr ||
                check->insi_faddr.ina_46.i46a_addr4.s_addr != in->insi_faddr.ina_46.i46a_addr4.s_addr) { free(fds); return 2; }
            ++matched;
        }
    }
    free(fds);
    if (!identity((int)pid, &after) || !same_identity(&before, &after)) return 2;
    if (matched != 1) return 1;
    puts("TEST_SOCKET_PAIR_MATCH");
    return 0;
}
