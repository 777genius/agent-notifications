//go:build linux || darwin

package setupwizard

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestInspectReportsUnreadableLiveProfileWithoutChangingInstalledState(t *testing.T) {
	req := installedClaudeSelectionRequest(t)
	snapshot, err := installruntime.ReadInstalledSnapshot(req.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	mat, err := materializer(req, snapshot, req.RuntimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := liveClientBindings(mat, req.InstallationID, "claude")
	if err != nil || len(bindings) != 1 {
		t.Fatalf("installed binding: %+v %v", bindings, err)
	}
	data, err := bindingDataRoot(mat, req.InstallationID, bindings[0])
	if err != nil || data == "" {
		t.Fatalf("installed data: %q %v", data, err)
	}
	profilePath := filepath.Join(data, liveProfileFile)
	profileBefore, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	stateBefore, err := os.ReadFile(mat.Roots.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profilePath, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	req.Action = ActionInspect
	got, err := Run(testCtx(t), req)
	var syntax *json.SyntaxError
	if !errors.As(err, &syntax) || got.Outcome != "incomplete" || got.Reason != "live_profile_unavailable" || !strings.Contains(err.Error(), bindings[0].ClientBindingID) {
		t.Fatalf("corrupt live profile was reported as a successful inspection: %+v %v", got, err)
	}
	assertInstalled := func(result Result, profile string) {
		t.Helper()
		for _, target := range result.Targets {
			if target.Client == "claude" && target.Unit == "agent-notify" {
				if target.Outcome != "installed" || target.Reason != bindings[0].ClientBindingID || target.Profile != profile || target.TreeDigest == "" {
					t.Fatalf("inspection lost known installed facts: %+v", target)
				}
				return
			}
		}
		t.Fatal("inspection lost installed target")
	}
	assertInstalled(got, "")
	if body, err := os.ReadFile(profilePath); err != nil || string(body) != "{" {
		t.Fatalf("inspection changed corrupt profile metadata: %q %v", body, err)
	}
	if stateAfter, err := os.ReadFile(mat.Roots.StateFile); err != nil || !bytes.Equal(stateBefore, stateAfter) {
		t.Fatalf("inspection changed installed UAP state: %v", err)
	}
	after, err := installruntime.ReadInstalledSnapshot(req.ControlRoot)
	if err != nil || !reflect.DeepEqual(snapshot.Ledger, after.Ledger) {
		t.Fatalf("inspection changed managed state: %+v %v", after.Ledger, err)
	}
	if err := os.WriteFile(profilePath, profileBefore, 0600); err != nil {
		t.Fatal(err)
	}
	got, err = Run(testCtx(t), req)
	if err != nil || got.Outcome != "completed" {
		t.Fatalf("valid profile inspection: %+v %v", got, err)
	}
	assertInstalled(got, req.ClaudeConfig)
	// An absent sidecar remains valid legacy metadata; only read/parse errors
	// make the profile observation unavailable.
	if err := os.Remove(profilePath); err != nil {
		t.Fatal(err)
	}
	got, err = Run(testCtx(t), req)
	if err != nil || got.Outcome != "completed" {
		t.Fatalf("absent legacy profile inspection: %+v %v", got, err)
	}
	assertInstalled(got, "")
	// An independently unreadable pending intent must add recovery guidance
	// without replacing the typed profile failure or its primary reason.
	if err := os.WriteFile(profilePath, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	plantPendingIntent(t, testCtx(t), req.ControlRoot, req.RuntimeRoot, snapshot.Ledger.Generation, portablesetup.Intent{
		Version: 0, SetupIntentID: "unreadable-profile-intent", Action: "install", Stage: "confirmed",
		Targets: []portablesetup.IntentTarget{{Client: "claude", Units: []string{"agent-notify"}}},
	})
	before := policyConflictFiles(t, filepath.Dir(req.ControlRoot))
	got, err = Run(testCtx(t), req)
	if !errors.As(err, &syntax) || got.Outcome != "incomplete" || got.Reason != "live_profile_unavailable" || len(got.NextActions) != 1 || got.NextActions[0].Reason != "pending_intent_unreadable" {
		t.Fatalf("pending intent replaced the known profile failure: %+v %v", got, err)
	}
	assertInstalled(got, "")
	if after := policyConflictFiles(t, filepath.Dir(req.ControlRoot)); !reflect.DeepEqual(before, after) {
		t.Fatal("inspection changed profile or pending intent facts")
	}
}

func TestInspectProfileErrorKeepsRecoveryFacts(t *testing.T) {
	for _, pending := range []struct {
		name        string
		kernel, uap bool
		unreadable  bool
	}{
		{name: "kernel", kernel: true},
		{name: "uap", uap: true},
		{name: "both", kernel: true, uap: true},
		{name: "unreadable-uap", kernel: true, uap: true, unreadable: true},
	} {
		t.Run(pending.name, func(t *testing.T) {
			req := installedClaudeSelectionRequest(t)
			ctx := testCtx(t)
			snap, err := installruntime.ReadInstalledSnapshot(req.ControlRoot)
			if err != nil {
				t.Fatal(err)
			}
			mat, err := materializer(req, snap, req.RuntimeRoot)
			if err != nil {
				t.Fatal(err)
			}
			bindings, err := liveClientBindings(mat, req.InstallationID, "claude")
			if err != nil || len(bindings) != 1 {
				t.Fatalf("installed binding: %+v %v", bindings, err)
			}
			data, err := bindingDataRoot(mat, req.InstallationID, bindings[0])
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(data, liveProfileFile), []byte("{"), 0600); err != nil {
				t.Fatal(err)
			}
			if pending.kernel {
				plantWizardKernelJournal(t, ctx, req.ControlRoot, req.RuntimeRoot)
			}
			if pending.uap {
				plantWizardJournal(t, req.ControlRoot)
			}
			if pending.unreadable {
				if err := os.WriteFile(wizardPendingJournalPath(req.ControlRoot), []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			req.Action, req.Agents = ActionInspect, []string{"claude", "codex"}
			req.CodexHome = t.TempDir()
			before := policyConflictFiles(t, filepath.Dir(req.ControlRoot))
			got, err := Run(ctx, req)
			var syntax *json.SyntaxError
			if !errors.As(err, &syntax) || !strings.Contains(err.Error(), bindings[0].ClientBindingID) || got.Outcome != "incomplete" || got.Reason != "recovery_required" {
				t.Errorf("inspection lost profile cause or recovery precedence: %+v %v", got, err)
			}
			if pending.unreadable && (err == nil || !strings.Contains(err.Error(), "wizard-pending-op")) {
				t.Errorf("inspection lost the UAP read error beside the profile error: %v", err)
			}
			kernel, uap := false, false
			for _, next := range got.NextActions {
				if next.Kind == "recover" {
					kernel = kernel || next.Reason == "kernel-journal"
					uap = uap || strings.Contains(next.Reason, "wizard-pending-op")
				}
			}
			if kernel != pending.kernel || uap != pending.uap {
				t.Errorf("profile error hid recovery actions: %+v", got.NextActions)
			}
			installed, absent := false, false
			for _, target := range got.Targets {
				if target.Unit != "agent-notify" {
					continue
				}
				installed = installed || target.Client == "claude" && target.Outcome == "installed" && target.Reason == bindings[0].ClientBindingID && target.Profile == "" && target.TreeDigest != ""
				absent = absent || target.Client == "codex" && target.Outcome == "absent"
			}
			if !installed || !absent {
				t.Errorf("profile error hid selected installation facts: %+v", got.Targets)
			}
			if after := policyConflictFiles(t, filepath.Dir(req.ControlRoot)); !reflect.DeepEqual(before, after) {
				t.Fatal("read-only inspection changed installed state, profile or pending journals")
			}
		})
	}
}
