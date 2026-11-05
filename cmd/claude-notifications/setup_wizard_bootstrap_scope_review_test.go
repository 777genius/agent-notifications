package main

import (
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
)

func TestBootstrapScopeRejectsChangedInstallationIdentity(t *testing.T) {
	// A confirmed bootstrap request does not choose a separate installation
	// identity; an explicitly supplied one must not widen the selected scope.
	expected := setupwizard.Request{
		Agents: []string{"cursor"},
		ControlRoot: "/TEST-control",
		ScopeRoot: "/TEST-profile", CursorConfig: "/TEST-profile",
		ClientExecutables: map[string]string{"cursor": "/TEST-client"},
	}
	if !sameWizardBootstrapScope(expected, expected) {
		t.Fatal("exact confirmed scope rejected")
	}
	changed := expected
	changed.InstallationID = "TEST-foreign-installation"
	if sameWizardBootstrapScope(changed, expected) {
		t.Fatal("foreign installation identity accepted after immutable confirmation")
	}
	selected := expected
	selected.InstallationID = "TEST-selected-installation"
	if !sameWizardBootstrapScope(selected, selected) {
		t.Fatal("exact nonempty installation identity rejected")
	}
	selected.InstallationID = "TEST-replayed-installation"
	if sameWizardBootstrapScope(selected, changed) {
		t.Fatal("a changed identity passed selection comparison")
	}
}
