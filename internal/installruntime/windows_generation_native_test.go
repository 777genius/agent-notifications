//go:build windows

package installruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/windowscallback"
	"golang.org/x/sys/windows/registry"
)

// These real fixed-participant tests are source-only in ordinary compile/inert
// CI. Only separately admitted disposable installed CI may execute them.
func nativeWindowsRecoveryAdmission(t *testing.T) {
	t.Helper()
	if os.Getenv("TEST_WINDOWS_INSTALLED_RECOVERY") != "fresh-owned-account-no-show" || os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("GITHUB_REPOSITORY") != "777genius/agent-notifications" || os.Getenv("GITHUB_EVENT_NAME") != "workflow_dispatch" || os.Getenv("RUNNER_OS") != "Windows" || !filepath.IsAbs(os.Getenv("RUNNER_TEMP")) {
		t.Skip("real registry/shortcut effects require explicit disposable TEST admission")
	}
}
func nativeWindowsRequest(t *testing.T) (context.Context, Request, Ledger, *WindowsChange) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	// Keep transaction/generation evidence even when foreign state blocks
	// recovery. The separately admitted job owns eventual account disposal.
	root, e := os.MkdirTemp(os.Getenv("RUNNER_TEMP"), "TEST-windows-recovery-")
	if e != nil {
		t.Fatal(e)
	}
	t.Log("retained TEST evidence", root)
	r := Request{ControlRoot: filepath.Join(root, "control"), RuntimeRoot: filepath.Join(root, "bin"), Owner: "existing-installer", ConsumerID: "codex", Files: []File{{Path: filepath.Join(root, "bin", "TEST-registration.json"), Data: []byte(`{}`), Mode: 0600}}}
	l, e := Commit(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	w, e := StageWindowsGeneration(ctx, r.ControlRoot)
	if e != nil {
		t.Fatal(e)
	}
	r.Files = nil
	r.RefreshOnly = true
	r.PolicyOnly = true
	r.ExpectedGeneration = &l.Generation
	r.Windows = w
	route, _ := json.Marshal(map[string]any{"localRouting": true, "windowsCallbackSnapshot": map[string]string{"snapshotPath": w.Binding.SnapshotPath, "sha256": w.Binding.SHA256}})
	r.PolicyFields = map[string]json.RawMessage{"route": route}
	return ctx, r, l, w
}

// Breakage: a crash after real registry/shortcut mutation before journal update
// cannot finish the original transaction, or binding appears before all typed
// participants read back and durable commit_decided. No notification is sent.
func TestWindowsNativeRecoverFixedMutationBoundaries(t *testing.T) {
	nativeWindowsRecoveryAdmission(t)
	for _, boundary := range []string{"windows:mutated:apply-clsid", "windows:mutated:apply-aumid", "windows:mutated:apply-shortcut", "windows:commit_decided", "windows:binding_published"} {
		t.Run(boundary, func(t *testing.T) {
			ctx, r, l, w := nativeWindowsRequest(t)
			r.Fault = func(phase string) error {
				if phase == boundary {
					return fmt.Errorf("owned TEST crash")
				}
				return nil
			}
			if _, e := Commit(ctx, r); e == nil {
				t.Fatal("fault not reached")
			}
			tx, e := readTransactionFile(filepath.Join(r.ControlRoot, "transaction.json"))
			if e != nil {
				t.Fatal(e)
			}
			// Read actual policy bytes after the injected Commit returns. The
			// public snapshot reader deliberately blocks every pending journal,
			// so its rejection alone cannot prove publication ordering.
			_, fields, _, e := readPolicyForUpdate(r.ControlRoot)
			if e != nil {
				t.Fatal(e)
			}
			compact := func(value []byte) []byte {
				if len(value) == 0 {
					return nil
				}
				var out bytes.Buffer
				if e := json.Compact(&out, value); e != nil {
					t.Fatal(e)
				}
				return out.Bytes()
			}
			published := bytes.Equal(compact(fields["route"]), compact(r.PolicyFields["route"]))
			if published != (boundary == "windows:binding_published") {
				t.Fatal("binding publication crossed decision/completion", boundary)
			}
			repair := Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot, Owner: r.Owner, ConsumerID: r.ConsumerID, RecoverOnly: true}
			after, e := Commit(ctx, repair)
			if e != nil || after.Generation != l.Generation+1 || len(after.WindowsRetained) != 1 {
				t.Fatal(after, e, tx.Windows.Phase)
			}
			if _, e = os.Lstat(filepath.Join(r.ControlRoot, "transaction.json")); !os.IsNotExist(e) {
				t.Fatal("pending journal remains", e)
			}
			policy, e := ReadPolicySnapshot(ctx, r.ControlRoot)
			if e != nil || !bytes.Equal(compact(policy.Fields["route"]), compact(r.PolicyFields["route"])) {
				t.Fatal("recovered binding not published", e)
			}
			g, e := windowscallback.Open(ctx, w.Binding, windowscallback.Deadline(ctx))
			if e != nil {
				t.Fatal(e)
			}
			defer g.Close()
			if _, e = g.Operator(ctx, "readback", nil, windowscallback.Deadline(ctx)); e != nil {
				t.Fatal(e)
			}
			// Fresh unique objects, no records/Show/callbacks in this TEST lane. Restore
			// only the exact fixed objects while the admitted generation remains held.
			for _, mode := range []string{"restore-shortcut", "restore-aumid", "restore-clsid"} {
				if _, e = g.Operator(ctx, mode, nil, windowscallback.Deadline(ctx)); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}

// Breakage: post-decision recovery repairs a changed foreign value, or chooses
// rollback after a binding's durable commit decision. Preserve the foreign edit.
func TestWindowsNativeForeignDecisionRecoveryRefuses(t *testing.T) {
	nativeWindowsRecoveryAdmission(t)
	ctx, r, _, w := nativeWindowsRequest(t)
	r.Fault = func(phase string) error {
		if phase == "windows:commit_decided" {
			return fmt.Errorf("owned TEST crash")
		}
		return nil
	}
	if _, e := Commit(ctx, r); e == nil {
		t.Fatal("fault not reached")
	}
	key, e := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\AppUserModelId\`+w.Snapshot.AUMID, registry.QUERY_VALUE|registry.SET_VALUE)
	if e != nil {
		t.Fatal(e)
	}
	defer key.Close()
	if e = key.SetStringValue("DisplayName", "TEST foreign replacement"); e != nil {
		t.Fatal(e)
	}
	repair := Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot, Owner: r.Owner, ConsumerID: r.ConsumerID, RecoverOnly: true}
	if _, e = Commit(ctx, repair); e == nil {
		t.Fatal("foreign edit repaired")
	}
	got, _, e := key.GetStringValue("DisplayName")
	if e != nil || got != "TEST foreign replacement" {
		t.Fatal(got, e)
	}
	repair.RecoverOnly = false
	repair.RollbackPending = true
	if _, e = Commit(ctx, repair); !errors.Is(e, ErrPolicyRecovery) {
		t.Fatal("durable commit reversed", e)
	}
	// The foreign object and pending generation intentionally remain evidence;
	// this lane grants no vendor/registration cleanup after the refused recovery.
}
