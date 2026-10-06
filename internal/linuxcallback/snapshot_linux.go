//go:build linux

// Package linuxcallback owns the installed Linux portal callback, not producer policy.
package linuxcallback

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/strictjson"
)

const ReaderMarker = "agent-notifications-linux-callback-reader-v1"

var ErrUnavailable = errors.New("linux_navigation_unavailable")

// Snapshot remains readable independently of the mutable ledger, journal and
// current setup. Readers and snapshots are retained until native lifetime is known.
type Snapshot struct {
	Version                          int
	InstallationID, ApplicationID    string
	Launcher, Executable             string
	LauncherSHA256, ExecutableSHA256 string
	ReleaseSHA256, ManifestSHA256    string
	Reader, ReaderSHA256, DataRoot   string
	Records                          string
}

func Digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func validKey(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == strings.ToLower(s)
}
func canonical(s string) bool {
	return filepath.IsAbs(s) && filepath.Clean(s) == s && !strings.ContainsAny(s, "\x00\r\n")
}
func (s Snapshot) Valid() bool {
	return s.Version == 1 && validKey(s.InstallationID) && s.ApplicationID == "org.agentnotifications.Callback"+s.InstallationID && canonical(s.Launcher) && canonical(s.Executable) && canonical(s.Records) && canonical(s.Reader) && canonical(s.DataRoot) && validKey(s.ReaderSHA256) && validKey(s.LauncherSHA256) && validKey(s.ExecutableSHA256) && validKey(s.ReleaseSHA256) && validKey(s.ManifestSHA256)
}
func ThreadURI(thread string) (string, error) {
	if thread == "" || len(thread) > 256 || !utf8.ValidString(thread) {
		return "", ErrUnavailable
	}
	for _, r := range thread {
		if unicode.Is(unicode.Cc, r) {
			return "", ErrUnavailable
		}
	}
	// One opaque path segment, including IDs containing slashes, percent or dots.
	segment := url.PathEscape(thread)
	if thread == "." {
		segment = "%2E"
	}
	if thread == ".." {
		segment = "%2E%2E"
	}
	return "codex://threads/" + segment, nil
}
func privateInfo(path string, directory bool) (os.FileInfo, error) {
	info, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Getuid() || info.Mode()&os.ModeSymlink != 0 || (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) || info.Mode().Perm()&0077 != 0 {
		return nil, ErrUnavailable
	}
	return info, nil
}
func ReadOwned(path string, limit int64) ([]byte, error) {
	if !canonical(path) {
		return nil, ErrUnavailable
	}
	if _, e := privateInfo(filepath.Dir(path), true); e != nil {
		return nil, e
	}
	info, e := privateInfo(path, false)
	if e != nil || info.Size() > limit {
		return nil, ErrUnavailable
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer func() { _ = f.Close() }() // Read-only handle, with no buffered writes.
	got, e := f.Stat()
	if e != nil || !os.SameFile(info, got) {
		return nil, ErrUnavailable
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, ErrUnavailable
	}
	return b, nil
}
func decode(b []byte, v any) error {
	if strictjson.Validate(b, strictjson.Budget{Bytes: 16384, Depth: 8, Entries: 128}) != nil {
		return ErrUnavailable
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return ErrUnavailable
	}
	return nil
}
func Load(binding notification.LinuxBinding) (Snapshot, error) {
	var s Snapshot
	b, e := ReadOwned(binding.SnapshotPath, 16384)
	if e != nil || Digest(b) != binding.SHA256 || decode(b, &s) != nil || !s.Valid() {
		return s, ErrUnavailable
	}
	if s.Records != filepath.Join(filepath.Dir(binding.SnapshotPath), "records") || s.Reader != filepath.Join(filepath.Dir(binding.SnapshotPath), "reader") {
		return s, ErrUnavailable
	}
	return s, nil
}
