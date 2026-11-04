//go:build linux || darwin

package setupwizard

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/codex"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/vscode"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/vscodelocalhooks"
)

func TestWizardGroupRecoveryChecksEverySelectedAuthority(t *testing.T) {
	if runtime.GOOS == "darwin" && runtime.GOARCH != "arm64" {
		t.Skip("Local selected delivery has no qualified Darwin Intel tuple")
	}
	for _, drift := range []string{"invalid-local-selection", "public-invalid-local-composition", "replaced-physical-profile"} {
		for _, localFirst := range []bool{false, true} {
			order := "local-last"
			if localFirst {
				order = "local-first"
			}
			t.Run(drift+"/"+order, func(t *testing.T) {
				if drift == "replaced-physical-profile" && runtime.GOOS != "darwin" {
					t.Skip("physical Local profile authority is supported only on Darwin ARM64")
				}
				ctx := testCtx(t)
				control, runtimeRoot, global, primary, generation := managedRuntime(t)
				root := filepath.Dir(control)
				profile, codexHome, pkg := filepath.Join(root, "TEST-local"), filepath.Join(root, "TEST-codex"), filepath.Join(root, "TEST-package")
				for _, path := range []string{profile, codexHome} {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				}
				writePackage(t, pkg, primary)
				tuple := vscode.SourceQualifiedTESTTuple(runtime.GOOS)
				if runtime.GOOS == "darwin" {
					tuple = vscode.QualifiedDarwinTESTTuple()
				}
				cfg := &vscode.LocalConfig{ProfileSettingsPath: filepath.Join(profile, "settings.json"), QualifiedTuple: tuple, TargetShell: vscodelocalhooks.Target{Shell: vscodelocalhooks.Shell(tuple.TargetShell)}, MCPServers: []string{"agent-notify"}, Skills: []string{"agent-notifications"}}
				adapter, err := vscode.NewLocal(*cfg)
				if err != nil {
					t.Fatal(err)
				}
				uapRoot := filepath.Join(root, "uap")
				mat, err := portablesetup.NewMaterializer(portablesetup.UAPRoots{StateFile: filepath.Join(uapRoot, "state", "state-v2.json"), LockFile: filepath.Join(uapRoot, "state", "mutation.lock"), OperationsDir: filepath.Join(uapRoot, "state", "operations"), PluginDataBase: filepath.Join(uapRoot, "plugin-data"), ManagedRoot: filepath.Join(uapRoot, "managed"), HelperExecutable: primary})
				if err != nil {
					t.Fatal(err)
				}
				mat.Registry, err = clients.NewRegistry(adapter, codex.New())
				if err != nil {
					t.Fatal(err)
				}
				snap, err := installruntime.ReadInstalledSnapshot(control)
				if err != nil {
					t.Fatal(err)
				}
				id := portablesetup.Identity{InstallationID: "00000000-0000-4000-8000-000000000239", ComponentID: snap.Ledger.ID, Owner: "existing-installer", ScopeRoot: profile, ControlRoot: control, GlobalConfig: global, RuntimeRoot: runtimeRoot, Primary: "bin/claude-notifications"}
				local := portablesetup.MaterializeRequest{Identity: id, LocalConfig: cfg, Integration: portable.CopilotVSCode, ExpectedGeneration: generation, PackageRoot: pkg, ClientConfigRoot: profile, ClientExecutable: primary, OperationID: "TEST-group-recovery-install"}
				sibling := local
				sibling.Integration, sibling.LocalConfig, sibling.ClientConfigRoot = portable.Codex, nil, codexHome
				sibling.Identity.ScopeRoot = codexHome
				if bindings, err := mat.ApplyGroup(ctx, []portablesetup.MaterializeRequest{local, sibling}); err != nil || len(bindings) != 2 {
					t.Fatalf("install actual Local/Codex group: %+v %v", bindings, err)
				}
				stateBefore, err := os.ReadFile(mat.Roots.StateFile)
				if err != nil {
					t.Fatal(err)
				}
				req := Request{Action: ActionInstall, Agents: []string{"codex", "copilot-vscode"}, Yes: true, Hooks: boolPtr(false), AgentNotify: boolPtr(true), LocalConfig: cfg, PackageRoot: pkg, CodexHome: codexHome, ClientExecutable: primary, Helper: primary, ControlRoot: control, RuntimeRoot: runtimeRoot, GlobalConfig: global, ScopeRoot: profile, Primary: id.Primary, InstallationID: id.InstallationID}
				restore := func() { req.LocalConfig = cfg }
				switch drift {
				case "invalid-local-selection":
					req.LocalConfig = nil
				case "public-invalid-local-composition":
					changed := *cfg
					changed.QualifiedTuple.QualificationID = "TEST-unqualified"
					req.LocalConfig = &changed
				default:
					state, err := mat.Store.Load()
					if err != nil {
						t.Fatal(err)
					}
					captured := false
					for _, binding := range state.Installations[0].Clients {
						if binding.ClientID == portablesetup.ClientID(portable.CopilotVSCode) {
							captured = binding.ProfileAuthority != nil && !binding.ProfileAuthority.IsZero()
						}
					}
					if !captured {
						t.Fatal("installed Local binding lacks actual physical authority")
					}
					original := profile + "-original"
					if err := os.Rename(profile, original); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(profile, 0700); err != nil {
						t.Fatal(err)
					}
					restore = func() {
						if err := os.Remove(profile); err != nil {
							t.Fatal(err)
						}
						if err := os.Rename(original, profile); err != nil {
							t.Fatal(err)
						}
					}
				}
				// A real interrupted kernel transaction is recoverable, but only
				// after every selected client's admission/authority is valid.
				hook := plantWizardKernelJournal(t, ctx, control, runtimeRoot)
				journal := filepath.Join(control, "transaction.json")
				journalBefore, err := os.ReadFile(journal)
				if err != nil {
					t.Fatal(err)
				}
				agents := []portable.Integration{portable.Codex, portable.CopilotVSCode}
				if localFirst {
					agents[0], agents[1] = agents[1], agents[0]
					req.Agents[0], req.Agents[1] = req.Agents[1], req.Agents[0]
				}
				if drift == "public-invalid-local-composition" || drift == "replaced-physical-profile" {
					got, err := Run(ctx, req)
					if err == nil || got.Outcome != "incomplete" {
						t.Fatalf("public Run admitted invalid Local authority: %+v %v", got, err)
					}
				} else if err := recoverWizardJournals(ctx, mat, id, req, agents); err == nil {
					t.Error("group recovery accepted an invalid selected Local authority")
				}
				journalAfter, err := os.ReadFile(journal)
				if err != nil || !bytes.Equal(journalBefore, journalAfter) {
					t.Fatalf("refused group recovery changed pending kernel journal: %v", err)
				}
				if _, err := os.Lstat(hook); !os.IsNotExist(err) {
					t.Fatalf("refused group recovery replayed pending write: %v", err)
				}
				stateAfter, err := os.ReadFile(mat.Roots.StateFile)
				if err != nil || !bytes.Equal(stateBefore, stateAfter) {
					t.Fatalf("refused group recovery changed installed UAP state: %v", err)
				}
				restore()
				if drift == "replaced-physical-profile" {
					if err := verifyWizardKernelRecovery(ctx, req, snap, runtimeRoot, agents); err != nil {
						t.Fatal("public recovery admission with original selected authority", err)
					}
				}
				if err := recoverWizardJournals(ctx, mat, id, req, agents); err != nil {
					t.Fatal("recovery with original selected authority", err)
				}
				if body, err := os.ReadFile(hook); err != nil || string(body) != "new" {
					t.Fatalf("valid recovery did not replay owned pending write: %q %v", body, err)
				}
				if _, err := os.Lstat(journal); !os.IsNotExist(err) {
					t.Fatalf("valid recovery left kernel journal: %v", err)
				}
			})
		}
	}
}
