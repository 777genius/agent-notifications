//go:build linux || darwin

package portablesetup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/claude"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/codex"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/ports"
)

type finalizationRunner struct {
	listingRunner
	afterInstalled func()
}

func (r finalizationRunner) Run(ctx context.Context, command ports.Command) (ports.CommandResult, error) {
	result, err := r.listingRunner.Run(ctx, command)
	if err == nil && string(result.Stdout) != "[]" {
		r.afterInstalled()
	}
	return result, err
}

// The callback runs in the actual UAP activation lifecycle. Its filesystem
// fault affects host finalization only, after committed binding publication.
func TestMaterializerInstallFinalizationPreservesCommittedResults(t *testing.T) {
	for _, group := range []bool{false, true} {
		name := "single"
		if group {
			name = "group"
		}
		t.Run(name, func(t *testing.T) {
			mat, req, root := registryMaterializerFixture(t)
			req.Integration = portable.Claude
			factsPath := clientFactsPath(filepath.Dir(mat.Roots.StateFile))
			injected := false
			mat.Roots.ClaudeRunner = finalizationRunner{listingRunner{configRoot: req.ClientConfigRoot}, func() {
				if injected {
					return
				}
				if err := os.Mkdir(factsPath, 0700); err != nil {
					t.Fatal(err)
				}
				injected = true
			}}
			var err error
			if group {
				sibling := req
				sibling.Integration = portable.Codex
				sibling.ClientConfigRoot = filepath.Join(root, "TEST-codex-profile")
				if e := os.Mkdir(sibling.ClientConfigRoot, 0700); e != nil {
					t.Fatal(e)
				}
				_, err = mat.ApplyGroup(testCtx(t), []MaterializeRequest{req, sibling})
			} else {
				_, err = mat.Install(testCtx(t), req)
			}
			if !injected {
				t.Fatalf("activation callback not reached: %v", err)
			}
			state, readErr := mat.Store.Load()
			if readErr != nil {
				t.Fatal(readErr)
			}
			installed, ok := findInstallation(state, req.Identity.InstallationID)
			count := 1
			if group {
				count = 2
			}
			if !ok || len(installed.Clients) != count {
				t.Fatalf("committed clients: %+v", installed.Clients)
			}
			var persisted ResultError
			if !errors.As(err, &persisted) || !persisted.Finalization {
				t.Fatalf("committed %d clients but result lost: %v", count, err)
			}
			var pathErr *os.PathError
			if !errors.As(err, &pathErr) || pathErr.Path != factsPath {
				t.Fatalf("finalization cause lost: %v", err)
			}
			if persisted.Result.Outcome != uapinstaller.OutcomeCompleted {
				t.Fatalf("committed outcome: %+v", persisted.Result)
			}
			clients := persisted.Result.Targets
			if !group {
				clients = []uapinstaller.ClientResult{persisted.Result.Client}
			}
			if len(clients) != count {
				t.Fatalf("committed results: %+v", clients)
			}
			for _, client := range clients {
				if client.Materialization != string(domain.MaterializationMaterialized) {
					t.Fatalf("materialization lost: %+v", client)
				}
			}
		})
	}
}

func TestMaterializerRemoveFinalizationPreservesAbsentResult(t *testing.T) {
	mat, req, root := registryMaterializerFixture(t)
	req.Integration = portable.Claude
	mat.Roots.ClaudeRunner = listingRunner{configRoot: req.ClientConfigRoot}
	if _, err := mat.Install(testCtx(t), req); err != nil {
		t.Fatal(err)
	}
	req.OperationID = "TEST-finalization-remove"
	// Reverse discovery has a real post-removal admission failure: no owned
	// command can be restored. It must not hide the completed UAP removal.
	req.Discovery = Discovery{ConfigPath: filepath.Join(root, "TEST-reverse.json")}
	err := mat.Remove(testCtx(t), req)
	state, readErr := mat.Store.Load()
	if readErr != nil {
		t.Fatal(readErr)
	}
	installed, ok := findInstallation(state, req.Identity.InstallationID)
	if !ok || len(installed.Clients) != 0 || !installed.DataRetained {
		t.Fatalf("removal not committed: %+v", installed)
	}
	var persisted ResultError
	if !errors.As(err, &persisted) || !persisted.Finalization {
		t.Fatalf("removal committed but result lost: %v", err)
	}
	if persisted.Result.Operation != uapinstaller.OpRemove || persisted.Result.Outcome != uapinstaller.OutcomeCompleted ||
		!persisted.Result.DataRetained || persisted.Result.Binding.ClientID != "claude" || persisted.Result.Client.ClientID != "claude" {
		t.Fatalf("completed removal facts lost: %+v", persisted.Result)
	}
}

