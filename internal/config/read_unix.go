//go:build linux || darwin

package config

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

// ReadFileSnapshot reads a single regular-file inode. Atomic POSIX publication
// means cooperating writers cannot expose partially written JSON.
func ReadFileSnapshot(path string, limit int) (Snapshot, error) {
	return readFileSnapshotUnmanaged(path, limit)
}

func resolveForRead(env EnvSnapshot) (Selection, error) { return Resolve(env) }

func openSnapshotFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func normalizeExistingFinal(path string) (string, error) { return path, nil }

// POSIX mode diagnostics are already available from the resolver's Lstat.
func nativePermissionDiagnostics(string) []Diagnostic { return nil }

func readSnapshotContext(ctx context.Context, path string, limit int) (Snapshot, error) {
	if ctx.Err() != nil {
		return Snapshot{}, &Error{Code: ConfigLockTimeout, Path: path}
	}
	return ReadFileSnapshot(path, limit)
}
func portableGuardUnavailable(err error) bool {
	return os.IsPermission(err) || errors.Is(err, unix.EROFS)
}
