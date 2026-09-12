//go:build linux || darwin

package portablesetup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/clientsetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

type fakeUAP struct {
	stage    func(Envelope) (Receipt, error)
	activate func(Receipt) error
	remove   func(string) error
	staged   []Envelope
}

func (f *fakeUAP) Stage(_ context.Context, env Envelope) (Receipt, error) {
	f.staged = append(f.staged, env)
	if f.stage != nil {
		return f.stage(env)
	}
	return Receipt{}, nil
}
func (f *fakeUAP) Activate(_ context.Context, receipt Receipt) error {
	if f.activate != nil {
		return f.activate(receipt)
	}
	return nil
}
func (f *fakeUAP) Remove(_ context.Context, id string) error {
	if f.remove != nil {
		return f.remove(id)
	}
	return nil
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func bindingFixture(t *testing.T) (portable.Binding, installruntime.Ledger) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := portable.Binding{
		Version: 1, Integration: portable.Codex, InstallationID: "uap-install", BindingID: "codex-binding",
		ScopeID: "user", Owner: "existing-installer", ScopeRoot: filepath.Join(root, "scope with spaces"),
		DataRoot: filepath.Join(root, "shared data"), ControlRoot: filepath.Join(root, "control"),
		GlobalConfig: filepath.Join(root, "global", "config.json"), RuntimeRoot: filepath.Join(root, "primary runtime"),
		Primary: "primary",
	}
	for _, p := range []string{b.ScopeRoot, b.DataRoot, filepath.Dir(b.GlobalConfig)} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	r := installruntime.Request{ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Owner: b.Owner, ConsumerID: "existing", Files: []installruntime.File{{Path: filepath.Join(b.RuntimeRoot, b.Primary), Data: []byte("inert primary"), Mode: 0700}}}
	l, err := installruntime.Commit(testCtx(t), r)
	if err != nil {
		t.Fatal(err)
	}
	b.ComponentID = l.ID
	return b, l
}

