package setupwizard

import (
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
