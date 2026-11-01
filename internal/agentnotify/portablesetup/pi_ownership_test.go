//go:build linux

package portablesetup

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/piauthority"
	"github.com/777genius/agent-notifications/internal/pievent"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/providers"
)

// Use the real installed lifecycle fixture: excluded ownership markers do not
// alter the snapshot digest, but they must deny the same installed authority.
func assertPiExcludedOwnershipMarkers(t *testing.T, root, active, selector string, facts piauthority.Runtime) {
	t.Helper()
	admit := func(t *testing.T) (*piauthority.Gate, pievent.Binding) {
		t.Helper()
		if err := (providers.Stager{}).Verify(testCtx(t), active, facts.LoadedRevision.ProjectionDigest); err != nil {
			t.Fatalf("common installed verifier denied clean package: %v", err)
		}
		gate, binding, _, _, err := piauthority.Open(testCtx(t), selector, facts.LoadedRevision)
		if err != nil || gate == nil {
			t.Fatalf("clean package Open denied: %v", err)
		}
		if !gate.Channels(testCtx(t), binding, facts.LoadedRevision).Desktop {
			t.Fatal("clean package Channels denied")
		}
		release, err := gate.Acquire(testCtx(t), binding, facts.LoadedRevision, pievent.DesktopChannel)
		if err != nil {
			t.Fatalf("clean package Acquire denied: %v", err)
		}
		release()
		return gate, binding
	}
	gate, binding := admit(t)
	for _, marker := range []string{filepath.Join(".git", "config"), ".uap-integrations.lock", ".plugin-kit-ai.lock"} {
		t.Run("excluded-"+filepath.ToSlash(marker), func(t *testing.T) {
			path := filepath.Join(active, marker)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("TEST foreign ownership marker\n"), 0600); err != nil {
				t.Fatal(err)
			}
			before := registryFixtureFiles(t, root)
			if err := (providers.Stager{}).Verify(testCtx(t), active, facts.LoadedRevision.ProjectionDigest); err == nil {
				t.Error("common installed verifier admitted excluded marker")
			}
			if _, _, _, _, err := piauthority.Open(testCtx(t), selector, facts.LoadedRevision); err == nil {
				t.Error("Open admitted excluded marker")
			}
			if channels := gate.Channels(testCtx(t), binding, facts.LoadedRevision); channels != (pievent.Channels{}) {
				t.Errorf("Channels admitted excluded marker: %+v", channels)
			}
			if release, err := gate.Acquire(testCtx(t), binding, facts.LoadedRevision, pievent.DesktopChannel); err == nil {
				release()
				t.Error("Acquire admitted excluded marker")
			}
			if after := registryFixtureFiles(t, root); !reflect.DeepEqual(after, before) {
				t.Error("denied verification changed fixture bytes, modes or directories")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if filepath.Dir(marker) != "." {
				if err := os.Remove(filepath.Dir(path)); err != nil {
					t.Fatal(err)
				}
			}
			before = registryFixtureFiles(t, root)
			admit(t)
			if after := registryFixtureFiles(t, root); !reflect.DeepEqual(after, before) {
				t.Error("restored verification changed fixture bytes, modes or directories")
			}
		})
	}
}
