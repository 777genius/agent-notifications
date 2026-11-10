package setupwizard

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWizardInstallOrUpdateRejectsUnselectedPrerequisite(t *testing.T) {
	selected := []string{"claude"}
	next := []NextAction{
		{Kind: "update", Agents: []string{"codex"}},
		{Kind: "install", Agents: []string{"claude"}},
	}
	if validInstallOrUpdateActions(selected, next) {
		t.Fatal("bootstrap may not mutate an unselected sibling")
	}
	if validInstallOrUpdateActions(selected, []NextAction{{Kind: "update", Agents: []string{"claude", "claude"}}}) {
		t.Fatal("duplicate client action accepted")
	}
	if validInstallOrUpdateActions(selected, []NextAction{{Kind: "repair", Agents: selected}}) {
		t.Fatal("unexpected action accepted")
	}
}

func TestBootstrapReadySelectionPreservesOptOut(t *testing.T) {
	if got := readyBootstrapAgents([]string{"claude", "codex"}, []string{"codex"}); !reflect.DeepEqual(got, []string{"codex"}) {
		t.Fatalf("effect subset: %v", got)
	}
	// An existing opt-out is not converted into an installation by a successful
	// primary setup: readiness can only further reduce the confirmed selection.
	if got := readyBootstrapAgents([]string{"claude"}, []string{"codex"}); len(got) != 0 {
		t.Fatalf("ready client bypassed opt-out: %v", got)
	}
}

// The install/update phase can commit clients even if an ensuing read-only
// inspection refuses the control root before it can enumerate UAP bindings.
func TestVerifyInstallOrUpdatePreservesCommittedTargetsWhenInspectUnavailable(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "TEST-control-file")
	if err := os.WriteFile(blocked, []byte("not a control directory"), 0600); err != nil {
		t.Fatal(err)
	}
	req := Request{Action: ActionInstall, Agents: []string{"codex"}, ControlRoot: blocked}
	committed := Result{Action: "install", Outcome: "completed", Targets: []TargetResult{
		{Client: "codex", Unit: "agent-notify", Outcome: "completed"},
	}}
	got, err := verifyInstallOrUpdate(context.Background(), req, committed)
	if err == nil || got.Action != "install" || got.Outcome != "incomplete" || got.Reason == "" {
		t.Fatalf("early inspect error reported success: %+v %v", got, err)
	}
	if !reflect.DeepEqual(got.Targets, committed.Targets) {
		t.Fatalf("early inspection erased committed target: %+v", got.Targets)
	}
	got.Targets[0].Client = "mutated"
	if committed.Targets[0].Client != "codex" {
		t.Fatal("failure result aliased caller-owned target rows")
	}
}
