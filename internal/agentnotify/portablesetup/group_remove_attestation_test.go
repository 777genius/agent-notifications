//go:build linux || darwin

package portablesetup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/ports"
)

type groupRemovalRunner struct {
	listingRunner
	codexRemoves int
}

func (r *groupRemovalRunner) Run(ctx context.Context, command ports.Command) (ports.CommandResult, error) {
	if slices.Contains(command.Argv, "remove") {
		r.codexRemoves++
		return ports.CommandResult{}, nil
	}
	return r.listingRunner.Run(ctx, command)
}

func TestMaterializerRemoveGroupKeepsCodexAttestationPerTarget(t *testing.T) {
	for _, codexAttested := range []bool{false, true} {
		name := "claude-only"
		if codexAttested {
			name = "codex-only"
		}
		t.Run(name, func(t *testing.T) {
			mat, claude, root := registryMaterializerFixture(t)
			claude.Integration = portable.Claude
			mat.Roots.ClaudeRunner = listingRunner{configRoot: claude.ClientConfigRoot}
			codex := claude
			codex.Integration = portable.Codex
			codex.ClientConfigRoot = filepath.Join(root, "TEST-attested-codex")
			if err := os.Mkdir(codex.ClientConfigRoot, 0700); err != nil {
				t.Fatal(err)
			}
			if installed, err := mat.ApplyGroup(testCtx(t), []MaterializeRequest{claude, codex}); err != nil || len(installed) != 2 {
				t.Fatalf("materialize group: %+v %v", installed, err)
			}
			// Observe the actual SDK native dispatch boundary with the existing
			// injected-runner fixture; this is not native Codex activation E2E.
			runner := &groupRemovalRunner{listingRunner: listingRunner{configRoot: claude.ClientConfigRoot}}
			mat.Roots.ClaudeRunner = runner
			claude.ExternalUninstalled, codex.ExternalUninstalled = !codexAttested, codexAttested
			claude.OperationID, codex.OperationID = "TEST-group-attestation-remove", "TEST-group-attestation-remove"
			removed, err := mat.RemoveGroup(testCtx(t), []MaterializeRequest{codex, claude})
			if !codexAttested {
				// Host intent/revocation precedes SDK Apply. This refusal prevents
				// native dispatch and UAP removal; it does not promise no host effects.
				var refused ResultError
				if !errors.As(err, &refused) || refused.Result.Outcome != uapinstaller.OutcomeIncomplete || runner.codexRemoves != 0 {
					t.Fatalf("Claude attestation authorized Codex removal: result=%+v calls=%d err=%v", removed, runner.codexRemoves, err)
				}
				state, err := mat.Store.Load()
				if err != nil {
					t.Fatal(err)
				}
				installation, ok := findInstallation(state, codex.Identity.InstallationID)
				if !ok || len(installation.Clients) != 2 {
					t.Fatalf("unattested group removed a client binding: %+v", installation)
				}
			} else if err != nil || len(removed) != 2 || runner.codexRemoves == 0 {
				t.Fatalf("Codex's own attestation was lost: result=%+v calls=%d err=%v", removed, runner.codexRemoves, err)
			}
		})
	}
}
