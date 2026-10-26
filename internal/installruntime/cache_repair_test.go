package installruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type cacheRepairFixture struct {
	ctx                                  context.Context
	root, control, old, sibling, current string
	ledger                               Ledger
}

func newCacheRepairFixture(t *testing.T) cacheRepairFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, ".claude", "plugins", "cache", "claude-notifications-go", "claude-notifications-go")
	f := cacheRepairFixture{ctx: ctx, root: root, control: filepath.Join(root, "control"), old: filepath.Join(cache, "1.46.1"), sibling: filepath.Join(cache, "1.47.0"), current: filepath.Join(cache, "1.49.4")}
	for i, consumer := range []string{"gemini-hooks", "portable:claude", "claude-hooks"} {
		runtime := []string{f.old, f.sibling, f.current}[i]
		files := []File{{Path: filepath.Join(runtime, "hook"), Data: []byte("exact retained hook"), Mode: 0700}}
		if i == 0 {
			files = append(files, File{Path: filepath.Join(runtime, "install.sh"), Data: []byte("unique cache payload\n" + WriterProtocolMarker), Mode: 0700})
		}
		f.ledger, err = Commit(ctx, Request{ControlRoot: f.control, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: consumer,
			Consumer: Consumer{Registration: consumer + " TEST registration", Commands: []string{filepath.Join(runtime, "hook")}}, Files: files})
		if err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f cacheRepairFixture) removeOld(t *testing.T) {
	t.Helper()
	for _, root := range []string{f.old, f.sibling} {
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCacheRepairSharedMissingRootsAndPayload(t *testing.T) {
	f := newCacheRepairFixture(t)
	policyBefore, err := os.ReadFile(filepath.Join(f.control, "agent-notifications.json"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	f.removeOld(t)
	before := diagnosticTree(t, f.root)
	plan, err := PreviewCacheRepair(f.ctx, f.control)
	if err != nil || len(plan.Paths) != 3 {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	if !reflect.DeepEqual(before, diagnosticTree(t, f.root)) {
		t.Fatal("preview changed installation")
	}
	after, err := RepairCache(f.ctx, f.control, plan)
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation != f.ledger.Generation+1 || !reflect.DeepEqual(after.Consumers, f.ledger.Consumers) ||
		!reflect.DeepEqual(after.Files, f.ledger.Files) || after.RuntimeRoot != f.ledger.RuntimeRoot || after.Enabled != f.ledger.Enabled {
		t.Fatal("repair changed ownership or policy decisions")
	}
	policyAfter, err := os.ReadFile(filepath.Join(f.control, "agent-notifications.json"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(policyBefore, policyAfter) {
		t.Fatal("repair rewrote policy bytes")
	}
	if _, err = ReadInstalledSnapshot(f.control); err != nil {
		t.Fatal(err)
	}
	healthy, err := PreviewCacheRepair(f.ctx, f.control)
	if err != nil || len(healthy.Paths) != 0 {
		t.Fatalf("healthy preview=%+v %v", healthy, err)
	}
}

func TestCacheRepairExactReplicaWithoutPayload(t *testing.T) {
	f := newCacheRepairFixture(t)
	if err := os.Remove(filepath.Join(f.old, "hook")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(f.control, "cache-repair.blobs")); err != nil {
		t.Fatal(err)
	}
	plan, err := PreviewCacheRepair(f.ctx, f.control)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = RepairCache(f.ctx, f.control, plan); err != nil {
		t.Fatal(err)
	}
	if got, err := Fingerprint(filepath.Join(f.old, "hook")); err != nil || got != f.ledger.Files[filepath.Join(f.old, "hook")] {
		t.Fatalf("replica restore=%+v %v", got, err)
	}
}

func TestCacheRepairRefusalsAreReadOnly(t *testing.T) {
	for _, scenario := range []string{"foreign-file", "source-changed", "generation", "policy", "foreign-owner", "no-replica", "payload-changed", "pending", "noncache-missing"} {
		t.Run(scenario, func(t *testing.T) {
			f := newCacheRepairFixture(t)
			if err := os.Remove(filepath.Join(f.old, "hook")); err != nil {
				t.Fatal(err)
			}
			plan, err := PreviewCacheRepair(f.ctx, f.control)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "foreign-file":
				err = os.WriteFile(filepath.Join(f.old, "hook"), []byte("user replacement"), 0700)
			case "source-changed":
				err = os.WriteFile(filepath.Join(f.current, "hook"), []byte("user source edit"), 0700)
			case "generation":
				_, err = Commit(f.ctx, Request{ControlRoot: f.control, RuntimeRoot: f.current, Owner: f.ledger.Owner, ConsumerID: "claude-hooks", RefreshOnly: true, Files: []File{{Path: filepath.Join(f.old, "hook"), Data: []byte("exact retained hook"), Mode: 0700}}})
				if err == nil {
					err = os.Remove(filepath.Join(f.old, "hook"))
				}
			case "policy":
				err = os.WriteFile(filepath.Join(f.control, "agent-notifications.json"), []byte(`{"schemaVersion":1,"enabled":true}`), 0600)
			case "foreign-owner":
				l := f.ledger
				l.Owner = "foreign"
				err = writeJSON(filepath.Join(f.control, "ownership.json"), l)
			case "no-replica", "payload-changed":
				if err = os.Remove(filepath.Join(f.old, "install.sh")); err != nil {
					t.Fatal(err)
				}
				if scenario == "no-replica" {
					err = os.RemoveAll(filepath.Join(f.control, "cache-repair.blobs"))
				} else {
					err = os.WriteFile(filepath.Join(f.control, "cache-repair.blobs", f.ledger.Files[filepath.Join(f.old, "install.sh")].SHA256), []byte("foreign payload"), 0600)
				}
			case "pending":
				err = os.WriteFile(filepath.Join(f.control, "transaction.json"), []byte("{}"), 0600)
			case "noncache-missing":
				l := f.ledger
				l.Files[filepath.Join(f.root, "noncache")] = identity([]byte("missing"), 0600)
				err = writeJSON(filepath.Join(f.control, "ownership.json"), l)
			}
			if err != nil {
				t.Fatal(err)
			}
			before := diagnosticTree(t, f.root)
			if _, err = RepairCache(f.ctx, f.control, plan); err == nil {
				t.Fatal("unsafe repair succeeded")
			}
			if !reflect.DeepEqual(before, diagnosticTree(t, f.root)) {
				t.Fatal("refusal mutated state")
			}
		})
	}
}

func TestCacheRepairJournalRecovery(t *testing.T) {
	for _, boundary := range []string{"transaction", "promotion", "ledger"} {
		t.Run(boundary, func(t *testing.T) {
			f := newCacheRepairFixture(t)
			f.removeOld(t)
			plan, err := PreviewCacheRepair(f.ctx, f.control)
			if err != nil {
				t.Fatal(err)
			}
			fault := errors.New("TEST interrupted repair")
			_, err = repairCache(f.ctx, f.control, plan, func(phase string) error {
				if phase == boundary || boundary == "promotion" && phase == "promotion:"+plan.Paths[0] {
					return fault
				}
				return nil
			})
			if !errors.Is(err, fault) {
				t.Fatalf("fault not reached: %v", err)
			}
			if _, err = PreviewCacheRepair(f.ctx, f.control); err == nil {
				t.Fatal("pending repair accepted")
			}
			l, err := Commit(f.ctx, Request{ControlRoot: f.control, RecoverOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			if l.Generation != f.ledger.Generation+1 || !reflect.DeepEqual(l.Files, f.ledger.Files) || !reflect.DeepEqual(l.Consumers, f.ledger.Consumers) {
				t.Fatal("redo changed repair decision")
			}
			if _, err = ReadInstalledSnapshot(f.control); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCacheRepairFreshAndCancelledReadOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	p, err := PreviewCacheRepair(context.Background(), root)
	if err != nil || len(p.Paths) != 0 {
		t.Fatalf("fresh preview=%+v %v", p, err)
	}
	if _, err = os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("fresh preview created root")
	}
	if err = os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "agent-notifications.json"), []byte(`{"schemaVersion":1,"enabled":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	p, err = PreviewCacheRepair(context.Background(), root)
	entries, readErr := os.ReadDir(root)
	if err != nil || len(p.Paths) != 0 || readErr != nil || len(entries) != 1 || entries[0].Name() != "agent-notifications.json" {
		t.Fatalf("config-only preview created or required installation locks: %+v %v %v", p, err, entries)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = PreviewCacheRepair(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
}

func TestCacheRepairSeedsRetainedFilesAndPrunesOnlyVerifiedPayloads(t *testing.T) {
	f := newCacheRepairFixture(t)
	dir := filepath.Join(f.control, "cache-repair.blobs")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	r := Request{ControlRoot: f.control, RuntimeRoot: f.current, Owner: f.ledger.Owner, ConsumerID: "claude-hooks", RefreshOnly: true}
	// An upgrade must back up unchanged old install.sh, not just its own files.
	l, err := Commit(f.ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	unique := filepath.Join(f.old, "install.sh")
	blob := filepath.Join(dir, l.Files[unique].SHA256)
	if _, err = os.Stat(blob); err != nil {
		t.Fatal("unchanged retained payload was not seeded", err)
	}
	foreign := filepath.Join(dir, identity([]byte("different bytes"), 0600).SHA256)
	if err = os.WriteFile(foreign, []byte("user data"), 0600); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(dir, "user-notes")
	if err = os.WriteFile(unknown, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	r.Files = []File{{Path: unique, Before: l.Files[unique], Remove: true}}
	if _, err = Commit(f.ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(blob); !os.IsNotExist(err) {
		t.Fatal("obsolete verified payload retained")
	}
	for _, path := range []string{foreign, unknown} {
		if _, err = os.Stat(path); err != nil {
			t.Fatal("foreign cache child removed", err)
		}
	}
	if err = os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadInstalledSnapshot(f.control); err != nil {
		t.Fatal("optional cache became installation prerequisite", err)
	}
}

func TestCacheRepairManagedAndNativeAliases(t *testing.T) {
	skipUnsupportedNative(t)
	for _, wholeRoot := range []bool{false, true} {
		t.Run(map[bool]string{false: "alias-only", true: "whole-root"}[wholeRoot], func(t *testing.T) {
			f := newCacheRepairFixture(t)
			change, err := StageNative(f.ctx, f.control, nativeFixture(t))
			if err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(f.old, "claude-notifications-linux-amd64")
			launcher := filepath.Join(f.old, "claude-notifications")
			nativeAlias := filepath.Join(f.old, "ClaudeNotifier.app")
			l, err := Commit(f.ctx, Request{ControlRoot: f.control, RuntimeRoot: f.old, Owner: f.ledger.Owner, ConsumerID: "gemini-hooks", RefreshOnly: true, Native: change,
				Files: []File{{Path: binary, Data: []byte(WriterProtocolMarker), Mode: 0700}, {Path: launcher, Link: filepath.Base(binary)}, {Path: nativeAlias, Link: change.After.Path}}})
			if err != nil {
				t.Fatal(err)
			}
			if wholeRoot {
				if err = os.RemoveAll(f.old); err != nil {
					t.Fatal(err)
				}
			} else {
				for _, path := range []string{launcher, nativeAlias} {
					if err = os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
			}
			plan, err := PreviewCacheRepair(f.ctx, f.control)
			if err != nil {
				t.Fatal(err)
			}
			after, err := RepairCache(f.ctx, f.control, plan)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(l.Native, after.Native) || !reflect.DeepEqual(l.Consumers, after.Consumers) || !reflect.DeepEqual(l.Files, after.Files) {
				t.Fatal("repair altered native or consumer identities")
			}
			if _, err = ReadInstalledSnapshot(f.control); err != nil {
				t.Fatal(err)
			}
		})
	}
}
