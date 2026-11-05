//go:build linux || darwin

package main

import (
	"path/filepath"
	"testing"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
)

func TestSelectedCursorBindingDoesNotInheritSiblingProfileOrInstallation(t *testing.T) {
	for _, tc := range []struct {
		name, siblingProfile, selectedInstallation string
	}{
		{name: "other-profile", siblingProfile: "sibling"},
		{name: "other-installation", siblingProfile: "selected", selectedInstallation: "TEST-selected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			profile := filepath.Join(root, ".cursor")
			sibling := filepath.Join(root, "sibling", ".cursor")
			recorded := sibling
			if tc.siblingProfile == "selected" {
				recorded = profile
			}
			state := domain.StateFileV2{
				SchemaVersion: domain.StateSchemaVersion,
				Installations: []domain.Installation{{
					InstallationID: "TEST-other",
					Clients: map[string]domain.ClientBinding{
						"TEST-sibling": {
							ClientID: "cursor", ClientBindingID: "TEST-sibling", NativeProfileRoot: recorded,
						},
					},
				}},
			}
			// The target predicate operates on a read-only snapshot. Do not
			// attempt to persist this intentionally partial authority fixture
			// as a valid UAP installation.
			if selectedCursorBinding(state, tc.selectedInstallation, profile) {
				t.Fatal("foreign Cursor profile/installation admitted as selected")
			}
			if !selectedCursorBinding(state, "TEST-other", recorded) {
				t.Fatal("exact recorded Cursor selection refused")
			}
		})
	}
}
