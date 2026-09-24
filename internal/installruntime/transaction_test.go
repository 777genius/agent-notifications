package installruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func request(t *testing.T) (context.Context, Request) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	root := t.TempDir()
	return ctx, Request{ControlRoot: filepath.Join(root, "control"), RuntimeRoot: filepath.Join(root, "bin"), Owner: "existing-installer", ConsumerID: "codex"}
}
func TestRecoverEveryBoundary(t *testing.T) {
	for _, boundary := range []string{"transaction", "promotion", "ledger"} {
		t.Run(boundary, func(t *testing.T) {
			ctx, r := request(t)
			target := filepath.Join(r.RuntimeRoot, "hook")
			r.Files = []File{{Path: target, Data: []byte("new"), Mode: 0755}}
			r.Fault = func(phase string) error {
				if phase == boundary || boundary == "promotion" && phase == "promotion:"+target {
					return fmt.Errorf("crash")
				}
				return nil
			}
			if _, err := Commit(ctx, r); err == nil {
				t.Fatal("fault not reached")
			}
			r.Fault = nil
			r.Files = nil
			l, err := Commit(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			if l.Generation != 2 || l.ID == "" {
				t.Fatalf("bad recovered ledger: %+v", l)
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "new" {
				t.Fatalf("promotion: %s %v", data, err)
			}
			again, err := Commit(ctx, r)
			if err != nil || again.ID != l.ID {
				t.Fatalf("repeat repair: %v", err)
			}
		})
	}
}
func TestRecoveryPreservesForeignEdit(t *testing.T) {
	ctx, r := request(t)
	target := filepath.Join(t.TempDir(), "hooks.json")
	r.ConfigPaths = []string{target}
	r.Files = []File{{Path: target, Data: []byte("ours"), Mode: 0600}}
	r.Fault = func(string) error { return fmt.Errorf("crash") }
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("fault not reached")
	}
	if err := os.WriteFile(target, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	r.Fault = nil
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("foreign recovery accepted")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "foreign" {
		t.Fatal("foreign edit overwritten")
	}
}
func TestCorruptTransactionRefused(t *testing.T) {
	ctx, r := request(t)
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.ControlRoot, "transaction.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("corrupt marker accepted")
	}
}
func TestConsumerGenerationAndOwner(t *testing.T) {
	ctx, r := request(t)
	first, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	r.ConsumerID = "claude"
	second, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	r.ConsumerID = "codex"
	r.RemoveConsumer = true
	third, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(third.Consumers) != 1 || third.ID != first.ID || third.PolicyGeneration <= second.PolicyGeneration {
		t.Fatal("consumer ledger invalid")
	}
	r.ConsumerID = "claude"
	last, err := Commit(ctx, r)
	if err != nil || len(last.Consumers) != 0 {
		t.Fatalf("final consumer: %v", err)
	}
	r.Owner = "foreign-manager"
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("silent takeover")
	}
}

