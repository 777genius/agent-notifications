//go:build linux

package portablesetup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/piauthority"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/pievent"
)

// Exercise the real common engine and installed authority together. No
// hand-written UAP receipt, consumer registration or projected extension.
func TestPiCommonLifecycleAndAuthority(t *testing.T) {
	b, ledger := bindingFixture(t)
	root := filepath.Dir(b.ControlRoot)
	uapRoot := filepath.Join(root, "uap")
	mat, err := NewMaterializer(UAPRoots{StateFile: filepath.Join(uapRoot, "state", "state-v2.json"), LockFile: filepath.Join(uapRoot, "state", "mutation.lock"), OperationsDir: filepath.Join(uapRoot, "state", "operations"), PluginDataBase: filepath.Join(uapRoot, "plugin-data"), ManagedRoot: filepath.Join(uapRoot, "managed"), HelperExecutable: filepath.Join(b.RuntimeRoot, b.Primary)})
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(root, "TEST-pi-agent")
	if err := os.MkdirAll(filepath.Join(profile, "extensions"), 0700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(profile, "extensions", "TEST-foreign.ts")
	if err := os.WriteFile(foreign, []byte("export default () => {};\n"), 0600); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(root, "TEST-pi-package")
	if _, err := portableasset.Build(portableasset.BuildRequest{Version: "1.0.0", GOOS: "linux", GOARCH: "amd64", Executable: filepath.Join(b.RuntimeRoot, b.Primary), OutputRoot: pkg}); err != nil {
		t.Fatal(err)
	}
	req := MaterializeRequest{Identity: Identity{InstallationID: "00000000-0000-4000-8000-000000000541", ComponentID: b.ComponentID, Owner: b.Owner, ScopeRoot: b.ScopeRoot, ControlRoot: b.ControlRoot, GlobalConfig: b.GlobalConfig, RuntimeRoot: b.RuntimeRoot, Primary: b.Primary}, Integration: portable.Pi, ExpectedGeneration: ledger.Generation, PackageRoot: pkg, ClientConfigRoot: profile, ClientExecutable: filepath.Join(b.RuntimeRoot, b.Primary), OperationID: "TEST-pi-install"}

	installed, err := mat.Install(testCtx(t), req)
	if err != nil {
		t.Fatal(err)
	}
	name, _ := installed.Filename()
	selector := filepath.Join(installed.DataRoot, name)
	readRuntime := func() piauthority.Runtime {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(installed.DataRoot, piauthority.RuntimeFile))
		if err != nil {
			t.Fatal(err)
		}
		var facts piauthority.Runtime
		if err := json.Unmarshal(body, &facts); err != nil {
			t.Fatal(err)
		}
		return facts
	}
	facts := readRuntime()
	policy, err := installruntime.ReadPolicySnapshot(testCtx(t), b.ControlRoot)
	if err != nil || policy.Policy.Enabled || policy.Installation.Enabled {
		t.Fatal("Pi registration changed unrelated portable enablement", err)
	}

	gate, binding, cfg, _, err := piauthority.Open(testCtx(t), selector, facts.LoadedRevision)
	if err != nil {
		t.Fatalf("open installed authority: %v", err)
	}
	if cfg == nil || !gate.Channels(testCtx(t), binding, facts.LoadedRevision).Desktop || gate.Channels(testCtx(t), binding, facts.LoadedRevision).Webhook {
		t.Fatal("new Pi channel consent differs from desktop-only default")
	}
	release, err := gate.Acquire(testCtx(t), binding, facts.LoadedRevision, pievent.DesktopChannel)
	if err != nil {
		t.Fatal(err)
	}
	release()
	// Existing registration and derived bytes are idempotent.
	snap, _ := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	req.ExpectedGeneration = snap.Ledger.Generation
	req.OperationID = "TEST-pi-reinstall"
	if _, err := mat.Install(testCtx(t), req); err != nil {
		t.Fatal(err)
	}
	if got := readRuntime(); got != facts {
		t.Fatalf("idempotent metadata changed: %+v", got)
	}
	state, _ := mat.Store.Load()
	installation, _ := findInstallation(state, req.Identity.InstallationID)
	client := installation.Clients[installed.BindingID]
	if filepath.Base(client.TargetLocator)[0] == '.' || filepath.Dir(client.TargetLocator) != filepath.Join(profile, "extensions") {
		t.Fatalf("hidden/wrong active path: %s", client.TargetLocator)
	}
	// A changed canonical source seals a new projection; the live old observer
	// cannot borrow current receipts by rereading mutable derived metadata.
	path := filepath.Join(pkg, "com.universal-agent-plugins.pi", "index.ts")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\n// TEST replacement revision\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	snap, _ = installruntime.ReadInstalledSnapshot(b.ControlRoot)
	req.ExpectedGeneration = snap.Ledger.Generation
	req.OperationID = "TEST-pi-update"
	req.Operation = uapinstaller.OpUpdate
	if _, err := mat.Install(testCtx(t), req); err != nil {
		t.Fatal(err)
	}
	current := readRuntime()
	if current.LoadedRevision == facts.LoadedRevision {
		t.Fatal("update did not replace projection")
	}
	if _, _, _, _, err := piauthority.Open(testCtx(t), selector, facts.LoadedRevision); err == nil {
		t.Fatal("stale observer admitted after update")
	}
	if _, _, _, _, err := piauthority.Open(testCtx(t), selector, current.LoadedRevision); err != nil {
		t.Fatalf("fresh observer denied: %v", err)
	}
	// Missing-directory repair reuses common ownership; removal needs no CLI.
	if err := os.RemoveAll(client.TargetLocator); err != nil {
		t.Fatal(err)
	}
	snap, _ = installruntime.ReadInstalledSnapshot(b.ControlRoot)
	req.ExpectedGeneration = snap.Ledger.Generation
	req.OperationID = "TEST-pi-repair"
	req.Operation = uapinstaller.OpRepair
	if _, err := mat.Install(testCtx(t), req); err != nil {
		t.Fatal(err)
	}
	repaired := readRuntime()
	if _, _, _, _, err := piauthority.Open(testCtx(t), selector, repaired.LoadedRevision); err != nil {
		t.Fatalf("fresh observer denied after native repair: %v", err)
	}
	snap, _ = installruntime.ReadInstalledSnapshot(b.ControlRoot)
	req.ExpectedGeneration = snap.Ledger.Generation
	req.OperationID = "TEST-pi-remove"
	req.ClientExecutable = ""
	req.Operation = ""
	if err := mat.Remove(testCtx(t), req); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := piauthority.Open(testCtx(t), selector, current.LoadedRevision); err == nil {
		t.Fatal("removed observer admitted")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("foreign Pi extension lost: %v", err)
	}
}