type finalizationClaudeAdapter struct {
	*claude.Adapter
	afterDeactivate func()
}

func (a finalizationClaudeAdapter) Deactivate(ctx context.Context, env clients.Env, req domain.DeactivationRequest) (domain.DeactivationOutcome, error) {
	result, err := a.Adapter.Deactivate(ctx, env, req)
	if err == nil {
		a.afterDeactivate()
	}
	return result, err
}

func TestMaterializerGroupRemoveFinalizationPreservesAbsentResults(t *testing.T) {
	mat, req, root := registryMaterializerFixture(t)
	req.Integration = portable.Claude
	mat.Roots.ClaudeRunner = listingRunner{configRoot: req.ClientConfigRoot}
	sibling := req
	sibling.Integration = portable.Codex
	sibling.ClientConfigRoot = filepath.Join(root, "TEST-codex-profile")
	if err := os.Mkdir(sibling.ClientConfigRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := mat.ApplyGroup(testCtx(t), []MaterializeRequest{req, sibling}); err != nil {
		t.Fatal(err)
	}
	// The real adapter deactivates normally. Damage only the host intent,
	// which UAP does not own, so its removal can commit before host finalization
	// refuses to clear a path that is no longer the recorded regular file.
	fault := IntentPath(req.Identity.ControlRoot)
	injected := false
	var err error
	mat.Registry, err = clients.NewRegistry(finalizationClaudeAdapter{claude.New(), func() {
		if err := os.Remove(fault); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(fault, 0700); err != nil {
			t.Fatal(err)
		}
		injected = true
	}}, codex.New())
	if err != nil {
		t.Fatal(err)
	}
	req.OperationID = "TEST-finalization-group-remove"
	sibling.OperationID = req.OperationID
	sibling.ExternalUninstalled = true
	_, err = mat.RemoveGroup(testCtx(t), []MaterializeRequest{req, sibling})
	if !injected {
		t.Fatalf("native deactivation callback not reached: %v", err)
	}
	state, readErr := mat.Store.Load()
	if readErr != nil {
		t.Fatal(readErr)
	}
	installed, ok := findInstallation(state, req.Identity.InstallationID)
	if !ok || len(installed.Clients) != 0 || !installed.DataRetained {
		t.Fatalf("group removal not committed: %+v", installed)
	}
	var persisted ResultError
	if !errors.As(err, &persisted) || !persisted.Finalization {
		t.Fatalf("group removal committed but result lost: %v", err)
	}
	if persisted.Result.Operation != uapinstaller.OpRemove || persisted.Result.Outcome != uapinstaller.OutcomeCompleted ||
		!persisted.Result.DataRetained || len(persisted.Result.Targets) != 2 {
		t.Fatalf("completed group removal facts lost: %+v", persisted.Result)
	}
	seen := map[string]bool{}
	for _, target := range persisted.Result.Targets {
		// The pinned SDK identifies each removed target but leaves its
		// materialization empty. Preserve those exact facts for the caller.
		if (target.ClientID != "claude" && target.ClientID != "codex") || seen[target.ClientID] || target.BindingID == "" || target.Materialization != "" {
			t.Fatalf("raw group removal result changed: %+v", target)
		}
		seen[target.ClientID] = true
	}
	if info, statErr := os.Stat(fault); statErr != nil || !info.IsDir() {
		t.Fatalf("unexpected repair of foreign intent path: %v", statErr)
	}
}

// The public activation progress callback runs after each target's saved
// activation result. A foreign unreadable journal then makes final Inspect
// fail while leaving both committed clients observable in the state store.
func TestMaterializerGroupFinalInspectionFailurePreservesResults(t *testing.T) {
	mat, req, root := registryMaterializerFixture(t)
	req.Integration = portable.Claude
	mat.Roots.ClaudeRunner = listingRunner{configRoot: req.ClientConfigRoot}
	sibling := req
	sibling.Integration = portable.Codex
	sibling.ClientConfigRoot = filepath.Join(root, "TEST-codex-profile")
	if err := os.Mkdir(sibling.ClientConfigRoot, 0700); err != nil {
		t.Fatal(err)
	}
	completed := 0
	fault := filepath.Join(mat.Roots.OperationsDir, "TEST-unreadable.json")
	mat.Progress = func(_, phase, outcome string) {
		if phase != "verification" || outcome != "done" {
			return
		}
		completed++
		if completed == 2 {
			if err := os.WriteFile(fault, []byte("{"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, err := mat.ApplyGroup(testCtx(t), []MaterializeRequest{req, sibling})
	if completed != 2 {
		t.Fatalf("both activations not completed: %d %v", completed, err)
	}
	state, readErr := mat.Store.Load()
	if readErr != nil {
		t.Fatal(readErr)
	}
	installed, ok := findInstallation(state, req.Identity.InstallationID)
	if !ok || len(installed.Clients) != 2 {
		t.Fatalf("committed clients lost: %+v", installed.Clients)
	}
	var persisted ResultError
	if !errors.As(err, &persisted) || !persisted.Finalization || !strings.Contains(err.Error(), filepath.Base(fault)) {
		t.Fatalf("final inspection error not retained: %v", err)
	}
	if persisted.Result.Outcome != uapinstaller.OutcomeCompleted || len(persisted.Result.Targets) != 2 {
		t.Fatalf("committed group result lost: %+v", persisted.Result)
	}
	if _, err := os.Stat(fault); err != nil {
		t.Fatalf("unexpected repair of foreign journal: %v", err)
	}
}

func TestGroupFinalInspectionMatchesSelectedBindingAndPreservesSiblings(t *testing.T) {
	_, req, root := registryMaterializerFixture(t)
	req.Integration = portable.Claude
	selected, err := Complete(req.Identity, req.Integration, "claude", "user", filepath.Join(root, "selected"), filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := Complete(req.Identity, req.Integration, "claude", "user", filepath.Join(root, "other-profile"), filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	selectedFact := uapinstaller.InspectedBinding{ClientID: "claude", BindingID: selected.BindingID, Scope: "user", TargetPath: filepath.Join(root, "selected"), DataRoot: selected.DataRoot}
	siblingFact := selectedFact
	siblingFact.BindingID, siblingFact.TargetPath = other.BindingID, filepath.Join(root, "other-profile")
	view := uapinstaller.Inspection{Installations: []uapinstaller.InspectedInstallation{{InstallationID: req.Identity.InstallationID, Bindings: []uapinstaller.InspectedBinding{selectedFact, siblingFact}}}}
	result := uapinstaller.Result{InstallationID: req.Identity.InstallationID, Targets: []uapinstaller.ClientResult{{ClientID: "claude", BindingID: selected.BindingID}}}
	got, err := installedGroupBindings([]MaterializeRequest{req}, result, view)
	if err != nil || len(got) != 1 || got[0] != selected {
		t.Fatalf("selected binding confused with sibling: %+v %v", got, err)
	}
	view.Installations[0].Bindings = []uapinstaller.InspectedBinding{siblingFact}
	if _, err := installedGroupBindings([]MaterializeRequest{req}, result, view); !errors.Is(err, ErrPreflight) {
		t.Fatalf("missing selected binding substituted sibling: %v", err)
	}
	view.Installations[0].Bindings = []uapinstaller.InspectedBinding{selectedFact, selectedFact}
	if _, err := installedGroupBindings([]MaterializeRequest{req}, result, view); !errors.Is(err, ErrPreflight) {
		t.Fatalf("duplicate selected identity accepted: %v", err)
	}
}
