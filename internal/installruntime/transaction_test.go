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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
