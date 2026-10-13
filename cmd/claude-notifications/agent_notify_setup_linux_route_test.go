//go:build linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	policyruntime "github.com/777genius/agent-notifications/internal/agentnotify/runtime"
	notifysetup "github.com/777genius/agent-notifications/internal/agentnotify/setup"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
)

// Only the offline selected-resource verifier is replaced. CLI parsing, managed
// transaction, policy reads and durable journal provisioning are production code.
func TestSetupLinuxDesktopThreadBinding(t *testing.T) {
	for _, scenario := range []string{"bound", "unbound", "no-verifier", "rejected", "removed-snapshot", "tampered-snapshot", "none", "windows"} {
		t.Run(scenario, func(t *testing.T) {
			f := newSetupCommandFixture(t)
			ctx := setupCommandContext(t)
			binding := notification.LinuxBinding{SnapshotPath: filepath.Join(f.root, "retained", "snapshot.json"), SHA256: strings.Repeat("a", 64)}
			if scenario == "tampered-snapshot" {
				if err := os.Mkdir(filepath.Dir(binding.SnapshotPath), 0700); err != nil {
					t.Fatal(err)
				}
				setupCommandWrite(t, binding.SnapshotPath, `{}`, 0600)
			}
			if scenario != "unbound" {
				patch, err := json.Marshal(map[string]any{"linuxCallbackSnapshot": binding})
				if err != nil {
					t.Fatal(err)
				}
				generation := f.generation(t)
				_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: f.control, RuntimeRoot: f.runtime, Owner: "existing-installer", ConsumerID: "hooks", RefreshOnly: true, ExpectedGeneration: &generation, PolicyFields: map[string]json.RawMessage{"route": patch}})
				if err != nil {
					t.Fatal(err)
				}
			}
			checks := 0
			f.composition.setup = func(o *notifysetup.Options) {
				o.Platform = "linux"
				if scenario == "windows" {
					o.Platform = "windows"
				}
				o.VerifyApplication = func(context.Context, notifysetup.Application) error { t.Fatal("Darwin verifier called"); return nil }
				if scenario == "removed-snapshot" || scenario == "tampered-snapshot" {
					return // Exercise the actual Linux verifier composed by the CLI.
				}
				o.VerifyLinuxBinding = nil
				if scenario != "no-verifier" {
					o.VerifyLinuxBinding = func(c context.Context, got notification.LinuxBinding) error {
						checks++
						if scenario == "none" || scenario == "unbound" || scenario == "windows" || got != binding {
							t.Fatal("unexpected Linux verification", got)
						}
						if scenario == "rejected" {
							return fmt.Errorf("TEST selected resources unavailable")
						}
						return c.Err()
					}
				}
			}
			navigation, wantCode := "desktop_thread", 1
			if scenario == "bound" || scenario == "none" {
				wantCode = 0
			}
			if scenario == "none" {
				navigation = "none"
			}
			before := setupCommandTree(t, f.root)
			result := setupCommandRun(t, ctx, f.args(t, "enable", "--global-config", f.global, "--navigation", navigation, "--allow-unknown-caller", "false", "--allow-caller-asserted", "true"), f.composition, wantCode)
			if wantCode != 0 {
				wantReason := "linux_callback_unavailable"
				if scenario == "windows" {
					wantReason = "unsupported_platform"
				}
				if result.Reason != wantReason || !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
					t.Fatal("unavailable setup mutated state", result)
				}
				return
			}
			if !result.ExplicitIntent || !result.RuntimeEligible || (scenario == "bound" && checks < 2) || (scenario == "none" && checks != 0) {
				t.Fatal(result, checks)
			}
			s, err := installruntime.ReadPolicySnapshot(ctx, f.control)
			if err != nil {
				t.Fatal(err)
			}
			policy, err := policyruntime.ValidateSetupPolicy(s, f.global)
			if err != nil || policy.Route.Linux != binding || policy.Route.LocalRouting != (scenario == "bound") || policy.Route.AllowUnknownCaller || !policy.Route.AllowCallerAsserted {
				t.Fatal("binding or explicit consent lost", policy, err)
			}
			store, err := journal.Open(ctx, journal.Options{Root: filepath.Join(f.control, "state", "journal"), Clock: journal.DefaultClock()})
			if err != nil {
				t.Fatal(err)
			}
			admitted, err := store.Admit(ctx, journal.Admission{Key: journal.Key{Source: "codex/local", Session: "TEST-session", Kind: journal.Explicit, Request: "bound-setup"}, Digest: [32]byte{1}, TrackingID: "tracking"})
			if err != nil || !admitted.Fresh {
				t.Fatal(admitted, err)
			}
		})
	}
}
