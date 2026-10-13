//go:build linux

package opencodeevent

import (
	"runtime"
	"strconv"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"golang.org/x/sys/unix"
)

type systemCounter struct{}

func (systemCounter) SampleCounter() (CounterSample, bool) {
	// Namespace membership is per thread; do not migrate between handle/tick.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	domain, ok := linuxTimeDomain()
	boot, sec, nsec, sampled := journal.LinuxBootSample(journal.TrustedBootIDPath)
	after, stable := linuxTimeDomain()
	var res unix.Timespec
	q, _ := qualityAllowance("linux-boottime")
	err := unix.ClockGetres(unix.CLOCK_BOOTTIME, &res)
	r, valid := clockNanoseconds(res.Sec, res.Nsec)
	wallErr := unix.ClockGetres(unix.CLOCK_REALTIME, &res)
	wallRes, wallValid := clockNanoseconds(res.Sec, res.Nsec)
	return CounterSample{boot, domain, "linux-boottime", sec, nsec},
		ok && sampled && stable && domain == after && err == nil && valid && r > 0 && r <= q && wallErr == nil && wallValid && wallRes > 0 && wallRes <= q
}

func linuxTimeDomain() (string, bool) {
	// Follow only the fixed kernel namespace handle, then verify nsfs/type.
	fd, err := unix.Open("/proc/thread-self/ns/time", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", false
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	var fs unix.Statfs_t
	nstype, err := unix.IoctlRetInt(fd, unix.NS_GET_NSTYPE)
	if err != nil || nstype != unix.CLONE_NEWTIME || unix.Fstatfs(fd, &fs) != nil ||
		fs.Type != unix.NSFS_MAGIC || unix.Fstat(fd, &st) != nil || st.Dev == 0 || st.Ino == 0 {
		return "", false
	}
	return "linux-time:" + strconv.FormatUint(uint64(st.Dev), 10) + ":" + strconv.FormatUint(st.Ino, 10), true
}