func TestVersionedClaudeCacheRelocation(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprint("shared=", shared), func(t *testing.T) {
			root := t.TempDir()
			var err error
			root, err = filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			cache := filepath.Join(root, ".claude", "plugins", "cache", "claude-notifications-go", "claude-notifications-go")
			oldRoot, newRoot := filepath.Join(cache, "1.45.7"), filepath.Join(cache, "1.45.12")
			oldFile, newFile := filepath.Join(oldRoot, "bin", "claude-notifications-linux-amd64"), filepath.Join(newRoot, "bin", "claude-notifications-linux-amd64")
			control := filepath.Join(root, "control")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			old := Request{ControlRoot: control, RuntimeRoot: oldRoot, Owner: "existing-installer", ConsumerID: "claude-hooks",
				Files: []File{{Path: oldFile, Data: []byte("old" + WriterProtocolMarker), Mode: 0700}}}
			first, err := Commit(ctx, old)
			if err != nil {
				t.Fatal(err)
			}
			if shared {
				if _, err = Commit(ctx, Request{ControlRoot: control, RuntimeRoot: oldRoot, Owner: old.Owner,
					ConsumerID: "portable:existing", Consumer: Consumer{Registration: "unchanged", Commands: []string{oldFile}}}); err != nil {
					t.Fatal(err)
				}
			}
			move := Request{ControlRoot: control, RuntimeRoot: newRoot, Owner: old.Owner, ConsumerID: old.ConsumerID,
				Files: []File{{Path: newFile, Data: []byte("new" + WriterProtocolMarker), Mode: 0700}}}
			if _, err = Commit(ctx, move); err == nil {
				t.Fatal("silent cache relocation")
			}
			if _, err = os.Stat(newFile); !os.IsNotExist(err) {
				t.Fatal("rejected relocation published new bytes")
			}
			move.RelocateVersionedCache = true
			moved, err := Commit(ctx, move)
			if err != nil {
				t.Fatal(err)
			}
			if moved.ID != first.ID || moved.Consumers["claude-hooks"].RuntimeRoot != newRoot || !moved.Files[newFile].Exists {
				t.Fatalf("wrong relocated ownership: %+v", moved)
			}
			if shared {
				if moved.RuntimeRoot != oldRoot || moved.Consumers["portable:existing"].RuntimeRoot != oldRoot || !moved.Files[oldFile].Exists {
					t.Fatal("shared old runtime was not retained")
				}
				if data, err := os.ReadFile(oldFile); err != nil || string(data) != "new"+WriterProtocolMarker {
					t.Fatalf("retained portable primary was not refreshed: %q, %v", data, err)
				}
			} else {
				if moved.RuntimeRoot != newRoot || moved.Files[oldFile].Exists {
					t.Fatal("unreferenced Claude cache was not de-owned")
				}
				if data, err := os.ReadFile(oldFile); err != nil || string(data) != "old"+WriterProtocolMarker {
					t.Fatalf("old cache bytes changed: %q, %v", data, err)
				}
				if err := os.Remove(oldFile); err != nil {
					t.Fatal(err)
				}
				if _, err := Commit(ctx, Request{ControlRoot: control, RuntimeRoot: newRoot, Owner: old.Owner,
					ConsumerID: old.ConsumerID, RefreshOnly: true}); err != nil {
					t.Fatalf("pruned old cache blocked new runtime: %v", err)
				}
			}
		})
	}
}

