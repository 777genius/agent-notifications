//go:build linux || darwin

package config

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreSelectionOrderAndAliases(t *testing.T) {
	for _, first := range []string{"legacy", "neutral"} {
		t.Run(first, func(t *testing.T) {
			env := storeEnv(t)
			explicit := env
			explicit.Vars = map[string]string{}
			for k, v := range env.Vars {
				explicit.Vars[k] = v
			}
			legacy := filepath.Join(env.Vars["HOME"], ".claude", "claude-notifications-go", "config.json")
			explicit.Vars[OverrideEnv] = legacy
			if first == "legacy" {
				if _, e := EnsureInitialized(context.Background(), InitRequest{Env: explicit}); e != nil {
					t.Fatal(e)
				}
				r, e := EnsureInitialized(context.Background(), InitRequest{Env: env})
				if e != nil || r.Changed || r.Selection.Source != "legacy" {
					t.Fatalf("%+v %v", r, e)
				}
			} else {
				r, e := EnsureInitialized(context.Background(), InitRequest{Env: env})
				if e != nil {
					t.Fatal(e)
				}
				if _, e = EnsureInitialized(context.Background(), InitRequest{Env: explicit}); e != nil {
					t.Fatal(e)
				}
				_, e = ApplyEdits(context.Background(), EditRequest{Env: env, ExpectRevision: r.Revision, Edits: Edits{Set: map[string]json.RawMessage{"/debug/benchmark": json.RawMessage("true")}}})
				var ce *Error
				if !errors.As(e, &ce) || ce.Code != ConfigConflict {
					t.Fatalf("retarget %v", e)
				}
			}
		})
	}
	env := storeEnv(t)
	r, e := EnsureInitialized(context.Background(), InitRequest{Env: env})
	if e != nil {
		t.Fatal(e)
	}
	alias := filepath.Join(env.Vars["HOME"], "alias")
	if e = os.Symlink(filepath.Dir(r.Selection.Path), alias); e != nil {
		t.Fatal(e)
	}
	env.Vars[OverrideEnv] = filepath.Join(alias, "config.json")
	next, e := EnsureInitialized(context.Background(), InitRequest{Env: env})
	if e != nil || next.Revision != r.Revision || next.Selection.Path != r.Selection.Path {
		t.Fatalf("alias identity %+v %v", next, e)
	}
}
func TestStoreUnsafeParentsAndLocks(t *testing.T) {
	for _, attack := range []string{"writable-parent", "target-symlink", "lock-symlink", "lock-hardlink"} {
		t.Run(attack, func(t *testing.T) {
			env := storeEnv(t)
			r, e := EnsureInitialized(context.Background(), InitRequest{Env: env})
			if e != nil {
				t.Fatal(e)
			}
			switch attack {
			case "writable-parent":
				if e = os.Chmod(filepath.Dir(r.Selection.Path), 0777); e != nil {
					t.Fatal(e)
				}
			case "target-symlink":
				if e = os.Remove(r.Selection.Path); e != nil {
					t.Fatal(e)
				}
				if e = os.Symlink(filepath.Join(t.TempDir(), "missing"), r.Selection.Path); e != nil {
					t.Fatal(e)
				}
			case "lock-symlink":
				if e = os.Remove(r.Selection.Path + ".lock"); e != nil {
					t.Fatal(e)
				}
				if e = os.Symlink(filepath.Join(t.TempDir(), "victim"), r.Selection.Path+".lock"); e != nil {
					t.Fatal(e)
				}
			case "lock-hardlink":
				if e = os.Link(r.Selection.Path+".lock", filepath.Join(filepath.Dir(r.Selection.Path), "lock-alias")); e != nil {
					t.Fatal(e)
				}
			}
			if _, e = ApplyEdits(context.Background(), EditRequest{Env: env, ExpectRevision: r.Revision, Edits: Edits{Set: map[string]json.RawMessage{"/debug/benchmark": json.RawMessage("true")}}}); e == nil {
				t.Fatal("accepted unsafe path")
			}
		})
	}
}
func TestStorePortableLockDeadlineAndParentSwap(t *testing.T) {
	env := storeEnv(t)
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(root, "portable.json")
	env.Vars = map[string]string{OverrideEnv: target}
	r, e := EnsureInitialized(context.Background(), InitRequest{Env: env})
	if e != nil {
		t.Fatal(e)
	}
	m, e := beginMutation(context.Background(), env)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, e = ApplyEdits(ctx, EditRequest{Env: env, ExpectRevision: r.Revision, Edits: Edits{Set: map[string]json.RawMessage{"/debug/benchmark": json.RawMessage("true")}}})
	var ce *Error
	if !errors.As(e, &ce) || ce.Code != ConfigLockTimeout {
		t.Fatalf("deadline %v", e)
	}
	m.close()
	parent, e := openStoreParent(filepath.Dir(target), false)
	if e != nil {
		t.Fatal(e)
	}
	defer parent.close()
	moved := filepath.Dir(target) + "-moved"
	if e = os.Rename(filepath.Dir(target), moved); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(moved) })
	if e = os.Mkdir(filepath.Dir(target), 0700); e != nil {
		t.Fatal(e)
	}
	if e = parent.verify(); e == nil {
		t.Fatal("parent exchange accepted")
	}
	if r.Selection.Path != target {
		t.Fatal("portable retargeted")
	}
}

