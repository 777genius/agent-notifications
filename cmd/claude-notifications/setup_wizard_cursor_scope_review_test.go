//go:build linux || darwin

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
)

func TestImplicitCursorUpdateDoesNotInheritSiblingProfileOrInstallation(t *testing.T) {
	for _, tc := range []struct {
		name, siblingProfile, selectedInstallation string
	}{
		{name: "other-profile", siblingProfile: "sibling"},
		{name: "other-installation", siblingProfile: "selected", selectedInstallation: "TEST-selected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _, _ := configureFixture(t)
			profile := filepath.Join(f.root, ".cursor")
			sibling := filepath.Join(f.root, "sibling", ".cursor")
			for _, root := range []string{profile, sibling} {
				if err := os.MkdirAll(root, 0700); err != nil {
					t.Fatal(err)
				}
			}
			recorded := sibling
			if tc.siblingProfile == "selected" {
				recorded = profile
			}
			store := statev2.Store{Path: filepath.Join(filepath.Dir(f.control), "uap", "state", "state-v2.json")}
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
			if err := store.Save(state); err != nil {
				t.Fatal(err)
			}
			req := setupwizard.Request{
				Action: setupwizard.ActionUpdate, Agents: []string{"cursor"},
				ControlRoot: f.control, RuntimeRoot: f.runtime,
				ScopeRoot: profile, CursorConfig: profile, InstallationID: tc.selectedInstallation,
			}
			got, err := composeCursorWizard(setupCommandContext(t), req)
			if err != nil || got.CursorAgentNotify == nil || *got.CursorAgentNotify {
				t.Fatalf("sibling presence contaminated selected Cursor update: %+v %v", got, err)
			}
		})
	}
}
