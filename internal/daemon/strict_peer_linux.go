//go:build linux

package daemon

import (
	"net"
	"os"
	"syscall"

	"github.com/777genius/agent-notifications/internal/notification"
)

// strictPeer owns the connected Unix socket: Linux supplies credentials from
// the kernel, not from a request body or a mutable environment variable.
func strictPeer(conn net.Conn) (int, bool) {
	unix, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, false
	}
	raw, err := unix.SyscallConn()
	if err != nil {
		return 0, false
	}
	var credentials *syscall.Ucred
	err = raw.Control(func(fd uintptr) {
		credentials, err = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if err != nil || credentials == nil || credentials.Uid != uint32(os.Geteuid()) || credentials.Pid <= 1 {
		return 0, false
	}
	return int(credentials.Pid), true
}

func strictPeerMatchesOrigin(conn net.Conn, target *notification.TerminalTarget) bool {
	if target == nil || target.OwnerPID <= 1 || target.OwnerBirth == 0 {
		return false
	}
	pid, ok := strictPeer(conn)
	return ok && processAncestor(pid, target.OwnerPID, target.OwnerBirth)
}

// processAncestor verifies an actual kernel process lineage, including the
// terminal process birth tick. A sibling process with the same UID or cwd is
// not authorized to invent a notification for a different terminal.
func processAncestor(pid, ancestor int, birth uint64) bool {
	seen := make(map[int]bool)
	for depth := 0; pid > 1 && depth < 80 && !seen[pid]; depth++ {
		seen[pid] = true
		info, err := procIdentity(pid)
		if err != nil {
			return false
		}
		if pid == ancestor {
			return info.start == birth
		}
		pid = info.parent
	}
	return false
}
