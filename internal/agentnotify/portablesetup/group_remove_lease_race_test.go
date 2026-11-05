//go:build linux || darwin

package portablesetup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Reproduce a competing install after RemoveGroup reads membership but before
// the coordinator lease. This is the public materializer route, not a mock
// of SDK removal. The TEST callback only controls the ordering.
func TestRemoveGroupSeesClientInstalledWhileWaitingForMutationLease(t *testing.T) {
	ctx := testCtx(t)
	mat, claude, root := registryMaterializerFixture(t)
	claude.Integration = portable.Claude
	claude.OperationID = "TEST-608-initial-claude"
	mat.Roots.ClaudeRunner = listingRunner{configRoot: claude.ClientConfigRoot}
	claudeBinding, err := mat.Install(ctx, claude)
	if err != nil {
		t.Fatalf("install original Claude client: %v", err)
	}

	codex := claude
	codex.Integration = portable.Codex
	codex.ClientConfigRoot = filepath.Join(root, "TEST-608-competing-codex")
	codex.OperationID = "TEST-608-competing-install"
	if err := os.MkdirAll(codex.ClientConfigRoot, 0700); err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(claude.Identity.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	claude.ExpectedGeneration = snap.Ledger.Generation
	codex.ExpectedGeneration = snap.Ledger.Generation

	reached := 0
	var codexBinding portable.Binding
	mat.beforeGroupRemoveMutation = func() {
		reached++
		if reached != 1 {
			t.Fatal("group remove reached its pre-lease seam more than once")
		}
		competitor := mat
		competitor.beforeGroupRemoveMutation = nil
		codexBinding, err = competitor.Install(ctx, codex)
		if err != nil {
			t.Fatalf("competing Codex install: %v", err)
		}
		state, loadErr := mat.Store.Load()
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		installed, ok := findInstallation(state, claude.Identity.InstallationID)
		if !ok || len(installed.Clients) != 2 {
			t.Fatalf("competing installation did not publish both clients: %+v", installed)
		}
	}
	claude.OperationID = "TEST-608-group-remove"
	codex.OperationID = claude.OperationID
	codex.ExternalUninstalled = true // Codex removal has its own explicit attestation.
	removed, err := mat.RemoveGroup(ctx, []MaterializeRequest{claude, codex})
	if reached != 1 {
		t.Fatalf("synchronization seam not exercised: %d, err=%v", reached, err)
	}
	if err != nil {
		t.Fatalf("public group removal failed after competing install: %v", err)
	}
	if len(removed) != 2 || removed[0].AlreadyAbsent || removed[1].AlreadyAbsent {
		t.Fatalf("removed a live client as already absent: %+v", removed)
	}
	state, err := mat.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	installed, ok := findInstallation(state, claude.Identity.InstallationID)
	if !ok || len(installed.Clients) != 0 || !installed.DataRetained {
		t.Fatalf("group removal left installed UAP clients: %+v", installed)
	}
	for _, binding := range []portable.Binding{claudeBinding, codexBinding} {
		locator, err := binding.Filename()
		if err != nil {
			t.Fatal(err)
		}
		lease, err := portable.Acquire(ctx, binding.DataRoot, locator)
		if err == nil {
			lease.Release()
			t.Errorf("removed client %s still has an acquirable locator", binding.Integration)
		}
	}
}
