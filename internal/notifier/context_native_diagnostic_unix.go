//go:build darwin || linux

package notifier

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func contextNativeDiagnosticSignal(err error) (*bool, *int) {
	var exited *exec.ExitError
	if !errors.As(err, &exited) {
		return nil, nil
	}
	status, ok := exited.Sys().(syscall.WaitStatus)
	if !ok {
		return nil, nil
	}
	signaled := status.Signaled()
	if !signaled {
		return &signaled, nil
	}
	signal := int(status.Signal())
	if signal <= 0 || signal > 127 {
		return nil, nil
	}
	return &signaled, &signal
}

// Work-ref-only observation of the original reader. No caller-selected output
// path, directory creation, overwrite, retry, fsync, or raw native data.
func contextNativeDiagnosticSink() func(contextNativeDiagnostic) {
	if os.Getenv("AGENT_NOTIFICATIONS_TEST_NATIVE_READER") != "1" {
		return nil
	}
	temporary := os.Getenv("TMPDIR")
	if !filepath.IsAbs(temporary) || filepath.Clean(temporary) != temporary || len(temporary) > 4096 {
		return nil
	}
	physical, err := filepath.EvalSymlinks(temporary)
	if err != nil || physical != temporary || filepath.Base(temporary) != "tmp" {
		return nil
	}
	caseRoot := filepath.Dir(temporary)
	switch filepath.Base(caseRoot) {
	case "case-completion", "case-question", "case-sound-off", "case-click-off", "case-desktop-off", "case-stale-origin":
	default:
		return nil
	}
	if name := filepath.Base(filepath.Dir(caseRoot)); !strings.HasPrefix(name, "TEST-pi-installed-macos-") || len(name) <= len("TEST-pi-installed-macos-") {
		return nil
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(temporary, "/"), "/")
	for index, component := range parts {
		next, openErr := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
		_ = unix.Close(fd)
		if openErr != nil {
			return nil
		}
		fd = next
		if index >= len(parts)-3 {
			var info unix.Stat_t
			if unix.Fstat(fd, &info) != nil || info.Mode&unix.S_IFMT != unix.S_IFDIR || info.Mode&07777 != 0700 || info.Uid != uint32(os.Geteuid()) {
				_ = unix.Close(fd)
				return nil
			}
		}
	}
	// Darwin MNT_LOCAL=0x1000 (x/sys's Darwin constant is not built on Linux).
	if runtime.GOOS == "darwin" {
		var volume unix.Statfs_t
		if unix.Fstatfs(fd, &volume) != nil || uint64(volume.Flags)&0x1000 == 0 {
			_ = unix.Close(fd)
			return nil
		}
	}
	return func(value contextNativeDiagnostic) {
		defer unix.Close(fd)
		data, encodeErr := json.Marshal(value)
		if encodeErr != nil || len(data) > 1024 {
			return
		}
		out, openErr := unix.Openat(fd, "native-context-reader.json", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
		if openErr != nil {
			return
		}
		defer unix.Close(out)
		var info unix.Stat_t
		if unix.Fstat(out, &info) != nil || info.Mode&unix.S_IFMT != unix.S_IFREG || info.Mode&07777 != 0600 || info.Uid != uint32(os.Geteuid()) || info.Nlink != 1 {
			return
		}
		_, _ = unix.Write(out, data) // One bounded best-effort write, never a send wait.
	}
}
