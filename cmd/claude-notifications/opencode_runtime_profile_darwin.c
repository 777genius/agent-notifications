//go:build darwin && cgo

#include "opencode_runtime_profile_darwin.h"
#include <libproc.h>
#include <sys/proc_info.h>
#include <sys/proc.h>
#include <sys/sysctl.h>
#include <mach/vm_prot.h>
#include <string.h>
#include <limits.h>
#include <sys/event.h>
#include <unistd.h>
#include <fcntl.h>
#include <time.h>

int an_runtime_process(int pid, struct an_runtime_process *out, char *path, size_t capacity) {
 struct proc_bsdinfo b = {0};
 struct proc_archinfo a = {0};
 if (pid <= 0 || capacity < 2 || capacity > PROC_PIDPATHINFO_MAXSIZE) return 0;
 if (proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &b, sizeof(b)) != sizeof(b) ||
     proc_pidinfo(pid, PROC_PIDARCHINFO, 0, &a, sizeof(a)) != sizeof(a)) return 0;
 memset(path, 0, capacity);
 int n = proc_pidpath(pid, path, (uint32_t)capacity);
 if (n <= 0 || (size_t)n >= capacity || strnlen(path, capacity) != (size_t)n) return 0;
 if (b.pbi_status == SZOMB || b.pbi_pid != (uint32_t)pid || b.pbi_ppid == 0 || b.pbi_start_tvsec == 0 || b.pbi_start_tvusec >= 1000000) return 0;
 *out = (struct an_runtime_process){b.pbi_pid, b.pbi_ppid, (uint32_t)a.p_cputype,
  (uint32_t)a.p_cpusubtype, b.pbi_start_tvsec, b.pbi_start_tvusec};
 return 1;
}
int an_runtime_region(int pid, uint64_t address, struct an_runtime_region *out) {
 struct proc_regionwithpathinfo r = {0};
 if (proc_pidinfo(pid, PROC_PIDREGIONPATHINFO, address, &r, sizeof(r)) != sizeof(r)) return 0;
 *out = (struct an_runtime_region){r.prp_prinfo.pri_address, r.prp_prinfo.pri_size,
  (uint64_t)r.prp_vip.vip_vi.vi_stat.vst_dev, r.prp_vip.vip_vi.vi_stat.vst_ino,
  (r.prp_prinfo.pri_protection & VM_PROT_EXECUTE) != 0};
 return 1;
}
int an_runtime_args(int pid, unsigned char *out, size_t capacity, size_t *length) {
 int mib[3] = {CTL_KERN, KERN_PROCARGS2, pid};
 if (capacity != 4096 || pid <= 0) return 0;
 size_t n = capacity;
 // No size allocation query, ENOMEM retry, or environment-tail traversal.
 // Full capacity is ambiguous: XNU may have returned only the saved data tail.
 if (sysctl(mib, 3, out, &n, NULL, 0) != 0 || n >= capacity || n < sizeof(int)) return 0;
 *length = n;
 return 1;
}

// Retain an SDK process-event epoch so even exec of the SAME inode revokes the
// lease. Polling only snapshots of start time/vnode cannot detect that case.
int an_runtime_watch(int pid) {
 int fd = kqueue();
 if (fd < 0) return -1;
 struct kevent change;
 EV_SET(&change, (uintptr_t)pid, EVFILT_PROC, EV_ADD | EV_ENABLE,
        NOTE_EXEC | NOTE_EXIT, 0, NULL);
 struct timespec zero = {0, 0};
 if (fcntl(fd, F_SETFD, FD_CLOEXEC) != 0 ||
     kevent(fd, &change, 1, NULL, 0, &zero) != 0) {
  close(fd); return -1;
 }
 return fd;
}
int an_runtime_unchanged(int fd) {
 struct kevent event;
 struct timespec zero = {0, 0};
 return kevent(fd, NULL, 0, &event, 1, &zero) == 0;
}
