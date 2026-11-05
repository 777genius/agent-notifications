//go:build linux || darwin

package portablesetup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
)

// Group selection excludes both selected clients; only exact live sibling
// facts may be observed by the SDK. A stale sidecar cannot grant authority.
func TestKnownTargetsExcludeAllSelectedGroupClients(t *testing.T) {
	mat, claude, root := registryMaterializerFixture(t)
	claude.Integration = portable.Claude
	mat.Roots.ClaudeRunner = listingRunner{configRoot: claude.ClientConfigRoot}
	codex := claude
	codex.Integration = portable.Codex
	codex.ClientConfigRoot = filepath.Join(root, "TEST-group-known-codex")
	if err := os.MkdirAll(codex.ClientConfigRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := mat.ApplyGroup(testCtx(t), []MaterializeRequest{claude, codex}); err != nil {
		t.Fatal("real group installation:", err)
	}
	all, err := mat.knownTargets(claude.Identity.InstallationID)
	if err != nil || len(all) != 2 {
		t.Fatalf("committed group facts missing: %+v %v", all, err)
	}
	selected, err := mat.knownTargets(claude.Identity.InstallationID, "claude", "codex")
	if err != nil || len(selected) != 0 {
		t.Fatalf("selected clients leaked into compatibility only: %+v %v", selected, err)
	}
	sibling, err := mat.knownTargets(claude.Identity.InstallationID, "codex")
	if err != nil || len(sibling) != 1 || sibling[0].ClientID != "claude" ||
		sibling[0].BindingID == "" || sibling[0].ConfigRoot != claude.ClientConfigRoot {
		t.Fatalf("unselected Claude facts lost: %+v %v", sibling, err)
	}
	if err := recordClientFact(filepath.Dir(mat.Roots.StateFile), clientFact{
		BindingID: "TEST-stale-binding", ConfigRoot: claude.ClientConfigRoot, Executable: claude.ClientExecutable,
	}, "claude"); err != nil {
		t.Fatal(err)
	}
	if stale, err := mat.knownTargets(claude.Identity.InstallationID, "codex"); err != nil || len(stale) != 0 {
		t.Fatalf("stale facts authorized compatibility: %+v %v", stale, err)
	}
}
