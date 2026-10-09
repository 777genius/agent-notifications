package installruntime

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func snapshotDiagnosticIsolation(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "CODEX_HOME", "CLAUDE_HOME", "CLAUDE_CONFIG_DIR", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "APPDATA", "LOCALAPPDATA", "TMPDIR", "TMP", "TEMP"} {
		dir := filepath.Join(root, key)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, dir)
	}
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", "")
	if err := os.Unsetenv("AGENT_NOTIFICATIONS_CONFIG"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "HOME", "sentinel"), []byte("fake user sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

type diagnosticNode struct {
	Mode     os.FileMode
	Size     int64
	Modified int64
	Hash     [32]byte
	Link     string
}

func diagnosticTree(t *testing.T, root string) map[string]diagnosticNode {
	t.Helper()
	nodes := map[string]diagnosticNode{}
	if err := filepath.WalkDir(root, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		st, err := os.Lstat(path)
		if err != nil {
			return err
		}
		node := diagnosticNode{Mode: st.Mode(), Size: st.Size(), Modified: st.ModTime().UnixNano()}
		if st.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			node.Hash = sha256.Sum256(data)
		}
		if st.Mode()&os.ModeSymlink != 0 {
			node.Link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		nodes[path] = node
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return nodes
}

func requireSnapshotDiagnostic(t *testing.T, err error, code, path string) {
	t.Helper()
	if err == nil {
		t.Fatal("invalid installation accepted")
	}
	wrapped := fmt.Errorf("outer context: %w", err)
	d := SnapshotDiagnosticFor(wrapped)
	if d.Code != code || d.Path != path || d.Action == "" {
		t.Fatalf("diagnostic=%+v error=%v", d, err)
	}
	var typed *SnapshotError
	if !errors.As(wrapped, &typed) || typed.Code != code || typed.Path != path || !errors.Is(wrapped, typed.Err) {
		t.Fatalf("cause lost: %v", wrapped)
	}
	if typed.Error() != typed.Err.Error() {
		t.Fatal("error text changed")
	}
}

func TestSnapshotDiagnosticManagedFiles(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(map[bool]string{true: "missing", false: "changed"}[missing], func(t *testing.T) {
			fakeUser := snapshotDiagnosticIsolation(t)
			ctx, r := request(t)
			// Use normal multi-character filenames, like the transaction fixtures'
			// "hook" and "payload". One-character names can produce a rename
			// information buffer below the Windows native API's minimum size.
			// Reverse request order still tests deterministic diagnostic selection.
			first, last := filepath.Join(r.RuntimeRoot, "a-hook"), filepath.Join(r.RuntimeRoot, "z-hook")
			r.Files = []File{{Path: last, Data: []byte("last"), Mode: 0600}, {Path: first, Data: []byte("first"), Mode: 0600}}
			l, err := Commit(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			code := "managed_file_changed"
			for _, path := range []string{last, first} {
				if missing {
					code = "managed_file_missing"
					err = os.Remove(path)
				} else {
					err = os.WriteFile(path, []byte("changed"), 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			before, userBefore := diagnosticTree(t, filepath.Dir(r.ControlRoot)), diagnosticTree(t, fakeUser)
			for i := 0; i < 50; i++ {
				s, err := ReadInstalledSnapshot(r.ControlRoot)
				requireSnapshotDiagnostic(t, err, code, first)
				if errors.Is(err, os.ErrNotExist) != missing {
					t.Fatalf("missing cause=%v", err)
				}
				if err.Error() != "installed file fingerprint mismatch: "+first {
					t.Fatalf("legacy text changed: %v", err)
				}
				if s.Ledger.Generation != l.Generation || s.Recovery || s.Enabled {
					t.Fatalf("bad failed observation: %+v", s)
				}
			}
			if !reflect.DeepEqual(before, diagnosticTree(t, filepath.Dir(r.ControlRoot))) || !reflect.DeepEqual(userBefore, diagnosticTree(t, fakeUser)) {
				t.Fatal("snapshot mutated state")
			}
		})
	}
}

func TestSnapshotDiagnosticControlLedgerPolicy(t *testing.T) {
	for _, category := range []string{"control", "ledger", "policy-generation", "policy", "unreadable-file"} {
		t.Run(category, func(t *testing.T) {
			fakeUser := snapshotDiagnosticIsolation(t)
			ctx, r := request(t)
			path := filepath.Join(r.RuntimeRoot, "payload")
			r.Files = []File{{Path: path, Data: []byte("payload"), Mode: 0600}}
			ledger, err := Commit(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			if category == "policy" {
				disabled := false
				r.PolicyEnabled, r.ExpectedGeneration = &disabled, &ledger.Generation
				r.Files = nil
				ledger, err = Commit(ctx, r)
				if err != nil {
					t.Fatal(err)
				}
			}
			code := ""
			switch category {
			case "control":
				code, path = "control_invalid", r.ControlRoot
				// Replace a fixture directory with a file, without changing permissions.
				if err := os.Rename(r.ControlRoot, r.ControlRoot+"-saved"); err != nil {
					t.Fatal(err)
				}
				err = os.WriteFile(r.ControlRoot, []byte("not a directory"), 0600)
			case "ledger":
				code, path = "ledger_invalid", filepath.Join(r.ControlRoot, "ownership.json")
				err = os.Truncate(path, 1)
			case "policy-generation":
				code, path = "policy_invalid", filepath.Join(r.ControlRoot, "policy-generation.json")
				err = os.Remove(path)
			case "policy":
				code, path = "policy_invalid", filepath.Join(r.ControlRoot, "agent-notifications.json")
				err = os.Truncate(path, 1)
			case "unreadable-file":
				code = "managed_file_unreadable"
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				err = os.Mkdir(path, 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, userBefore := diagnosticTree(t, filepath.Dir(r.ControlRoot)), diagnosticTree(t, fakeUser)
			s, err := ReadInstalledSnapshot(r.ControlRoot)
			requireSnapshotDiagnostic(t, err, code, path)
			if category == "ledger" || category == "control" {
				if s.Ledger.Generation != 0 {
					t.Fatal("invalid ledger reported as observed")
				}
			} else if s.Ledger.Generation != ledger.Generation {
				t.Fatal("valid ledger generation lost")
			}
			if s.Recovery {
				t.Fatal("invented recovery")
			}
			if !reflect.DeepEqual(before, diagnosticTree(t, filepath.Dir(r.ControlRoot))) || !reflect.DeepEqual(userBefore, diagnosticTree(t, fakeUser)) {
				t.Fatal("read mutated state")
			}
		})
	}
}

func TestSnapshotDiagnosticNative(t *testing.T) {
	skipUnsupportedNative(t)
	for _, category := range []string{"identity", "bytes", "attestation", "unreadable"} {
		t.Run(category, func(t *testing.T) {
			fakeUser := snapshotDiagnosticIsolation(t)
			ctx, r := request(t)
			source := nativeFixture(t)
			// Retained staging never executes the fixture helper.
			change, err := StageRetainedNative(ctx, r.ControlRoot, source)
			if err != nil {
				t.Fatal(err)
			}
			r.Native = change
			l, err := Commit(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			path := l.Native.Path
			code := ""
			switch category {
			case "identity":
				code = "native_identity_invalid"
				if err := os.Rename(path, path+"-saved"); err != nil {
					t.Fatal(err)
				}
				err = os.Mkdir(path, 0700)
			case "bytes":
				code = "native_bytes_changed"
				err = os.WriteFile(filepath.Join(path, "Contents", "MacOS", "terminal-notifier-modern"), []byte("changed"), 0755)
			case "unreadable":
				code = "native_unreadable"
				err = os.Symlink(source, filepath.Join(path, "foreign-link"))
			case "attestation":
				code = "native_attestation_invalid"
			}
			if err != nil {
				t.Fatal(err)
			}
			before, userBefore, sourceBefore := diagnosticTree(t, filepath.Dir(r.ControlRoot)), diagnosticTree(t, fakeUser), diagnosticTree(t, source)
			if category == "attestation" {
				// Exercise the same snapshot check with a corrupted in-memory copy of
				// the real committed record, without forging an ownership ledger.
				record := *l.Native
				record.Attestation = []byte("invalid private attestation")
				err = checkSnapshotNative(&record)
			} else {
				_, err = ReadInstalledSnapshot(r.ControlRoot)
			}
			requireSnapshotDiagnostic(t, err, code, path)
			if !reflect.DeepEqual(before, diagnosticTree(t, filepath.Dir(r.ControlRoot))) || !reflect.DeepEqual(userBefore, diagnosticTree(t, fakeUser)) || !reflect.DeepEqual(sourceBefore, diagnosticTree(t, source)) {
				t.Fatal("snapshot modified or probed native fixture")
			}
		})
	}
}

func TestSnapshotDiagnosticSafeUnknownAndBounds(t *testing.T) {
	if got := SnapshotDiagnosticFor(nil); got != (SnapshotDiagnostic{}) {
		t.Fatalf("nil=%+v", got)
	}
	secret := errors.New("credential and config body /private/path")
	d := SnapshotDiagnosticFor(secret)
	if d.Code != "snapshot_invalid" || d.Path != "" || strings.Contains(d.Action, "credential") {
		t.Fatalf("unknown leaked: %+v", d)
	}
	d = SnapshotDiagnosticFor(snapshotFailure("unrecognized-private-code", "secret", secret))
	if d.Code != "snapshot_invalid" || d.Path != "" {
		t.Fatalf("unknown code leaked: %+v", d)
	}
	const budget = 1024
	prefix := "quoted\"\n\x1b/"
	for _, tc := range []struct {
		name, path, want string
	}{
		{"invalid-UTF8", "before\xffafter", "before\uFFFDafter"},
		{"at-budget", strings.Repeat("a", budget), strings.Repeat("a", budget)},
		{"over-budget", strings.Repeat("a", budget+1), strings.Repeat("a", budget-3) + "..."},
		{"hostile-multibyte", prefix + strings.Repeat("é", 1000) + "\xff", prefix + strings.Repeat("é", (budget-3-len(prefix))/2) + "..."},
		{"invalid-UTF8-before-bound", "\xff" + strings.Repeat("é", 1000), "\uFFFD" + strings.Repeat("é", (budget-3-len("\uFFFD"))/2) + "..."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := SnapshotDiagnosticFor(snapshotFailure("managed_file_changed", tc.path, secret))
			if d.Code != "managed_file_changed" || d.Action == "" || d.Path != tc.want || len(d.Path) > budget || !utf8.ValidString(d.Path) {
				t.Fatalf("diagnostic=%+v want path=%q (%d bytes)", d, tc.want, len(tc.want))
			}
		})
	}
}