func TestStoreReadOnlyInitAndPrivateEdit(t *testing.T) {
	env := storeEnv(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "readonly.json")
	if e := os.WriteFile(target, []byte("{}\n"), 0444); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(dir, 0500); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	env.Vars = map[string]string{OverrideEnv: target}
	before, e := os.Stat(target)
	if e != nil {
		t.Fatal(e)
	}
	r, e := EnsureInitialized(context.Background(), InitRequest{Env: env})
	if e != nil || r.Changed {
		t.Fatalf("read-only no-op %v", e)
	}
	after, e := os.Stat(target)
	if e != nil || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("no-op touched metadata")
	}
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 1 {
		t.Fatal("no-op created a sidecar")
	}
	if e = os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	edited, e := ApplyEdits(context.Background(), EditRequest{Env: env, ExpectRevision: r.Revision, Edits: Edits{Set: map[string]json.RawMessage{"/debug/benchmark": json.RawMessage("true")}}})
	if e != nil || !edited.Changed {
		t.Fatal(e)
	}
	after, e = os.Stat(target)
	if e != nil || after.Mode().Perm() != 0600 {
		t.Fatal("real edit was not private")
	}
}

func TestStoreRejectsForeignOwnedAncestor(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires chown privilege")
	}
	root := t.TempDir()
	foreign := filepath.Join(root, "foreign")
	target := filepath.Join(foreign, "owned")
	if e := os.MkdirAll(target, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Chown(foreign, 65534, 65534); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.Chown(foreign, 0, 0) })
	p, e := openStoreParent(target, false)
	if e == nil {
		p.close()
		t.Fatal("foreign owner can replace private boundary")
	}
	var ce *Error
	if !errors.As(e, &ce) || ce.Code != ConfigPermissionDenied {
		t.Fatal(e)
	}
}

func prepareTestRoot(string) error { return nil }

func TestStoreCaseInsensitiveGuardIdentity(t *testing.T) {
	env := storeEnv(t)
	home, err := filepath.EvalSymlinks(env.Vars["HOME"])
	if err != nil {
		t.Fatal(err)
	}
	guardPath := filepath.Join(home, ".agent-notifications-config.lock")
	if err := os.WriteFile(guardPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(guardPath)
	if err != nil {
		t.Fatal(err)
	}
	upper, err := os.Stat(filepath.Join(home, ".AGENT-NOTIFICATIONS-CONFIG.lock"))
	if os.IsNotExist(err) {
		t.Skip("fixture filesystem is case sensitive")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, upper) {
		t.Fatal("fixture lock identities differ")
	}
	env.Vars[OverrideEnv] = filepath.Join(home, ".AGENT-NOTIFICATIONS-CONFIG")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, err := EnsureInitialized(ctx, InitRequest{Env: env})
	if err != nil || !r.Changed {
		t.Fatalf("init: %+v %v", r, err)
	}
	r, err = ApplyEdits(ctx, EditRequest{Env: env, ExpectRevision: r.Revision, Edits: Edits{Set: map[string]json.RawMessage{"/debug/benchmark": json.RawMessage("true")}}})
	if err != nil || !r.Changed {
		t.Fatalf("edit: %+v %v", r, err)
	}
	after, err := os.Stat(guardPath)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("guard identity changed: %v", err)
	}
	p, err := openStoreParent(home, false)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	release, err := p.lock(ctx, filepath.Base(guardPath), false, false)
	if err != nil {
		t.Fatal(err)
	}
	release()
}
