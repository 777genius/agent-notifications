//go:build linux

package linuxcallback

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/777genius/agent-notifications/internal/notification"
)

// Both activation parsers receive only absolute fixed arguments. Unsupported
// paths fail setup rather than acquiring shell/desktop interpolation authority.
func RegistrationBytes(s Snapshot, b notification.LinuxBinding) ([]byte, []byte, error) {
	safe := func(p string) bool {
		for _, r := range p {
			if !(r == '/' || r == '-' || r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
				return false
			}
		}
		return canonical(p)
	}
	if !s.Valid() || !safe(s.Reader) || !safe(b.SnapshotPath) || !validKey(b.SHA256) {
		return nil, nil, ErrUnavailable
	}
	command := s.Reader + " internal-linux-callback --snapshot " + b.SnapshotPath + " --sha256 " + b.SHA256
	desktop := []byte("[Desktop Entry]\nType=Application\nName=Agent Notifications\nNoDisplay=true\nDBusActivatable=true\nExec=" + command + "\n")
	service := []byte("[D-BUS Service]\nName=" + s.ApplicationID + "\nExec=" + command + "\n")
	return desktop, service, nil
}
func CheckInstallation(b notification.LinuxBinding, s Snapshot) error {
	reader, e := ReadOwned(s.Reader, 32<<20)
	if e != nil || Digest(reader) != s.ReaderSHA256 {
		return ErrUnavailable
	}
	desktop, service, e := RegistrationBytes(s, b)
	if e != nil {
		return e
	}
	for path, want := range map[string][]byte{filepath.Join(s.DataRoot, "applications", s.ApplicationID+".desktop"): desktop, filepath.Join(s.DataRoot, "dbus-1", "services", s.ApplicationID+".service"): service} {
		// These standard XDG directories may be readable by others. Their files
		// remain private and every ancestor rejects links or cooperative writes.
		for p := filepath.Dir(path); ; p = filepath.Dir(p) {
			info, e := os.Lstat(p)
			if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
				return ErrUnavailable
			}
			st, ok := info.Sys().(*syscall.Stat_t)
			if !ok || (st.Uid != 0 && int(st.Uid) != os.Getuid()) {
				return ErrUnavailable
			}
			if p == "/" {
				break
			}
		}
		info, e := privateInfo(path, false)
		if e != nil || info.Size() != int64(len(want)) {
			return ErrUnavailable
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		actual, e := f.Stat()
		if e != nil || !os.SameFile(info, actual) {
			f.Close()
			return ErrUnavailable
		}
		got, e := io.ReadAll(io.LimitReader(f, int64(len(want))+1))
		f.Close()
		if e != nil || !bytes.Equal(got, want) {
			return ErrUnavailable
		}
	}
	return nil
}
