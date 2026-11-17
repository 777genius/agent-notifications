//go:build linux || darwin

package portablesetup

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/copilotvscodeinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/codex"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/vscode"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/vscodelocalhooks"
)

func TestLocalComposedPackageOwnershipAcrossHandoff(t *testing.T) {
	if runtime.GOOS == "darwin" && runtime.GOARCH != "arm64" {
		t.Skip("Local selected delivery has no qualified Darwin Intel tuple")
	}
	for _, mode := range []string{"single", "group-local-first", "group-local-last"} {
		t.Run(mode, func(t *testing.T) {
			ctx := testCtx(t)
			mat, local, root := registryMaterializerFixture(t)
			local.Integration = portable.CopilotVSCode
			local.Identity.ScopeRoot = local.ClientConfigRoot
			settings := filepath.Join(local.ClientConfigRoot, "settings.json")
			if err := os.WriteFile(settings, []byte(`{"foreign":true}`), 0600); err != nil {
				t.Fatal(err)
			}
			tuple := vscode.SourceQualifiedTESTTuple(runtime.GOOS)
			if runtime.GOOS == "darwin" {
				tuple = vscode.QualifiedDarwinTESTTuple()
			}
			cfg := &vscode.LocalConfig{ProfileSettingsPath: settings, QualifiedTuple: tuple, TargetShell: vscodelocalhooks.Target{Shell: vscodelocalhooks.Shell(tuple.TargetShell)}, NativeStop: true, MCPServers: []string{"agent-notifications"}, Skills: []string{"agent-notifications"}}
			cfg.HookSpecs = copilotvscodeinstall.LocalHookSpecs(portable.Binding{ControlRoot: local.Identity.ControlRoot, BindingID: "TEST-placeholder"}, mat.Roots.HelperExecutable)
			hook, err := vscodelocalhooks.Render(cfg.TargetShell, cfg.HookSpecs)
			if err != nil {
				t.Fatal(err)
			}
			cfg.DeclaredHookDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(hook))
			adapter, err := vscode.NewLocal(*cfg)
			if err != nil {
				t.Fatal(err)
			}
			mat.Registry, err = clients.NewRegistry(adapter, codex.New())
			if err != nil {
				t.Fatal(err)
			}
			local.LocalConfig = cfg
			original := registryFixtureFiles(t, local.PackageRoot)
			if _, err := mat.Install(ctx, local); err != nil {
				t.Fatal("initial Local installation", err)
			}
			state, err := mat.Store.Load()
			if err != nil || len(state.Installations) != 1 {
				t.Fatalf("installed source unavailable: %+v %v", state, err)
			}
			retained := state.Installations[0].Source.CanonicalSource
			retainedFiles := registryFixtureFiles(t, retained)
			acquired := filepath.Join(filepath.Dir(filepath.Dir(mat.Roots.StateFile)), "acquired")
			countSnapshots := func() int {
				t.Helper()
				entries, err := os.ReadDir(acquired)
				if err != nil {
					t.Fatal(err)
				}
				return len(entries)
			}
			if got := countSnapshots(); got != 1 {
				t.Fatalf("initial committed snapshot count: %d", got)
			}
			snap, err := installruntime.ReadInstalledSnapshot(local.Identity.ControlRoot)
			if err != nil {
				t.Fatal(err)
			}
			local.ExpectedGeneration = snap.Ledger.Generation
			sibling := local
			sibling.Integration, sibling.LocalConfig = portable.Codex, nil
			sibling.ClientConfigRoot = filepath.Join(root, "TEST-package-owner-codex")
			sibling.ClientExecutable = buildProbe(t)
			if err := os.MkdirAll(sibling.ClientConfigRoot, 0700); err != nil {
				t.Fatal(err)
			}
			run := func(beforeHandoff bool) error {
				selected, other := local, sibling
				if beforeHandoff {
					// The binding template is validated after the new snapshot is
					// composed, before either recovery or kernel handoff can use it.
					selected.Identity.GlobalConfig, other.Identity.GlobalConfig = "relative", "relative"
				} else {
					// Source confirmation happens after entering the handoff. Keep
					// the snapshot even when the later Prepare refuses this digest.
					selected.TreeDigest = "sha256:" + strings.Repeat("0", 64)
					other.TreeDigest = selected.TreeDigest
				}
				if mode == "single" {
					_, err := mat.Install(ctx, selected)
					return err
				}
				requests := []MaterializeRequest{selected, other}
				if mode == "group-local-last" {
					requests[0], requests[1] = requests[1], requests[0]
				}
				_, err := mat.ApplyGroup(ctx, requests)
				return err
			}
			if err := run(true); err == nil {
				t.Fatal("invalid binding template unexpectedly admitted")
			}
			if got := countSnapshots(); got != 1 {
				t.Fatalf("early refusal retained an unreferenced snapshot: %d", got)
			}
			if err := run(false); !errors.Is(err, ErrSourceIdentityDrift) {
				t.Fatal("expected source refusal after handoff entry", err)
			}
			if got := countSnapshots(); got != 2 {
				t.Fatalf("post-handoff refusal must retain its new snapshot: %d", got)
			}
			if !reflect.DeepEqual(original, registryFixtureFiles(t, local.PackageRoot)) || !reflect.DeepEqual(retainedFiles, registryFixtureFiles(t, retained)) {
				t.Fatal("cleanup changed original or previously committed source")
			}
		})
	}
}