func TestVersionedClaudeCacheRelocationRejectsUnrefreshablePortablePrimary(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, ".claude", "plugins", "cache", "claude-notifications-go", "claude-notifications-go")
	oldRoot, newRoot := filepath.Join(cache, "1.45.7"), filepath.Join(cache, "1.45.13")
	oldFile, newFile := filepath.Join(oldRoot, "bin", "claude-notifications-linux-amd64"), filepath.Join(newRoot, "bin", "claude-notifications-linux-amd64")
	control := filepath.Join(root, "control")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := Commit(ctx, Request{ControlRoot: control, RuntimeRoot: oldRoot, Owner: "existing-installer", ConsumerID: "claude-hooks",
		Files: []File{{Path: oldFile, Data: []byte("old" + WriterProtocolMarker), Mode: 0700}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Commit(ctx, Request{ControlRoot: control, RuntimeRoot: oldRoot, Owner: "existing-installer", ConsumerID: "portable:existing",
		Consumer: Consumer{Commands: []string{oldFile}}}); err != nil {
		t.Fatal(err)
	}
	move := Request{ControlRoot: control, RuntimeRoot: newRoot, Owner: "existing-installer", ConsumerID: "claude-hooks",
		RelocateVersionedCache: true, Files: []File{{Path: filepath.Join(newRoot, "bin", "different"), Data: []byte("new"), Mode: 0700}}}
	if _, err := Commit(ctx, move); err == nil {
		t.Fatal("unrefreshable portable primary accepted")
	}
	if _, err := os.Stat(newFile); !os.IsNotExist(err) {
		t.Fatal("rejected relocation published new primary")
	}
}

func TestVersionedClaudeCacheRelocationRejectsOtherRootsAndEdits(t *testing.T) {
	root := t.TempDir()
	var err error
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, ".claude", "plugins", "cache", "claude-notifications-go", "claude-notifications-go")
	oldRoot := filepath.Join(cache, "1.45.7")
	oldFile := filepath.Join(oldRoot, "bin", "sender")
	control := filepath.Join(root, "control")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := Commit(ctx, Request{ControlRoot: control, RuntimeRoot: oldRoot, Owner: "existing-installer", ConsumerID: "claude-hooks",
		Files: []File{{Path: oldFile, Data: []byte("old"), Mode: 0700}}}); err != nil {
		t.Fatal(err)
	}
	for _, newRoot := range []string{filepath.Join(root, "foreign", "1.45.12"), filepath.Join(cache, "not-a-version")} {
		newFile := filepath.Join(newRoot, "bin", "sender")
		if _, err := Commit(ctx, Request{ControlRoot: control, RuntimeRoot: newRoot, Owner: "existing-installer", ConsumerID: "claude-hooks",
			RelocateVersionedCache: true, Files: []File{{Path: newFile, Data: []byte("new"), Mode: 0700}}}); err == nil {
			t.Fatalf("unrelated root accepted: %s", newRoot)
		}
	}
	if err := os.WriteFile(oldFile, []byte("foreign"), 0700); err != nil {
		t.Fatal(err)
	}
	newRoot := filepath.Join(cache, "1.45.12")
	if _, err := Commit(ctx, Request{ControlRoot: control, RuntimeRoot: newRoot, Owner: "existing-installer", ConsumerID: "claude-hooks",
		RelocateVersionedCache: true, Files: []File{{Path: filepath.Join(newRoot, "bin", "sender"), Data: []byte("new"), Mode: 0700}}}); err == nil {
		t.Fatal("foreign edit in old cache accepted")
	}
}

func TestVersionedClaudeCacheRelocationRecovers(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, ".claude", "plugins", "cache", "claude-notifications-go", "claude-notifications-go")
	oldRoot, newRoot := filepath.Join(cache, "1.45.7"), filepath.Join(cache, "1.45.12")
	oldFile, newFile := filepath.Join(oldRoot, "bin", "sender"), filepath.Join(newRoot, "bin", "sender")
	control := filepath.Join(root, "control")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := Commit(ctx, Request{ControlRoot: control, RuntimeRoot: oldRoot, Owner: "existing-installer", ConsumerID: "claude-hooks",
		Files: []File{{Path: oldFile, Data: []byte("old"), Mode: 0700}}}); err != nil {
		t.Fatal(err)
	}
	move := Request{ControlRoot: control, RuntimeRoot: newRoot, Owner: "existing-installer", ConsumerID: "claude-hooks",
		RelocateVersionedCache: true, Files: []File{{Path: newFile, Data: []byte("new"), Mode: 0700}},
		Fault: func(phase string) error {
			if phase == "transaction" {
				return fmt.Errorf("simulated interruption")
			}
			return nil
		},
	}
	if _, err := Commit(ctx, move); err == nil {
		t.Fatal("interruption did not stop relocation")
	}
	ledger, err := Commit(ctx, Request{ControlRoot: control, RecoverOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if ledger.Consumers["claude-hooks"].RuntimeRoot != newRoot || ledger.RuntimeRoot != newRoot || ledger.Files[oldFile].Exists || !ledger.Files[newFile].Exists {
		t.Fatalf("relocation recovery did not finish atomically: %+v", ledger)
	}
	if data, err := os.ReadFile(oldFile); err != nil || string(data) != "old" {
		t.Fatalf("old cache changed during recovery: %q, %v", data, err)
	}
}

func TestVersionedClaudeCacheRelocationSharedRecovery(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, ".claude", "plugins", "cache", "claude-notifications-go", "claude-notifications-go")
	oldRoot, newRoot := filepath.Join(cache, "1.45.7"), filepath.Join(cache, "1.45.13")
	entry := "claude-notifications-linux-amd64"
	oldFile, newFile := filepath.Join(oldRoot, "bin", entry), filepath.Join(newRoot, "bin", entry)
	control := filepath.Join(root, "control")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := Commit(ctx, Request{ControlRoot: control, RuntimeRoot: oldRoot, Owner: "existing-installer", ConsumerID: "claude-hooks",
		Files: []File{{Path: oldFile, Data: []byte("old" + WriterProtocolMarker), Mode: 0700}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Commit(ctx, Request{ControlRoot: control, RuntimeRoot: oldRoot, Owner: "existing-installer", ConsumerID: "portable:existing",
		Consumer: Consumer{Commands: []string{oldFile}}}); err != nil {
		t.Fatal(err)
	}
	move := Request{ControlRoot: control, RuntimeRoot: newRoot, Owner: "existing-installer", ConsumerID: "claude-hooks",
		RelocateVersionedCache: true, Files: []File{{Path: newFile, Data: []byte("new" + WriterProtocolMarker), Mode: 0700}},
		Fault: func(phase string) error {
			if phase == "transaction" {
				return fmt.Errorf("simulated interruption")
			}
			return nil
		},
	}
	if _, err := Commit(ctx, move); err == nil {
		t.Fatal("interruption did not stop relocation")
	}
	ledger, err := Commit(ctx, Request{ControlRoot: control, RecoverOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if ledger.RuntimeRoot != oldRoot || ledger.Consumers["claude-hooks"].RuntimeRoot != newRoot ||
		ledger.Consumers["portable:existing"].RuntimeRoot != oldRoot || !ledger.Files[oldFile].Exists || !ledger.Files[newFile].Exists {
		t.Fatalf("shared relocation recovery lost ownership: %+v", ledger)
	}
	for _, path := range []string{oldFile, newFile} {
		if data, err := os.ReadFile(path); err != nil || string(data) != "new"+WriterProtocolMarker {
			t.Fatalf("shared relocation recovery left stale bytes at %s: %q, %v", path, data, err)
		}
	}
}

func TestFinalUninstallRetainsForeignAndState(t *testing.T) {
	ctx, r := request(t)
	path := filepath.Join(r.RuntimeRoot, "sender")
	r.Files = []File{{Path: path, Data: []byte("sender"), Mode: 0755}}
	first, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(r.RuntimeRoot, "foreign")
	state := filepath.Join(r.ControlRoot, "state", "dedup")
	for _, p := range []string{foreign, state} {
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r.Files = nil
	r.ConsumerID = "claude"
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.ConsumerID = "codex"
	r.RemoveConsumer = true
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("first removal deleted shared runtime")
	}
	r.ConsumerID = "claude"
	last, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("last removal retained ordinary sender")
	}
	if first.ID != last.ID || last.PolicyGeneration <= first.PolicyGeneration {
		t.Fatal("lost namespace/generation")
	}
	for _, p := range []string{foreign, state} {
		data, err := os.ReadFile(p)
		if err != nil || string(data) != "keep" {
			t.Fatalf("lost unowned/state path: %s", p)
		}
	}
	if _, err := Commit(ctx, r); err != nil {
		t.Fatalf("repeat removal: %v", err)
	}
}

func TestRollbackIndividualIdentities(t *testing.T) {
	for _, foreignEdit := range []bool{false, true} {
		t.Run(fmt.Sprint(foreignEdit), func(t *testing.T) {
			ctx, r := request(t)
			old := filepath.Join(r.RuntimeRoot, "old")
			created := filepath.Join(r.RuntimeRoot, "created")
			if err := durable(old, []byte("before"), 0711); err != nil {
				t.Fatal(err)
			}
			before, err := Fingerprint(old)
			if err != nil {
				t.Fatal(err)
			}
			r.Files = []File{{Path: old, Before: before, Data: []byte("after"), Mode: 0755}, {Path: created, Data: []byte("new"), Mode: 0600}}
			r.Fault = func(phase string) error {
				if phase == "ledger" {
					return fmt.Errorf("crash")
				}
				return nil
			}
			if _, err := Commit(ctx, r); err == nil {
				t.Fatal("fault not reached")
			}
			if foreignEdit {
				if err := os.WriteFile(old, []byte("foreign"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			r.Fault = nil
			r.Files = nil
			r.RollbackPending = true
			_, err = Commit(ctx, r)
			if foreignEdit {
				if err == nil {
					t.Fatal("foreign rollback accepted")
				}
				data, _ := os.ReadFile(old)
				if string(data) != "foreign" {
					t.Fatal("foreign edit lost")
				}
				if _, err := os.Stat(created); err != nil {
					t.Fatal("partial rollback on conflict")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := Fingerprint(old)
			if err != nil || got != before {
				t.Fatalf("old identity: %+v %v", got, err)
			}
			if _, err := os.Stat(created); !os.IsNotExist(err) {
				t.Fatal("new file survived rollback")
			}
			if _, err := Commit(ctx, r); err != nil {
				t.Fatalf("repeat rollback: %v", err)
			}
		})
	}
}

func TestInterruptedRollbackResumesReverseDecision(t *testing.T) {
	ctx, r := request(t)
	asset := filepath.Join(r.RuntimeRoot, "sender")
	configPath := filepath.Join(t.TempDir(), "hooks.json")
	r.ConfigPaths = []string{configPath}
	r.Consumer.Commands = []string{"cmd-a"}
	r.Files = []File{
		{Path: asset, Data: []byte("A"), Mode: 0755},
		{Path: configPath, Data: []byte(`{"v":"A"}`), Mode: 0600},
	}
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	beforeAsset, err := Fingerprint(asset)
	if err != nil {
		t.Fatal(err)
	}
	beforeConfig, err := Fingerprint(configPath)
	if err != nil {
		t.Fatal(err)
	}
	r.Consumer.Commands = []string{"cmd-b"}
	r.Files = []File{
		{Path: asset, Before: beforeAsset, Data: []byte("B"), Mode: 0755},
		{Path: configPath, Before: beforeConfig, Data: []byte(`{"v":"B"}`), Mode: 0600},
	}
	r.Fault = func(phase string) error {
		if phase == "ledger" {
			return fmt.Errorf("crash")
		}
		return nil
	}
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("missing upgrade crash")
	}
	r.Files = nil
	r.RollbackPending = true
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("missing rollback crash")
	}
	pending, err := decodeTransaction(mustRead(t, filepath.Join(r.ControlRoot, "transaction.json")))
	if err != nil {
		t.Fatal(err)
	}
	if !pending.Rollback {
		t.Fatal("reversal intent not persisted")
	}
	r.Fault = nil
	ledger, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(asset); err != nil || string(data) != "A" {
		t.Fatalf("asset rolled forward: %s %v", data, err)
	}
	if data, err := os.ReadFile(configPath); err != nil || string(data) != `{"v":"A"}` {
		t.Fatalf("config rolled forward: %s %v", data, err)
	}
	if commands := ledger.Consumers[r.ConsumerID].Commands; len(commands) != 1 || commands[0] != "cmd-a" {
		t.Fatalf("consumer rolled forward: %+v", ledger.Consumers[r.ConsumerID])
	}
	if _, err := os.Lstat(filepath.Join(r.ControlRoot, "transaction.json")); !os.IsNotExist(err) {
		t.Fatal("pending marker retained")
	}
}

func TestMissingTransactionAfterPromotionRefuses(t *testing.T) {
	ctx, r := request(t)
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.RuntimeRoot, "sender")
	r.Files = []File{{Path: path, Data: []byte("new"), Mode: 0755}}
	r.Fault = func(phase string) error {
		if phase == "promotion:"+path {
			return fmt.Errorf("crash")
		}
		return nil
	}
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("fault not reached")
	}
	if err := os.Remove(filepath.Join(r.ControlRoot, "transaction.json")); err != nil {
		t.Fatal(err)
	}
	r.Fault = nil
	r.Files = nil
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("missing marker silently repaired")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatal("ambiguous promoted file changed")
	}
}

func TestConfigCrashBoundaryPreservesLaterForeignEdit(t *testing.T) {
	ctx, r := request(t)
	path := filepath.Join(t.TempDir(), "hooks.json")
	r.ConfigPaths = []string{path}
	r.Prepare = func() ([]File, error) {
		before, err := Fingerprint(path)
		return []File{{Path: path, Before: before, Data: []byte(`{"owned":true}`), Mode: 0600}}, err
	}
	r.Fault = func(phase string) error {
		if phase == "promotion:"+path {
			return fmt.Errorf("crash")
		}
		return nil
	}
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("config fault not reached")
	}
	foreign := `{"owned":true,"foreign":false}`
	if err := os.WriteFile(path, []byte(foreign), 0600); err != nil {
		t.Fatal(err)
	}
	r.Fault = nil
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("ambiguous config replay accepted")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != foreign {
		t.Fatal("foreign edit lost")
	}
}

func TestStaleGenerationRefusedBeforePrepare(t *testing.T) {
	ctx, r := request(t)
	l, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	stale := l.Generation - 1
	r.ExpectedGeneration = &stale
	r.Prepare = func() ([]File, error) { t.Fatal("stale transaction reached config adapter"); return nil, nil }
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("stale generation accepted")
	}
}

func TestRecordedModeMatchesFilesystem(t *testing.T) {
	for _, mode := range []uint32{0600, 0644, 0711, 0755} {
		path := filepath.Join(t.TempDir(), "file")
		data := []byte("identity")
		if err := durable(path, data, os.FileMode(mode)); err != nil {
			t.Fatal(err)
		}
		actual, err := Fingerprint(path)
		if err != nil || actual != identity(data, mode) {
			t.Fatalf("OS cannot retain recorded mode %o: %+v %v", mode, actual, err)
		}
	}
}

func TestRuntimeRefreshPreservesConsumerIdentity(t *testing.T) {
	ctx, r := request(t)
	r.Consumer = Consumer{Registration: filepath.Join(t.TempDir(), "hooks.json"), Commands: []string{"exact old hook"}}
	first, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	r.ConsumerID = "runtime-refresh"
	r.Consumer = Consumer{}
	r.RefreshOnly = true
	r.Files = []File{{Path: filepath.Join(r.RuntimeRoot, "utility"), Data: []byte("new optional utility"), Mode: 0755}}
	next, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Consumers, next.Consumers) {
		t.Fatal("refresh changed registration/added a phantom consumer")
	}
	r.RuntimeRoot = filepath.Join(t.TempDir(), "unregistered")
	r.Files = nil
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("unregistered refresh invented ownership")
	}
}

func TestCommitRefusesForeignEditOnReplacingPath(t *testing.T) {
	ctx, r := request(t)
	target := filepath.Join(r.RuntimeRoot, "asset")
	r.Files = []File{{Path: target, Data: []byte("original"), Mode: 0600}}
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := Fingerprint(target)
	if err != nil {
		t.Fatal(err)
	}
	r.Files = []File{{Path: target, Before: before, Data: []byte("upgrade"), Mode: 0600}}
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("foreign edit overwritten")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "foreign" {
		t.Fatalf("preserved: %s %v", data, err)
	}
}

func TestMissingTransactionBlobIsCorruptMarker(t *testing.T) {
	ctx, r := request(t)
	target := filepath.Join(r.RuntimeRoot, "asset")
	payload := make([]byte, 2<<20)
	for i := range payload {
		payload[i] = 'x'
	}
	r.Files = []File{{Path: target, Data: payload, Mode: 0600}}
	r.Fault = func(phase string) error {
		if phase == "promotion" || phase == "promotion:"+target {
			return fmt.Errorf("crash")
		}
		return nil
	}
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("fault not reached")
	}
	marker := filepath.Join(r.ControlRoot, "transaction.json")
	tx, err := readTransactionFile(marker)
	if err != nil || len(tx.Files) != 1 || tx.Files[0].DataSHA256 == "" {
		t.Fatalf("pending blob missing: %v", err)
	}
	blob := filepath.Join(transactionBlobDir(marker), tx.Files[0].DataSHA256)
	if err := os.Remove(blob); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	generation := tx.After.Generation
	r.Fault = nil
	r.Files = nil
	if _, err := Commit(ctx, r); err == nil {
		t.Fatal("missing blob treated as missing marker")
	}
	after, err := os.ReadFile(marker)
	if err != nil || string(after) != string(before) {
		t.Fatal("marker dropped")
	}
	ledger, err := readLedger(r.ControlRoot)
	if err != nil || ledger.Generation == generation {
		t.Fatalf("generation: %+v %v", ledger, err)
	}
}
