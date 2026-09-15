package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"syscall"
	"testing"
	"time"
)

func TestPathErrorPrivateCauseMetadata(t *testing.T) {
	err := pathErrorAt("/safe/path", "resolve-lstat", syscall.Errno(32))
	var ce *Error
	if !errors.As(err, &ce) || ce.causeStage != "resolve-lstat" || ce.causeErrno != 32 {
		t.Fatalf("cause metadata not retained: %#v", ce)
	}
	raw, marshalErr := json.Marshal(ce)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if string(raw) != `{"Code":"ConfigInvalid","Path":"/safe/path","Offset":0}` {
		t.Fatalf("private cause metadata escaped: %s", raw)
	}
	if got := ce.Error(); got != `ConfigInvalid: path="/safe/path" offset=0` {
		t.Fatalf("public error changed: %s", got)
	}
}

type fakeEntry struct {
	name string
	mode fs.FileMode
}

func (f fakeEntry) Name() string               { return f.name }
func (f fakeEntry) Size() int64                { return 0 }
func (f fakeEntry) Mode() fs.FileMode          { return f.mode }
func (f fakeEntry) ModTime() time.Time         { return time.Time{} }
func (f fakeEntry) IsDir() bool                { return f.mode.IsDir() }
func (f fakeEntry) Sys() any                   { return nil }
func (f fakeEntry) Type() fs.FileMode          { return f.mode.Type() }
func (f fakeEntry) Info() (fs.FileInfo, error) { return f, nil }
func fakeEnv(os string, vars map[string]string, entries map[string]fs.FileMode) EnvSnapshot {
	return EnvSnapshot{GOOS: os, Vars: vars, Lstat: func(p string) (fs.FileInfo, error) {
		if m, ok := entries[p]; ok {
			return fakeEntry{p, m}, nil
		}
		return nil, fs.ErrNotExist
	}, ReadDir: func(p string) ([]fs.DirEntry, error) { return nil, fs.ErrNotExist }}
}
func TestResolverSelection(t *testing.T) {
	l := "/home/.claude/claude-notifications-go/config.json"
	n := "/home/.config/agent-notifications/config.json"
	for _, mode := range []fs.FileMode{0, fs.ModeDir, fs.ModeSymlink, fs.ModeNamedPipe, fs.ModeSocket} {
		for _, bits := range []int{0, 1, 2, 3} {
			entries := map[string]fs.FileMode{}
			if bits&1 != 0 {
				entries[l] = mode
			}
			if bits&2 != 0 {
				entries[n] = mode
			}
			e := fakeEnv("linux", map[string]string{"HOME": "/home"}, entries)
			s, err := Resolve(e)
			if err != nil {
				t.Fatal(err)
			}
			want := n
			if bits&1 != 0 {
				want = l
			}
			if s.Path != want || s.Exists != (bits != 0) {
				t.Fatalf("selection %+v", s)
			}
			if bits == 3 {
				found := false
				for _, d := range s.Diagnostics {
					found = found || d.Code == ConfigMultipleCandidates
				}
				if !found {
					t.Fatal("missing multiple warning")
				}
			}
		}
	}
}
func TestExplicitBypassesHomeAndOtherCandidates(t *testing.T) {
	e := fakeEnv("linux", map[string]string{OverrideEnv: "/fixture/$ literal ü.json"}, nil)
	e.Lstat = func(p string) (fs.FileInfo, error) {
		if p != "/fixture/$ literal ü.json" {
			t.Fatal("read outside fixture")
		}
		return fakeEntry{p, 0}, nil
	}
	s, err := Resolve(e)
	if err != nil || s.Source != "explicit" {
		t.Fatalf("%+v %v", s, err)
	}
	for _, v := range []string{"", " ", "relative", "~/config", "/bad\x00"} {
		e.Vars[OverrideEnv] = v
		if _, err := Resolve(e); err == nil {
			t.Fatalf("accepted %q", v)
		}
	}
}
func TestOSPaths(t *testing.T) {
	for _, tt := range []struct {
		os   string
		vars map[string]string
		want string
	}{
		{"linux", map[string]string{"HOME": "/h", "XDG_CONFIG_HOME": "relative"}, "/h/.config/agent-notifications/config.json"},
		{"linux", map[string]string{"HOME": "/h", "XDG_CONFIG_HOME": "/x"}, "/x/agent-notifications/config.json"},
		{"darwin", map[string]string{"HOME": "/h", "XDG_CONFIG_HOME": "/ignored"}, "/h/Library/Application Support/agent-notifications/config.json"},
		{"windows", map[string]string{"USERPROFILE": `C:\Users\fixture`, "APPDATA": `C:\Roaming`, "HOME": "/ignored"}, `C:\Roaming\agent-notifications\config.json`},
	} {
		s, err := Resolve(fakeEnv(tt.os, tt.vars, nil))
		if err != nil || s.Path != tt.want {
			t.Fatalf("%s: %+v %v", tt.os, s, err)
		}
	}
	for _, p := range []string{`C:foo`, `\foo`, `\\?\C:\foo`, `\\.\foo`, `C:\foo:stream`, `C:\NUL`, `C:\foo.`, `C:\foo `} {
		if validAbsolute("windows", p) {
			t.Errorf("accepted %q", p)
		}
	}
	for _, p := range []string{`C:\space ü\$x.json`, `\\server\share\config.json`} {
		if !validAbsolute("windows", p) {
			t.Errorf("rejected %q", p)
		}
	}
}
func TestRecoveryAndErrors(t *testing.T) {
	e := fakeEnv("linux", map[string]string{"HOME": "/h"}, nil)
	e.ReadDir = func(p string) ([]fs.DirEntry, error) {
		return []fs.DirEntry{fakeEntry{"config.json.backup-own", 0}}, nil
	}
	if _, err := Resolve(e); err == nil {
		t.Fatal("ignored recovery")
	}
	e.ReadDir = func(p string) ([]fs.DirEntry, error) {
		return []fs.DirEntry{fakeEntry{"config.json.lock", 0}, fakeEntry{"other.backup-own", 0}}, nil
	}
	if _, err := Resolve(e); err != nil {
		t.Fatal(err)
	}
	e.Lstat = func(p string) (fs.FileInfo, error) { return nil, fs.ErrPermission }
	if _, err := Resolve(e); err == nil {
		t.Fatal("ignored permission")
	}
}

