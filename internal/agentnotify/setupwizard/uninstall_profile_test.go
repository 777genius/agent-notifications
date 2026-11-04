//go:build linux || darwin

package setupwizard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/testenv"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/shared"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
)

func uninstallProfileFixture(t *testing.T) (Request, portablesetup.Materializer, domain.ClientBinding, string) {
	t.Helper()
	control, runtimeRoot, global, _, _ := managedRuntime(t)
	root := filepath.Dir(control)
	probe := buildProbe(t)
	pkg, profile, scope := filepath.Join(root, "TEST-package"), filepath.Join(root, "TEST-codex"), filepath.Join(root, "TEST-scope")
	writePackage(t, pkg, probe)
	for _, path := range []string{profile, scope} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	req := Request{Action: ActionInstall, Agents: []string{"codex"}, Yes: true, Hooks: boolPtr(false), AgentNotify: boolPtr(true),
		PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtimeRoot, GlobalConfig: global, CodexHome: profile,
		ClientExecutable: probe, Helper: probe, ScopeRoot: scope, Primary: portable.PlatformPrimary(),
		InstallationID: "00000000-0000-4000-8000-000000000613"}
	snapshot, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	mat, err := materializer(req, snapshot, runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	// Use the SDK's supported manual activation mode for fixture creation;
	// this test exercises public uninstall admission, not native activation.
	mat.Roots.CodexRunner = nil
	installed, err := mat.Install(testCtx(t), portablesetup.MaterializeRequest{
		Identity: portablesetup.Identity{InstallationID: req.InstallationID, ComponentID: snapshot.Ledger.ID, Owner: snapshot.Ledger.Owner,
			ScopeRoot: scope, ControlRoot: control, GlobalConfig: global, RuntimeRoot: runtimeRoot, Primary: req.Primary},
		Integration: portable.Codex, ExpectedGeneration: snapshot.Ledger.Generation, PackageRoot: pkg,
		ClientConfigRoot: profile, ClientExecutable: probe, OperationID: "TEST-codex-profile-install",
	})
	if err != nil {
		t.Fatalf("materialize Codex fixture: %v", err)
	}
	// Record the same completed-install host fact the wizard records. Native
	// activation is outside this fixture's uninstall admission boundary.
	if err := recordLiveProfile(installed.DataRoot, "codex", profile); err != nil {
		t.Fatal(err)
	}
	bindings, err := liveClientBindings(mat, req.InstallationID, "codex")
	if err != nil || len(bindings) != 1 {
		t.Fatalf("installed Codex binding: %+v %v", bindings, err)
	}
	if ok, err := portable.ExactLocator(installed); err != nil || !ok {
		t.Fatalf("installed Codex locator is not exact: %v %v", ok, err)
	}
	if bindings[0].ClientBindingID == "" || bindings[0].PhysicalArtifact == "" || bindings[0].TargetLocator == "" || bindings[0].NativeProfileRoot != profile {
		t.Fatalf("installed Codex identity/profile facts missing: %+v", bindings[0])
	}
	data, err := bindingDataRoot(mat, req.InstallationID, bindings[0])
	if err != nil || data == "" {
		t.Fatalf("installed data: %q %v", data, err)
	}
	return req, mat, bindings[0], data
}

func clearLegacyCodexProfile(t *testing.T, req Request, mat portablesetup.Materializer, data string) domain.ClientBinding {
	t.Helper()
	if err := os.Remove(filepath.Join(data, liveProfileFile)); err != nil {
		t.Fatal(err)
	}
	state, err := mat.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for i := range state.Installations {
		if state.Installations[i].InstallationID != req.InstallationID {
			continue
		}
		for key, binding := range state.Installations[i].Clients {
			if binding.ClientID != "codex" {
				continue
			}
			// Retain the real legacy identity/artifact, omitting only the newer
			// native-profile field to exercise its supported registry fallback.
			binding.NativeProfileRoot = ""
			state.Installations[i].Clients[key] = binding
			if err := mat.Store.Save(state); err != nil {
				t.Fatal(err)
			}
			return binding
		}
	}
	t.Fatal("installed Codex binding missing")
	return domain.ClientBinding{}
}

func TestUninstallCodexProfileRefusalPrecedesNativeObservation(t *testing.T) {
	for _, scenario := range []string{"missing-wrong-home", "missing-managed-parent-home", "legacy-wrong-home", "legacy-wrong-home-explicit-flag", "recorded-managed-parent-home", "recorded-wrong-home", "malformed-record"} {
		t.Run(scenario, func(t *testing.T) {
			req, mat, binding, data := uninstallProfileFixture(t)
			root := filepath.Dir(req.ControlRoot)
			other := filepath.Join(root, "TEST-other-home")
			if err := os.Mkdir(other, 0700); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(scenario, "legacy-") {
				binding = clearLegacyCodexProfile(t, req, mat, data)
				req.CodexHome = other
			} else if strings.HasPrefix(scenario, "missing-") {
				if err := os.Remove(filepath.Join(data, liveProfileFile)); err != nil {
					t.Fatal(err)
				}
				req.CodexHome = other
				if scenario == "missing-managed-parent-home" {
					req.CodexHome = root
				}
			} else if scenario == "recorded-managed-parent-home" {
				req.CodexHome = root
			} else if scenario == "recorded-wrong-home" {
				if err := recordLiveProfile(data, "codex", other); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(filepath.Join(data, liveProfileFile), []byte("{"), 0600); err != nil {
				t.Fatal(err)
			}
			observed := filepath.Join(root, "TEST-native-observed")
			req.ClientExecutable = testenv.GoFixture(t, fmt.Sprintf(`package main
import ("fmt"; "os"; "strings")
func main() {
 if strings.Join(os.Args[1:], " ") != "plugin list --json" { os.Exit(1) }
 if err := os.WriteFile(%q, []byte(os.Getenv("CODEX_HOME")), 0600); err != nil { panic(err) }
 fmt.Println("{\"installed\":[]}")
}
`, observed))
			req.Action = ActionUninstall
			req.ExternalUninstalled = scenario == "legacy-wrong-home-explicit-flag"
			before := policyConflictFiles(t, root)
			got, err := Run(testCtx(t), req)
			if scenario == "malformed-record" {
				var syntax *json.SyntaxError
				if !errors.As(err, &syntax) {
					t.Errorf("malformed profile cause lost: %+v %v", got, err)
				}
			} else if !errors.Is(err, ErrLiveProfileConflict) || got.Outcome != "conflict" {
				t.Errorf("unproved home accepted: %+v %v", got, err)
			}
			if (strings.HasPrefix(scenario, "missing-") || strings.HasPrefix(scenario, "legacy-")) && (err == nil || !strings.Contains(err.Error(), binding.ClientBindingID) || !strings.Contains(err.Error(), "repair")) {
				t.Errorf("unproved binding lacks repair guidance: %+v %v", got, err)
			}
			if _, err := os.Lstat(observed); !os.IsNotExist(err) {
				t.Error("profile refusal ran the native absence observer")
			}
			if _, err := os.Lstat(portablesetup.IntentPath(req.ControlRoot)); !os.IsNotExist(err) {
				t.Error("profile refusal published an uninstall intent")
			}
			if after := policyConflictFiles(t, root); !reflect.DeepEqual(before, after) {
				t.Fatal("profile refusal changed owned runtime, binding, locator, profiles or native state")
			}
		})
	}
}

func TestUninstallCodexKnownProfileAdmitsObservation(t *testing.T) {
	for _, proof := range []string{"host-profile", "native-profile"} {
		t.Run(proof, func(t *testing.T) {
			req, _, _, data := uninstallProfileFixture(t)
			if proof == "native-profile" {
				if err := os.Remove(filepath.Join(data, liveProfileFile)); err != nil {
					t.Fatal(err)
				}
				// Repair still admits bindings created before the host sidecar.
				// Native activation is outside this read-only admission control.
				repair := req
				repair.Action = ActionRepair
				plan, err := Plan(testCtx(t), repair)
				if err != nil || !plan.Ready {
					t.Fatalf("pre-sidecar repair no longer admitted: %+v %v", plan.Result, err)
				}
			}
			req.Action = ActionUninstall
			req.ClientExecutable = writeCodexListStub(t, filepath.Dir(req.ControlRoot), `{"installed":[]}`)
			// Stop after admission and durable absence attestation, before SDK
			// native removal. Full native lifecycle is a separate platform gate.
			ctx, cancel := context.WithCancel(testCtx(t))
			defer cancel()
			admitted := false
			req.Progress = func(phase string) {
				if phase == "agent-notify" {
					admitted = true
					cancel()
				}
			}
			got, err := Run(ctx, req)
			if !admitted || !errors.Is(err, context.Canceled) {
				t.Fatalf("known Codex profile did not reach removal admission: %+v %v", got, err)
			}
			intent, err := portablesetup.ReadIntent(req.ControlRoot)
			if err != nil || !intent.ExternalUninstalled {
				t.Fatalf("known profile absence was not durably attested: %+v %v", intent, err)
			}
		})
	}
}

func TestUninstallCodexLegacyExactRegistryAdmission(t *testing.T) {
	req, mat, _, data := uninstallProfileFixture(t)
	binding := clearLegacyCodexProfile(t, req, mat, data)
	for _, evidence := range []string{"wrong-name", "wrong-source", "exact"} {
		t.Run(evidence, func(t *testing.T) {
			name, source := shared.ManagedMarketplaceName(binding.PhysicalArtifact), binding.TargetLocator
			if evidence == "wrong-name" {
				name = "foreign-marketplace"
			} else if evidence == "wrong-source" {
				source = filepath.Join(filepath.Dir(source), "foreign-artifact")
			}
			config := fmt.Sprintf("[marketplaces.%s]\nsource = %q\nsource_type = \"local\"\n", name, source)
			if err := os.WriteFile(filepath.Join(req.CodexHome, "config.toml"), []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			before := policyConflictFiles(t, filepath.Dir(req.ControlRoot))
			// Only the exact registry entry proves a legacy profile. Later UAP
			// migration/removal admission remains unchanged.
			err := guardUninstallLiveProfile(mat, req.InstallationID, "codex", req.CodexHome)
			if evidence == "exact" && err != nil {
				t.Fatalf("exact legacy marketplace proof rejected: %v", err)
			}
			if evidence != "exact" && !errors.Is(err, ErrLiveProfileConflict) {
				t.Fatalf("inexact legacy evidence admitted: %v", err)
			}
			if after := policyConflictFiles(t, filepath.Dir(req.ControlRoot)); !reflect.DeepEqual(before, after) {
				t.Fatal("legacy profile admission changed state")
			}
		})
	}
}
