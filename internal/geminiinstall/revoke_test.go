package geminiinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Previously, ordinary policy mutation refused missing receipt/runtime files
// and native drift before it could disable a consumer. Removal must revoke
// independently, preserve siblings and fence previously loaded event commands.
func TestRevokeBeforeCleanup(t *testing.T) {
	for _, damaged := range []bool{false, true} {
		name := "healthy"
		if damaged {
			name = "damaged"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			base := t.TempDir()
			root, runtimeRoot := filepath.Join(base, "control"), filepath.Join(base, "runtime")
			receipt, binary := filepath.Join(root, "gemini-receipt.json"), filepath.Join(runtimeRoot, "sender")
			l, err := installruntime.Commit(ctx, installruntime.Request{
				ControlRoot: root, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: consumerID,
				Consumer: installruntime.Consumer{Registration: receipt, Commands: []string{binary, "gemini-event"}},
				Files: []installruntime.File{{Path: receipt, Data: []byte("inert test receipt"), Mode: 0600},
					{Path: binary, Data: []byte("inert test sender"), Mode: 0700}},
			})
			if err != nil {
				t.Fatal(err)
			}
			// Use the existing shared native staging kernel, never execute a helper.
			source := filepath.Join(base, "fixture.app")
			helperRel := filepath.Join("Contents", "MacOS", "terminal-notifier-modern")
			if err := os.MkdirAll(filepath.Dir(filepath.Join(source, helperRel)), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, helperRel), []byte("inert test helper"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, "native"), 0700); err != nil {
				t.Fatal(err)
			}
			native, err := installruntime.StageNative(ctx, root, source)
			if err != nil {
				t.Fatal(err)
			}
			enabled := true
			l, err = installruntime.Commit(ctx, installruntime.Request{
				ControlRoot: root, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: "opencode-notifications",
				Consumer:           installruntime.Consumer{Registration: filepath.Join(base, "sibling-plugin")},
				ExpectedGeneration: &l.Generation, PolicyEnabled: &enabled, Native: native,
				PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"geminiNotifications":{"desktop":true,"webhook":true},"openCodeNotifications":{"desktop":true,"webhook":false},"foreign":{"keep":true}}`)},
			})
			if err != nil {
				t.Fatal(err)
			}
			s, err := installruntime.ReadPolicySnapshot(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			if damaged {
				for _, path := range []string{receipt, binary} {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(native.After.Path, helperRel), []byte("foreign damage"), 0700); err != nil {
					t.Fatal(err)
				}
				if _, err := installruntime.ReadPolicySnapshot(ctx, root); err == nil {
					t.Fatal("ordinary policy read accepted damaged delivery assets")
				}
			} else {
				_, release, err := installruntime.AcquirePolicyLease(ctx, root, s)
				if err != nil {
					t.Fatal(err)
				}
				short, stop := context.WithTimeout(ctx, 40*time.Millisecond)
				err = RevokeChannels(short, root, runtimeRoot)
				stop()
				release()
				if err == nil {
					t.Fatal("revocation crossed an active effect lease")
				}
			}
			if err := RevokeChannels(ctx, root, runtimeRoot); err != nil {
				t.Fatal(err)
			}
			next, recovery, err := installruntime.ReadOwnership(root)
			if err != nil || recovery || next.Generation != l.Generation+1 || next.PolicyGeneration != l.PolicyGeneration+1 {
				t.Fatalf("revocation did not publish a new generation: %+v %v", next, err)
			}
			if !reflect.DeepEqual(next.Consumers, l.Consumers) || !reflect.DeepEqual(next.Files, l.Files) || !reflect.DeepEqual(next.Native, l.Native) {
				t.Fatal("revocation changed registrations, assets or shared helper identity")
			}
			data, err := os.ReadFile(filepath.Join(root, "agent-notifications.json"))
			if err != nil {
				t.Fatal(err)
			}
			var policy struct {
				Enabled bool
				Route   map[string]json.RawMessage
			}
			if err := json.Unmarshal(data, &policy); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]string{
				"geminiNotifications":   `{"desktop":false,"webhook":false}`,
				"openCodeNotifications": `{"desktop":true,"webhook":false}`,
				"foreign":               `{"keep":true}`,
			} {
				var gotObject, wantObject any
				_ = json.Unmarshal(policy.Route[key], &gotObject)
				_ = json.Unmarshal([]byte(want), &wantObject)
				if !reflect.DeepEqual(gotObject, wantObject) {
					t.Fatalf("revocation changed %s: %s", key, data)
				}
			}
			if !policy.Enabled {
				t.Fatal("Gemini revocation disabled portable consent")
			}
			if _, release, err := installruntime.AcquirePolicyLease(ctx, root, s); err == nil {
				release()
				t.Fatal("loaded event command acquired a lease after revocation")
			}
			if _, err := os.Stat(filepath.Join(native.After.Path, helperRel)); err != nil {
				t.Fatal("revocation removed shared helper")
			}
		})
	}
}

func TestRemoveRevokesBeforeRuntimeSymlinkConflict(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix symlink substitution; Windows filesystem guards are qualified separately")
	}
	for _, explicitRuntime := range []bool{false, true} {
		name := "recorded-runtime"
		if explicitRuntime {
			name = "explicit-runtime"
		}
		t.Run(name, func(t *testing.T) {
			ctx, r, settings := installFixture(t)
			if err := Apply(ctx, r); err != nil {
				t.Fatal(err)
			}
			if err := setChannels(ctx, r.ControlRoot, r.RuntimeRoot, true, true); err != nil {
				t.Fatal(err)
			}
			old, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
			if err != nil {
				t.Fatal(err)
			}
			l := old.Installation.Ledger
			original := r.RuntimeRoot
			retained := original + "-retained"
			if err := os.Rename(original, retained); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(retained, original); err != nil {
				t.Fatal(err)
			}
			foreign := filepath.Join(retained, "foreign")
			if err := os.WriteFile(foreign, []byte("foreign retained asset"), 0600); err != nil {
				t.Fatal(err)
			}
			binaryName, _ := binaryName(r.GOOS, r.GOARCH)
			paths := []string{settings, filepath.Join(r.ControlRoot, receiptName), filepath.Join(retained, binaryName), foreign, original}
			before := make(map[string]installruntime.Identity)
			for _, path := range paths {
				before[path], err = installruntime.Fingerprint(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			r.Action = Remove
			if !explicitRuntime {
				r.RuntimeRoot = ""
			}
			err = Apply(ctx, r)
			if err == nil || !strings.Contains(err.Error(), "consent revoked; cleanup conflict") {
				desktop, webhook, _, readErr := readChannels(r.ControlRoot)
				if readErr != nil {
					t.Fatal(readErr)
				}
				t.Fatalf("removal did not revoke before cleanup conflict: %v; desktop=%v webhook=%v", err, desktop, webhook)
			}
			desktop, webhook, _, err := readChannels(r.ControlRoot)
			if err != nil || desktop || webhook {
				t.Fatalf("runtime conflict left consent active: %v %v %v", desktop, webhook, err)
			}
			next, recovery, err := installruntime.ReadOwnership(r.ControlRoot)
			if err != nil || recovery || next.Generation != l.Generation+1 || next.PolicyGeneration != l.PolicyGeneration+1 ||
				!reflect.DeepEqual(next.Consumers, l.Consumers) || !reflect.DeepEqual(next.Files, l.Files) || !reflect.DeepEqual(next.Native, l.Native) {
				t.Fatalf("cleanup conflict changed assets/registration or failed to advance generation: %+v %v", next, err)
			}
			for _, path := range paths {
				after, err := installruntime.Fingerprint(path)
				if err != nil || after != before[path] {
					t.Fatalf("cleanup changed retained/foreign asset %s: %+v %v", path, after, err)
				}
			}
			if _, release, err := installruntime.AcquirePolicyLease(ctx, r.ControlRoot, old); err == nil {
				release()
				t.Fatal("old snapshot admitted after removal conflict")
			}
			policyPath := filepath.Join(r.ControlRoot, "agent-notifications.json")
			revoked, err := os.ReadFile(policyPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(original); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(retained, original); err != nil {
				t.Fatal(err)
			}
			current, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
			if err != nil || current.Installation.Ledger.Generation != next.Generation {
				t.Fatalf("restored assets failed ordinary verification: %v", err)
			}
			after, err := os.ReadFile(policyPath)
			if err != nil || !bytes.Equal(after, revoked) {
				t.Fatal("restoring runtime restored consent")
			}
			if _, release, err := installruntime.AcquirePolicyLease(ctx, r.ControlRoot, old); err == nil {
				release()
				t.Fatal("restoring runtime admitted old snapshot")
			}
		})
	}
}