func TestInstallTwoClientsShareDataAndIndependentLocators(t *testing.T) {
	codex, ledger := bindingFixture(t)
	claude := codex
	claude.Integration = portable.Claude
	claude.BindingID = "claude-binding"
	uap := &fakeUAP{}
	uap.stage = func(env Envelope) (Receipt, error) {
		if len(env.Args) != 3 || env.Args[0] != "portable-launch" || env.Args[1] != "--locator" {
			t.Fatal("locator not injected before materialization")
		}
		name := env.Args[2]
		return Receipt{BindingID: "", DataRoot: codex.DataRoot, LocatorArg: name}, nil
	}
	svc := Service{Stager: uap, Activator: uap, Remover: uap}
	// BindingID must match receipt; specialize per client.
	install := func(b portable.Binding, gen uint64) portable.Binding {
		t.Helper()
		name, err := b.Filename()
		if err != nil {
			t.Fatal(err)
		}
		uap.stage = func(env Envelope) (Receipt, error) {
			if env.Args[2] != name {
				t.Fatalf("stager saw %v want %s", env.Args, name)
			}
			return Receipt{BindingID: b.BindingID, DataRoot: b.DataRoot, LocatorArg: name}, nil
		}
		got, err := svc.Install(testCtx(t), Request{Binding: b, ExpectedGeneration: gen, Envelope: Envelope{Command: "./bin/primary"}})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	codex = install(codex, ledger.Generation)
	snapshot, err := installruntime.ReadInstalledSnapshot(codex.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	claude = install(claude, snapshot.Ledger.Generation)
	codexName, _ := codex.Filename()
	claudeName, _ := claude.Filename()
	if codexName == claudeName {
		t.Fatal("shared data used one locator")
	}
	lease, err := portable.Acquire(testCtx(t), codex.DataRoot, codexName)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	lease, err = portable.Acquire(testCtx(t), claude.DataRoot, claudeName)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	snapshot, err = installruntime.ReadInstalledSnapshot(codex.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Remove(testCtx(t), Request{Binding: claude, ExpectedGeneration: snapshot.Ledger.Generation}); err != nil {
		t.Fatal(err)
	}
	if _, err := portable.Acquire(testCtx(t), claude.DataRoot, claudeName); err == nil {
		t.Fatal("removed locator still acquired")
	}
	if _, err := portable.Acquire(testCtx(t), codex.DataRoot, codexName); err != nil {
		t.Fatal("sibling locator lost")
	}
	if _, err := os.Stat(filepath.Join(codex.RuntimeRoot, codex.Primary)); err != nil {
		t.Fatal("shared runtime removed")
	}
}

func TestInstallRefusesStaleReceiptAndMissingRuntime(t *testing.T) {
	b, ledger := bindingFixture(t)
	uap := &fakeUAP{stage: func(env Envelope) (Receipt, error) {
		return Receipt{BindingID: "other", DataRoot: b.DataRoot, LocatorArg: env.Args[2]}, nil
	}}
	svc := Service{Stager: uap, Activator: uap, Remover: uap}
	if _, err := svc.Install(testCtx(t), Request{Binding: b, ExpectedGeneration: ledger.Generation}); err == nil {
		t.Fatal("foreign receipt accepted")
	}
	b.ControlRoot = filepath.Join(b.ScopeRoot, "missing-control")
	if _, err := svc.Install(testCtx(t), Request{Binding: b, ExpectedGeneration: 1}); err == nil {
		t.Fatal("missing runtime accepted")
	}
}

func TestInstallRemovesOwnedGlobalMCPBeforePortable(t *testing.T) {
	b, ledger := bindingFixture(t)
	config := filepath.Join(filepath.Dir(b.ControlRoot), "client", "config")
	if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := filepath.Join(b.RuntimeRoot, b.Primary)
	setup := clientsetup.Request{
		ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Command: cmd, ConfigPath: config,
		Provider: registration.Codex, Mode: clientsetup.Managed, ExpectedGeneration: ledger.Generation,
	}
	result, err := clientsetup.Apply(testCtx(t), setup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(config); err != nil {
		t.Fatal("owned MCP missing before portable install")
	}
	uap := &fakeUAP{}
	svc := Service{Stager: uap, Activator: uap, Remover: uap}
	name, err := b.Filename()
	if err != nil {
		t.Fatal(err)
	}
	uap.stage = func(env Envelope) (Receipt, error) {
		return Receipt{BindingID: b.BindingID, DataRoot: b.DataRoot, LocatorArg: name}, nil
	}
	if _, err := svc.Install(testCtx(t), Request{
		Binding: b, ExpectedGeneration: result.Ledger.Generation,
		Discovery: Discovery{ConfigPath: config, Command: cmd},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := portable.Acquire(testCtx(t), b.DataRoot, name); err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := clientsetup.Inspect(testCtx(t), clientsetup.Request{
		ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Command: cmd, ConfigPath: config,
		Provider: registration.Codex, Mode: clientsetup.Managed, ExpectedGeneration: snap.Ledger.Generation,
	})
	if err != nil || facts.Registered {
		t.Fatalf("owned MCP survived portable handoff: %+v %v", facts, err)
	}
}

func TestHandoffReverseRefusesWhileLocatorExistsThenRestoresOwnedMCP(t *testing.T) {
	b, ledger := bindingFixture(t)
	config := filepath.Join(filepath.Dir(b.ControlRoot), "client", "config")
	if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := filepath.Join(b.RuntimeRoot, b.Primary)
	setup := clientsetup.Request{
		ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Command: cmd, ConfigPath: config,
		Provider: registration.Codex, Mode: clientsetup.Managed, ExpectedGeneration: ledger.Generation,
	}
	result, err := clientsetup.Apply(testCtx(t), setup)
	if err != nil {
		t.Fatal(err)
	}
	uap := &fakeUAP{}
	svc := Service{Stager: uap, Activator: uap, Remover: uap}
	name, err := b.Filename()
	if err != nil {
		t.Fatal(err)
	}
	uap.stage = func(env Envelope) (Receipt, error) {
		return Receipt{BindingID: b.BindingID, DataRoot: b.DataRoot, LocatorArg: name}, nil
	}
	discovery := Discovery{ConfigPath: config, Command: cmd}
	if _, err := svc.Install(testCtx(t), Request{Binding: b, ExpectedGeneration: result.Ledger.Generation, Discovery: discovery}); err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.HandoffReverse(testCtx(t), Request{Binding: b, ExpectedGeneration: snap.Ledger.Generation, Discovery: discovery}); err == nil {
		t.Fatal("reverse handoff ran while portable locator existed")
	}
	facts, err := clientsetup.Inspect(testCtx(t), clientsetup.Request{
		ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Command: cmd, ConfigPath: config,
		Provider: registration.Codex, Mode: clientsetup.Managed, ExpectedGeneration: snap.Ledger.Generation,
	})
	if err != nil || facts.Registered {
		t.Fatalf("global MCP restored while portable still owned: %+v %v", facts, err)
	}
	if err := svc.Remove(testCtx(t), Request{Binding: b, ExpectedGeneration: snap.Ledger.Generation, Discovery: discovery}); err != nil {
		t.Fatal(err)
	}
	if _, err := portable.Acquire(testCtx(t), b.DataRoot, name); err == nil {
		t.Fatal("locator survived reverse remove")
	}
	snap, err = installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	facts, err = clientsetup.Inspect(testCtx(t), clientsetup.Request{
		ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Command: cmd, ConfigPath: config,
		Provider: registration.Codex, Mode: clientsetup.Managed, ExpectedGeneration: snap.Ledger.Generation,
	})
	if err != nil || !facts.Registered {
		t.Fatalf("owned MCP not restored after portable removal: %+v %v", facts, err)
	}
}

func TestInstallRefusesUnownedDiscoveryConflict(t *testing.T) {
	b, ledger := bindingFixture(t)
	config := filepath.Join(filepath.Dir(b.ControlRoot), "client", "foreign.json")
	if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"mcpServers":{"other":{"command":"/bin/false"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	uap := &fakeUAP{stage: func(env Envelope) (Receipt, error) {
		t.Fatal("stager ran after unowned conflict")
		return Receipt{}, nil
	}}
	svc := Service{Stager: uap, Activator: uap, Remover: uap}
	if _, err := svc.Install(testCtx(t), Request{
		Binding: b, ExpectedGeneration: ledger.Generation,
		Discovery: Discovery{ConfigPath: config, Command: filepath.Join(b.RuntimeRoot, b.Primary)},
	}); err == nil {
		t.Fatal("unowned MCP conflict accepted")
	}
	name, _ := b.Filename()
	if _, err := portable.Acquire(testCtx(t), b.DataRoot, name); err == nil {
		t.Fatal("locator published after refused handoff")
	}
}

func TestCommitBindingPublishFailureRemovesOnlyNewConsumer(t *testing.T) {
	b, ledger := bindingFixture(t)
	key, _, _, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	name, err := b.Filename()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.DataRoot, name), []byte(`{"conflict":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Service{}).CommitBinding(testCtx(t), Request{Binding: b, ExpectedGeneration: ledger.Generation}); err == nil {
		t.Fatal("conflicting locator accepted")
	}
	snap, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Ledger.Consumers[key]; ok {
		t.Fatal("new consumer survived failed publish")
	}
	if _, ok := snap.Ledger.Consumers["existing"]; !ok {
		t.Fatal("unrelated consumer removed during publish compensation")
	}
}

func TestCommitBindingPublishFailureKeepsExistingConsumer(t *testing.T) {
	b, ledger := bindingFixture(t)
	svc := Service{}
	if _, err := svc.CommitBinding(testCtx(t), Request{Binding: b, ExpectedGeneration: ledger.Generation}); err != nil {
		t.Fatal(err)
	}
	key, _, _, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	name, err := b.Filename()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.DataRoot, name), []byte(`{"conflict":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CommitBinding(testCtx(t), Request{Binding: b, ExpectedGeneration: snap.Ledger.Generation}); err == nil {
		t.Fatal("conflicting locator accepted")
	}
	snap, err = installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Ledger.Consumers[key]; !ok {
		t.Fatal("publish failure unregistered existing portable consumer")
	}
}
