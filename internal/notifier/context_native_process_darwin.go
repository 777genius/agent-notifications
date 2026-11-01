//go:build darwin && cgo

package notifier

/*
#cgo LDFLAGS: -lproc
#include <errno.h>
#include <libproc.h>
#include <signal.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
static int context_native_process(int pid, uint64_t *seconds, uint64_t *micros) {
 struct proc_bsdinfo info;
 if (pid <= 0 || proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, sizeof(info)) != sizeof(info) ||
     info.pbi_pid != pid || !info.pbi_start_tvsec || info.pbi_start_tvusec >= 1000000) return 0;
 *seconds = info.pbi_start_tvsec; *micros = info.pbi_start_tvusec;
 return 1;
}
// Signal only the exact process that acknowledged this invocation. A matching
// path or PID alone never establishes ownership of a LaunchServices app.
static int context_native_stop(int pid, uint64_t seconds, uint64_t micros, const char *executable) {
 uint64_t got_seconds, got_micros;
 char path[PROC_PIDPATHINFO_MAXSIZE];
 if (!context_native_process(pid, &got_seconds, &got_micros)) return 0;
 if (got_seconds != seconds || got_micros != micros ||
     proc_pidpath(pid, path, sizeof(path)) <= 0 || strcmp(path, executable)) return -1;
 int result = kill(pid, SIGKILL);
 return result < 0 && errno == ESRCH ? 0 : result;
}
*/
import "C"

import (
	"errors"
	"os"
	"time"
	"unsafe"
)

func currentContextNativeProcess() (contextNativeProcess, error) {
	return readContextNativeProcess(os.Getpid())
}

func readContextNativeProcess(pid int) (contextNativeProcess, error) {
	var seconds, micros C.uint64_t
	if C.context_native_process(C.int(pid), &seconds, &micros) != 1 {
		return contextNativeProcess{}, errors.New("native_process_identity_unavailable")
	}
	return contextNativeProcess{pid, uint64(seconds), uint64(micros)}, nil
}

func stopContextNativeProcess(process contextNativeProcess, executable string) error {
	path := C.CString(executable)
	defer C.free(unsafe.Pointer(path))
	if C.context_native_stop(C.int(process.PID), C.uint64_t(process.StartSeconds), C.uint64_t(process.StartMicroseconds), path) < 0 {
		return errors.New("native_process_identity_changed")
	}
	// The app belongs to launchd, so waitpid cannot reap it. Observe disappearance
	// of the same kernel birth identity instead of waiting on the open launcher.
	until := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(until) {
		current, err := readContextNativeProcess(process.PID)
		if err != nil || current != process {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return errors.New("native_process_exit_unconfirmed")
}
