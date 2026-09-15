package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReadDocumentSelectedErrorNeverFallsBack(t *testing.T) {
	l := "/h/.claude/claude-notifications-go/config.json"
	n := "/h/.config/agent-notifications/config.json"
	for _, data := range []string{"", `null`, `{"schemaVersion":3}`, `{"notifications":{"desktop":{"volume":3}}}`} {
		r := ReadRequest{Env: fakeEnv("linux", map[string]string{"HOME": "/h"}, map[string]fs.FileMode{l: 0, n: 0}), ReadSnapshot: func(p string, limit int) (Snapshot, error) {
			if p != l {
				t.Fatal("fallback read")
			}
			return Snapshot{[]byte(data), p}, nil
		}}
		if _, _, err := ReadDocument(r); err == nil {
			t.Fatal("accepted selected error")
		}
	}
}
func TestReadDocumentMissingAndHistory(t *testing.T) {
	r := ReadRequest{Env: fakeEnv("linux", map[string]string{"HOME": "/h"}, nil), ReadSnapshot: func(p string, limit int) (Snapshot, error) { t.Fatal("unexpected read"); return Snapshot{}, nil }}
	d, c, err := ReadDocument(r)
	if err != nil || c.Notifications.Webhook.Preset != "slack" || d.SchemaVersion() != 1 {
		t.Fatalf("fresh %v", err)
	}
	r.Env.Vars[OverrideEnv] = "/explicit"
	if _, _, err := ReadDocument(r); err == nil {
		t.Fatal("explicit missing became defaults")
	}
	delete(r.Env.Vars, OverrideEnv)
	r.Legacy.Candidates = []HistoricalCandidate{{Path: "/fixture/bundle"}}
	r.ReadSnapshot = func(p string, limit int) (Snapshot, error) { return Snapshot{[]byte(`{"future":"private"}`), p}, nil }
	if _, _, err := ReadDocument(r); err == nil {
		t.Fatal("unknown history ignored")
	}
	r.Legacy.Candidates[0].TrustedBaseline = []byte(`{"future":"private"}`)
	if _, _, err := ReadDocument(r); err != nil {
		t.Fatal(err)
	}
}
func TestReadDocumentDisappearanceIsChanged(t *testing.T) {
	e := fakeEnv("linux", map[string]string{OverrideEnv: "/fixture"}, map[string]fs.FileMode{"/fixture": 0})
	_, _, err := ReadDocument(ReadRequest{Env: e, ReadSnapshot: func(string, int) (Snapshot, error) { return Snapshot{}, fs.ErrNotExist }})
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != ConfigChanged {
		t.Fatalf("%v", err)
	}
}
func TestReadFileSnapshotAndAncestorIdentity(t *testing.T) {
	setTestHome(t, t.TempDir())
	root := t.TempDir()
	p := filepath.Join(root, "config.json")
	raw := []byte("{\"future\":9007199254740993001}\n")
	if err := os.WriteFile(p, raw, 0644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(p)
	snap, err := ReadFileSnapshot(p, MaxDocumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	if string(snap.Bytes) != string(raw) {
		t.Fatal("snapshot bytes")
	}
	after, _ := os.Stat(p)
	if before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("read changed file")
	}
	if _, err := ReadFileSnapshot(root, MaxDocumentBytes); err == nil {
		t.Fatal("directory read")
	}
	if _, err := ReadFileSnapshot(p, 1); err == nil {
		t.Fatal("read limit")
	}
	if runtime.GOOS == "windows" {
		return
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	got, err := canonicalParent(filepath.Join(alias, "missing", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	physicalRoot, _ := filepath.EvalSymlinks(root)
	if got != filepath.Join(physicalRoot, "missing", "config.json") {
		t.Fatalf("identity %q", got)
	}
	linked := filepath.Join(root, "linked.json")
	if err := os.Symlink(p, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFileSnapshot(linked, MaxDocumentBytes); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(root, "dangling.json")
	if err := os.Symlink(filepath.Join(root, "missing"), dangling); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFileSnapshot(dangling, MaxDocumentBytes); err == nil {
		t.Fatal("dangling accepted")
	}
}

func TestExplicitReadCannotTouchOutsideCanary(t *testing.T) {
	root := t.TempDir()
	fixture := filepath.Join(root, "config.json")
	outside := filepath.Join(t.TempDir(), "canary.json")
	if err := os.WriteFile(outside, []byte("secret-canary"), 0600); err != nil {
		t.Fatal(err)
	}
	// The injected filesystem denies every path except the selected fixture,
	// including valid automatic candidates and the real outside canary.
	e := fakeEnv(runtime.GOOS, map[string]string{OverrideEnv: fixture, "HOME": outside, "USERPROFILE": outside, "APPDATA": outside, "XDG_CONFIG_HOME": outside}, nil)
	e.Lstat = func(p string) (fs.FileInfo, error) {
		if p != fixture {
			t.Fatalf("outside access %q", p)
		}
		return fakeEntry{p, 0}, nil
	}
	r := ReadRequest{Env: e, Legacy: LegacyContext{Candidates: []HistoricalCandidate{{Path: outside}}}, ReadSnapshot: func(p string, limit int) (Snapshot, error) {
		if p != fixture {
			t.Fatalf("outside read %q", p)
		}
		return Snapshot{[]byte(`{}`), p}, nil
	}}
	if _, _, err := ReadDocument(r); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(outside)
	if err != nil || info.Size() != 13 {
		t.Fatal("canary modified")
	}
}

func TestSelectedDisappearsAfterSnapshotNeverDefaults(t *testing.T) {
	l := "/h/.claude/claude-notifications-go/config.json"
	present := true
	e := fakeEnv("linux", map[string]string{"HOME": "/h"}, nil)
	e.Lstat = func(p string) (fs.FileInfo, error) {
		if present && p == l {
			return fakeEntry{p, 0}, nil
		}
		return nil, fs.ErrNotExist
	}
	_, _, err := ReadDocument(ReadRequest{Env: e, ReadSnapshot: func(p string, limit int) (Snapshot, error) { present = false; return Snapshot{[]byte(`{}`), p}, nil }})
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != ConfigChanged {
		t.Fatalf("disappearance became defaults: %v", err)
	}
}
