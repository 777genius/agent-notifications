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
}