func TestUnselectedNeutralErrorsAreWarnings(t *testing.T) {
	l := "/h/.claude/claude-notifications-go/config.json"
	e := fakeEnv("linux", map[string]string{"HOME": "/h"}, nil)
	e.Lstat = func(p string) (fs.FileInfo, error) {
		if p == l {
			return fakeEntry{p, 0}, nil
		}
		return nil, fs.ErrPermission
	}
	s, err := Resolve(e)
	if err != nil || s.Path != l || len(s.Diagnostics) != 1 || s.Diagnostics[0].Code != ConfigPermissionDenied {
		t.Fatalf("%+v %v", s, err)
	}
	e = fakeEnv("windows", map[string]string{"USERPROFILE": `C:\h`}, map[string]fs.FileMode{`C:\h\.claude\claude-notifications-go\config.json`: 0})
	s, err = Resolve(e)
	if err != nil || s.Source != "legacy" {
		t.Fatalf("legacy needs APPDATA: %+v %v", s, err)
	}
	e = fakeEnv("linux", map[string]string{"XDG_CONFIG_HOME": "/x"}, nil)
	if _, err = Resolve(e); err == nil {
		t.Fatal("missing HOME accepted")
	}
}

func TestWindowsCleanCannotEscapeVolume(t *testing.T) {
	for _, tt := range []struct{ in, want string }{{`C:\..\..\file`, `C:\file`}, {`\\server\share\..\..\file`, `\\server\share\file`}, {`C:\a\..\file`, `C:\file`}} {
		if got := cleanPath("windows", tt.in); got != tt.want {
			t.Fatalf("%q -> %q want %q", tt.in, got, tt.want)
		}
	}
}

func TestRecoveryArtifactNamespace(t *testing.T) {
	for _, name := range []string{"config.json.backup-123", "config.json.tmp-123", "config.json.tmp", "config-123.json.tmp"} {
		e := fakeEnv("linux", map[string]string{OverrideEnv: "/fixture/config.json"}, nil)
		e.ReadDir = func(string) ([]fs.DirEntry, error) { return []fs.DirEntry{fakeEntry{name, fs.ModeSymlink}}, nil }
		if _, err := Resolve(e); err == nil {
			t.Fatalf("ignored %s", name)
		}
	}
	e := fakeEnv("linux", map[string]string{OverrideEnv: "/fixture/custom.json"}, nil)
	e.ReadDir = func(string) ([]fs.DirEntry, error) {
		return []fs.DirEntry{fakeEntry{"config-123.json.tmp", 0}, fakeEntry{"config.json.backup-1", 0}}, nil
	}
	if _, err := Resolve(e); err != nil {
		t.Fatal("unrelated file artifacts blocked explicit", err)
	}
}

func TestSelectionChangesUseBoundedReadRetry(t *testing.T) {
	// Each resolution alternates between existing L and existing N. No snapshot
	// may be returned under a selection which changed while it was read.
	l := "/h/.claude/claude-notifications-go/config.json"
	n := "/h/.config/agent-notifications/config.json"
	rounds := 0
	reads := 0
	e := fakeEnv("linux", map[string]string{"HOME": "/h"}, nil)
	e.Lstat = func(p string) (fs.FileInfo, error) {
		if p == l {
			rounds++
			if rounds%2 == 1 {
				return fakeEntry{p, 0}, nil
			}
			return nil, fs.ErrNotExist
		}
		return fakeEntry{n, 0}, nil
	}
	_, _, err := ReadDocument(ReadRequest{Env: e, ReadSnapshot: func(p string, limit int) (Snapshot, error) { reads++; return Snapshot{[]byte(`{}`), p}, nil }})
	if err == nil || reads != 3 {
		t.Fatalf("retry bound: reads=%d err=%v", reads, err)
	}
}
