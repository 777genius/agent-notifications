//go:build linux || darwin

package portablesetup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestPreflightRefusesMissingOrLinkedPrimaryBeforeCommit(t *testing.T) {
	for _, scenario := range []string{"missing", "unowned-symlink", "owned-symlink"} {
		t.Run(scenario, func(t *testing.T) {
			b, _ := bindingFixture(t)
			if scenario == "owned-symlink" {
				primary := filepath.Join(b.RuntimeRoot, b.Primary)
				if err := os.Rename(primary, filepath.Join(b.RuntimeRoot, "moved-primary")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("moved-primary", primary); err != nil {
					t.Fatal(err)
				}
			} else {
				b.Primary = "unowned"
			}
			if scenario == "unowned-symlink" {
				if err := os.Symlink("primary", filepath.Join(b.RuntimeRoot, b.Primary)); err != nil {
					t.Fatal(err)
				}
			}
			if err := (Service{}).preflight(b); !errors.Is(err, ErrPreflight) {
				t.Fatalf("unsafe primary accepted: %v", err)
			}
		})
	}
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
	if snap.Ledger.PendingMutation != nil || snap.Ledger.WriterFloor != installruntime.ReservationWriterFloor || snap.Ledger.Schema != 3 {
		t.Fatalf("handoff did not terminalize reservation: %+v", snap.Ledger)
	}
	if _, err := os.Lstat(IntentPath(b.ControlRoot)); !os.IsNotExist(err) {
		t.Fatal("intent retained after successful handoff")
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

func TestCommitBindingSameBindingDoesNotBumpGeneration(t *testing.T) {
	b, ledger := bindingFixture(t)
	svc := Service{}
	if _, err := svc.CommitBinding(testCtx(t), Request{Binding: b, ExpectedGeneration: ledger.Generation}); err != nil {
		t.Fatal(err)
	}
	first, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	key, _, _, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := first.Ledger.Consumers[key]; !ok {
		t.Fatal("first commit omitted portable consumer")
	}
	if _, err := svc.CommitBinding(testCtx(t), Request{Binding: b, ExpectedGeneration: first.Ledger.Generation}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CommitBinding(testCtx(t), Request{Binding: b, ExpectedGeneration: ledger.Generation}); err != nil {
		t.Fatal(err)
	}
	second, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if second.Ledger.Generation != first.Ledger.Generation {
		t.Fatalf("duplicate commit bumped generation %d -> %d", first.Ledger.Generation, second.Ledger.Generation)
	}
	if _, ok := second.Ledger.Consumers[key]; !ok {
		t.Fatal("duplicate commit dropped portable consumer")
	}
	if _, ok := second.Ledger.Consumers["existing"]; !ok {
		t.Fatal("duplicate commit dropped unrelated consumer")
	}
	name, err := b.Filename()
	if err != nil {
		t.Fatal(err)
	}
	lease, err := portable.Acquire(testCtx(t), b.DataRoot, name)
	if err != nil {
		t.Fatalf("locator after duplicate commit: %v", err)
	}
	lease.Release()
}

func TestRefuseConflictingLocatorIgnoresPathMatch(t *testing.T) {
	b, ledger := bindingFixture(t)
	name, err := b.Filename()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(b.DataRoot, name)
	if err := os.WriteFile(path, []byte(`{"conflict":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := refuseConflictingLocator(b); err == nil || !errors.Is(err, ErrPreflight) {
		t.Fatalf("conflicting locator accepted: %v", err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Ledger.Generation != ledger.Generation {
		t.Fatalf("preflight bumped generation %d -> %d", ledger.Generation, snap.Ledger.Generation)
	}
	key, _, raw, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Ledger.Consumers[key]; ok {
		t.Fatal("preflight committed portable consumer")
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := refuseConflictingLocator(b); err != nil {
		t.Fatalf("matching locator refused: %v", err)
	}
}

func ownedMCP(t *testing.T, b portable.Binding, ledger installruntime.Ledger) (string, string, installruntime.Ledger) {
	t.Helper()
	config := filepath.Join(filepath.Dir(b.ControlRoot), "client", "config")
	if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := filepath.Join(b.RuntimeRoot, b.Primary)
	result, err := clientsetup.Apply(testCtx(t), clientsetup.Request{
		ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Command: cmd, ConfigPath: config,
		Provider: registration.Codex, Mode: clientsetup.Managed, ExpectedGeneration: ledger.Generation,
	})
	if err != nil {
		t.Fatal(err)
	}
	return config, cmd, result.Ledger
}

func TestHandoffReservationIsPublishedBeforeDirectRemove(t *testing.T) {
	b, ledger := bindingFixture(t)
	config, cmd, ledger := ownedMCP(t, b, ledger)
	svc := Service{}
	req := Request{Binding: b, ExpectedGeneration: ledger.Generation, Discovery: Discovery{ConfigPath: config, Command: cmd}}
	published, res, err := svc.publishHandoffReservation(testCtx(t), req, ledger.Generation)
	if err != nil || res == nil {
		t.Fatalf("publish: %+v %v", res, err)
	}
	if _, err := os.Stat(config); err != nil {
		t.Fatal("direct MCP removed before reservation")
	}
	if published.PendingMutation == nil || published.WriterFloor != installruntime.ReservationWriterFloor {
		t.Fatalf("reservation not durable: %+v", published)
	}
	if _, err := os.Lstat(IntentPath(b.ControlRoot)); err != nil {
		t.Fatal("intent missing before direct remove")
	}
	extra := filepath.Join(b.RuntimeRoot, "hijack")
	if _, err := installruntime.Commit(testCtx(t), installruntime.Request{
		ControlRoot: b.ControlRoot, Owner: b.Owner, RuntimeRoot: b.RuntimeRoot, ConsumerID: "hijack",
		Files: []installruntime.File{{Path: extra, Data: []byte("x"), Mode: 0700}}, ExpectedGeneration: &published.Generation,
	}); !errors.Is(err, installruntime.ErrReservationConflict) {
		t.Fatalf("unmatched writer during reservation: %v", err)
	}
	uap := &fakeUAP{}
	name, err := b.Filename()
	if err != nil {
		t.Fatal(err)
	}
	uap.stage = func(Envelope) (Receipt, error) {
		return Receipt{BindingID: b.BindingID, DataRoot: b.DataRoot, LocatorArg: name}, nil
	}
	svc.Stager, svc.Activator, svc.Remover = uap, uap, uap
	if _, err := svc.Install(testCtx(t), Request{
		Binding: b, ExpectedGeneration: ledger.Generation, Discovery: req.Discovery,
	}); err != nil {
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
		t.Fatalf("resume did not retire direct MCP: %+v %v", facts, err)
	}
	if snap.Ledger.PendingMutation != nil {
		t.Fatal("reservation survived resumed install")
	}
}

func TestPendingInstallReservationConflictsWithRemove(t *testing.T) {
	b, ledger := bindingFixture(t)
	config, cmd, ledger := ownedMCP(t, b, ledger)
	svc := Service{Remover: &fakeUAP{}}
	req := Request{Binding: b, ExpectedGeneration: ledger.Generation, Discovery: Discovery{ConfigPath: config, Command: cmd}}
	published, res, err := svc.publishHandoffReservation(testCtx(t), req, ledger.Generation)
	if err != nil || res == nil || published.PendingMutation == nil {
		t.Fatalf("publish: %+v %v", res, err)
	}
	if err := svc.Remove(testCtx(t), Request{
		Binding: b, ExpectedGeneration: published.Generation, Discovery: req.Discovery,
	}); !errors.Is(err, ErrIntentConflict) {
		t.Fatalf("remove during install reservation: %v", err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil || snap.Ledger.PendingMutation == nil {
		t.Fatalf("conflict consumed install reservation: %+v %v", snap.Ledger.PendingMutation, err)
	}
	if _, err := os.Stat(config); err != nil {
		t.Fatal("conflict retired direct MCP")
	}
}

func TestPendingUpdateReservationRetiresOwnedDiscovery(t *testing.T) {
	b, ledger := bindingFixture(t)
	config, cmd, ledger := ownedMCP(t, b, ledger)
	svc := Service{}
	req := Request{
		Binding: b, ExpectedGeneration: ledger.Generation,
		Discovery:  Discovery{ConfigPath: config, Command: cmd},
		TreeDigest: "tree-update", HelperDigest: "helper-update", HelperVersion: "1.44.1",
	}
	published, reservation, err := svc.publishIntent(testCtx(t), req, ledger.Generation, "update", "confirmed", []string{"agent-notify"})
	if err != nil || reservation == nil {
		t.Fatalf("publish update intent: %+v %v", reservation, err)
	}
	req.ExpectedGeneration = published.Generation
	if _, _, err := svc.handoffForward(testCtx(t), req, "install"); !errors.Is(err, ErrIntentConflict) {
		t.Fatalf("different action reused update reservation: %v", err)
	}
	gen, got, err := svc.handoffForward(testCtx(t), req, "update")
	if err != nil || got == nil || got.ID != reservation.ID || gen <= published.Generation {
		t.Fatalf("update handoff: generation=%d reservation=%+v err=%v", gen, got, err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil || snap.Ledger.PendingMutation == nil {
		t.Fatalf("update reservation was lost: %+v %v", snap.Ledger.PendingMutation, err)
	}
	facts, err := clientsetup.Inspect(testCtx(t), clientsetup.Request{
		ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Command: cmd, ConfigPath: config,
		Provider: registration.Codex, Mode: clientsetup.Managed, ExpectedGeneration: snap.Ledger.Generation,
	})
	if err != nil || facts.Registered {
		t.Fatalf("owned direct MCP remained after update handoff: %+v %v", facts, err)
	}
}

func TestPendingInstallForOtherClientConflicts(t *testing.T) {
	b, ledger := bindingFixture(t)
	config, cmd, ledger := ownedMCP(t, b, ledger)
	svc := Service{}
	req := Request{Binding: b, ExpectedGeneration: ledger.Generation, Discovery: Discovery{ConfigPath: config, Command: cmd}}
	if _, _, err := svc.publishHandoffReservation(testCtx(t), req, ledger.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.matchingReservation(req, "install"); err != nil {
		t.Fatal(err)
	}
	req.Binding.Integration = portable.Claude
	if _, err := svc.matchingReservation(req, "install"); !errors.Is(err, ErrIntentConflict) {
		t.Fatalf("other client: %v", err)
	}
}

func TestPendingInstallDifferentDigestConflicts(t *testing.T) {
	b, ledger := bindingFixture(t)
	config, cmd, ledger := ownedMCP(t, b, ledger)
	svc := Service{}
	req := Request{
		Binding: b, ExpectedGeneration: ledger.Generation, Discovery: Discovery{ConfigPath: config, Command: cmd},
		SourceDigest: strings.Repeat("a", 64),
	}
	if _, _, err := svc.publishHandoffReservation(testCtx(t), req, ledger.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.matchingReservation(req, "install"); err != nil {
		t.Fatal(err)
	}
	omitted := req
	omitted.SourceDigest = ""
	if _, err := svc.matchingReservation(omitted, "install"); err != nil {
		t.Fatalf("omitted digest: %v", err)
	}
	req.SourceDigest = strings.Repeat("b", 64)
	if _, err := svc.matchingReservation(req, "install"); !errors.Is(err, ErrIntentConflict) {
		t.Fatalf("different digest: %v", err)
	}
}

func TestPendingInstallDifferentTreeDigestConflicts(t *testing.T) {
	b, ledger := bindingFixture(t)
	config, cmd, ledger := ownedMCP(t, b, ledger)
	svc := Service{}
	req := Request{
		Binding: b, ExpectedGeneration: ledger.Generation, Discovery: Discovery{ConfigPath: config, Command: cmd},
		SourceDigest: strings.Repeat("a", 64), TreeDigest: "tree-a",
	}
	if _, _, err := svc.publishHandoffReservation(testCtx(t), req, ledger.Generation); err != nil {
		t.Fatal(err)
	}
	intent, err := ReadIntent(b.ControlRoot)
	if err != nil || intent.TreeDigest != "tree-a" || intent.SourceDigest != req.SourceDigest {
		t.Fatalf("intent omitted tree digest: %+v %v", intent, err)
	}
	if _, err := svc.matchingReservation(req, "install"); err != nil {
		t.Fatal(err)
	}
	omitted := req
	omitted.TreeDigest = ""
	if _, err := svc.matchingReservation(omitted, "install"); err != nil {
		t.Fatalf("omitted tree digest: %v", err)
	}
	req.TreeDigest = "tree-b"
	if _, err := svc.matchingReservation(req, "install"); !errors.Is(err, ErrIntentConflict) {
		t.Fatalf("different tree digest: %v", err)
	}
}

func TestPendingInstallDifferentHelperDigestConflicts(t *testing.T) {
	b, ledger := bindingFixture(t)
	config, cmd, ledger := ownedMCP(t, b, ledger)
	svc := Service{}
	req := Request{
		Binding: b, ExpectedGeneration: ledger.Generation, Discovery: Discovery{ConfigPath: config, Command: cmd},
		HelperDigest: "helper-a", HelperVersion: "1.43.0",
	}
	if _, _, err := svc.publishHandoffReservation(testCtx(t), req, ledger.Generation); err != nil {
		t.Fatal(err)
	}
	intent, err := ReadIntent(b.ControlRoot)
	if err != nil || intent.HelperDigest != "helper-a" || intent.HelperVersion != "1.43.0" {
		t.Fatalf("intent omitted helper identity: %+v %v", intent, err)
	}
	if _, err := svc.matchingReservation(req, "install"); err != nil {
		t.Fatal(err)
	}
	omitted := req
	omitted.HelperDigest = ""
	if _, err := svc.matchingReservation(omitted, "install"); err != nil {
		t.Fatalf("omitted helper digest: %v", err)
	}
	req.HelperDigest = "helper-b"
	if _, err := svc.matchingReservation(req, "install"); !errors.Is(err, ErrIntentConflict) {
		t.Fatalf("different helper digest: %v", err)
	}
}

func TestPendingInstallDifferentHelperVersionConflicts(t *testing.T) {
	b, ledger := bindingFixture(t)
	config, cmd, ledger := ownedMCP(t, b, ledger)
	svc := Service{}
	req := Request{
		Binding: b, ExpectedGeneration: ledger.Generation, Discovery: Discovery{ConfigPath: config, Command: cmd},
		HelperVersion: "1.43.0",
	}
	if _, _, err := svc.publishHandoffReservation(testCtx(t), req, ledger.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.matchingReservation(req, "install"); err != nil {
		t.Fatal(err)
	}
	omitted := req
	omitted.HelperVersion = ""
	if _, err := svc.matchingReservation(omitted, "install"); err != nil {
		t.Fatalf("omitted helper version: %v", err)
	}
	req.HelperVersion = "1.44.0"
	if _, err := svc.matchingReservation(req, "install"); !errors.Is(err, ErrIntentConflict) {
		t.Fatalf("different helper version: %v", err)
	}
}

func TestHandoffIntentRecordsResolvedProfile(t *testing.T) {
	b, ledger := bindingFixture(t)
	config, cmd, ledger := ownedMCP(t, b, ledger)
	profile := filepath.Join(filepath.Dir(b.ControlRoot), "codex-profile")
	svc := Service{}
	req := Request{
		Binding: b, ExpectedGeneration: ledger.Generation, Discovery: Discovery{ConfigPath: config, Command: cmd},
		Profile: profile,
	}
	if _, _, err := svc.publishHandoffReservation(testCtx(t), req, ledger.Generation); err != nil {
		t.Fatal(err)
	}
	intent, err := ReadIntent(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(intent.Targets) != 1 || intent.Targets[0].Client != "codex" || intent.Targets[0].Profile != profile {
		t.Fatalf("intent: %+v", intent)
	}
}

func TestUninstallPublishesIntentBeforeFirstEffect(t *testing.T) {
	b, ledger := bindingFixture(t)
	uap := &fakeUAP{}
	name, err := b.Filename()
	if err != nil {
		t.Fatal(err)
	}
	uap.stage = func(Envelope) (Receipt, error) {
		return Receipt{BindingID: b.BindingID, DataRoot: b.DataRoot, LocatorArg: name}, nil
	}
	svc := Service{Stager: uap, Activator: uap, Remover: uap}
	if _, err := svc.Install(testCtx(t), Request{Binding: b, ExpectedGeneration: ledger.Generation}); err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(filepath.Dir(b.ControlRoot), "codex-profile")
	uap.remove = func(string) error {
		held, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
		if err != nil || held.Ledger.PendingMutation == nil {
			t.Fatalf("remove without uninstall reservation: %+v %v", held.Ledger.PendingMutation, err)
		}
		intent, err := ReadIntent(b.ControlRoot)
		if err != nil || intent.Action != "uninstall" || len(intent.Targets) == 0 || intent.Targets[0].Profile != profile {
			t.Fatalf("intent: %+v %v", intent, err)
		}
		return nil
	}
	if err := svc.Remove(testCtx(t), Request{Binding: b, ExpectedGeneration: snap.Ledger.Generation, Profile: profile}); err != nil {
		t.Fatal(err)
	}
	snap, err = installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil || snap.Ledger.PendingMutation != nil {
		t.Fatalf("uninstall left reservation: %+v %v", snap.Ledger.PendingMutation, err)
	}
	if _, err := os.Lstat(IntentPath(b.ControlRoot)); !os.IsNotExist(err) {
		t.Fatal("uninstall retained intent")
	}
}

func TestInstallConflictsWithPendingUninstall(t *testing.T) {
	b, ledger := bindingFixture(t)
	config, cmd, ledger := ownedMCP(t, b, ledger)
	svc := Service{}
	req := Request{Binding: b, ExpectedGeneration: ledger.Generation, Discovery: Discovery{ConfigPath: config, Command: cmd}}
	if _, _, err := svc.publishIntent(testCtx(t), req, ledger.Generation, "uninstall", "revoke-locator", []string{"direct-mcp"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.matchingReservation(req, "install"); !errors.Is(err, ErrIntentConflict) {
		t.Fatalf("install during uninstall: %v", err)
	}
}

func TestHandoffNoopDoesNotCreateIntent(t *testing.T) {
	b, ledger := bindingFixture(t)
	uap := &fakeUAP{}
	name, err := b.Filename()
	if err != nil {
		t.Fatal(err)
	}
	uap.stage = func(Envelope) (Receipt, error) {
		return Receipt{BindingID: b.BindingID, DataRoot: b.DataRoot, LocatorArg: name}, nil
	}
	svc := Service{Stager: uap, Activator: uap, Remover: uap}
	config := filepath.Join(filepath.Dir(b.ControlRoot), "client", "absent.json")
	if _, err := svc.Install(testCtx(t), Request{
		Binding: b, ExpectedGeneration: ledger.Generation,
		Discovery: Discovery{ConfigPath: config, Command: filepath.Join(b.RuntimeRoot, b.Primary)},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(IntentPath(b.ControlRoot)); !os.IsNotExist(err) {
		t.Fatal("noop created handoff intent")
	}
	snap, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Ledger.PendingMutation != nil || snap.Ledger.WriterFloor != 1 {
		t.Fatalf("noop raised reservation protocol: %+v", snap.Ledger)
	}
}

func TestPublishConfirmedIntentRecordsTargetsAndClears(t *testing.T) {
	b, ledger := bindingFixture(t)
	ctx := testCtx(t)
	profile := filepath.Join(filepath.Dir(b.ControlRoot), "codex-profile")
	svc := Service{}
	published, res, err := svc.PublishConfirmedIntent(ctx, ConfirmedIntent{
		ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Owner: b.Owner,
		ExpectedGeneration: ledger.Generation, Action: "install", Stage: "confirmed",
		SourceDigest: "abc", GlobalConfig: b.GlobalConfig, Targets: []IntentTarget{{
			Client: "codex", InstallationID: "uap-install", Profile: profile,
			Units: []string{"hooks", "agent-notify"},
		}},
	})
	if err != nil || res == nil || published.PendingMutation == nil {
		t.Fatalf("publish: %+v %v %v", published.PendingMutation, res, err)
	}
	intent, err := ReadIntent(b.ControlRoot)
	if err != nil || intent.Action != "install" || intent.Stage != "confirmed" || intent.SourceDigest != "abc" || intent.GlobalConfig != b.GlobalConfig {
		t.Fatalf("intent: %+v %v", intent, err)
	}
	if _, _, err := svc.PublishConfirmedIntent(ctx, ConfirmedIntent{
		ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Owner: b.Owner,
		Action: "install", SourceDigest: "abc", GlobalConfig: filepath.Join(filepath.Dir(b.GlobalConfig), "other.json"),
		Targets: []IntentTarget{{Client: "codex", Units: []string{"agent-notify"}}},
	}); !errors.Is(err, ErrIntentConflict) {
		t.Fatalf("pending global config drift was not refused: %v", err)
	}
	if len(intent.Targets) != 1 || intent.Targets[0].Profile != profile || strings.Join(intent.Targets[0].Units, ",") != "hooks,agent-notify" {
		t.Fatalf("targets: %+v", intent.Targets)
	}
	if err := svc.FinishConfirmedIntent(ctx, ConfirmedIntent{ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Owner: b.Owner}, res); err != nil {
		t.Fatal(err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil || snap.Ledger.PendingMutation != nil {
		t.Fatalf("finish left reservation: %+v %v", snap.Ledger.PendingMutation, err)
	}
	if _, err := os.Lstat(IntentPath(b.ControlRoot)); !os.IsNotExist(err) {
		t.Fatal("finish retained intent file")
	}
}

func TestFinishConfirmedIntentAfterClaudeCacheRelocationAndPortableRemoval(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, ".claude", "plugins", "cache", "claude-notifications-go", "claude-notifications-go")
	oldRoot, newRoot := filepath.Join(cache, "1.45.7"), filepath.Join(cache, "1.45.13")
	oldPrimary := filepath.Join(oldRoot, "bin", "claude-notifications-linux-amd64")
	newPrimary := filepath.Join(newRoot, "bin", "claude-notifications-linux-amd64")
	control := filepath.Join(root, "control")
	ctx := testCtx(t)
	owner := "existing-installer"
	ledger, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, RuntimeRoot: oldRoot, Owner: owner, ConsumerID: "claude-hooks",
		Files: []installruntime.File{{Path: oldPrimary, Data: []byte("old" + installruntime.WriterProtocolMarker), Mode: 0700}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ledger, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, RuntimeRoot: oldRoot, Owner: owner, ConsumerID: "portable:claude",
		Consumer: installruntime.Consumer{Commands: []string{oldPrimary}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ledger, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, RuntimeRoot: newRoot, Owner: owner, ConsumerID: "claude-hooks",
		RelocateVersionedCache: true,
		Files:                  []installruntime.File{{Path: newPrimary, Data: []byte("new" + installruntime.WriterProtocolMarker), Mode: 0700}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ledger.RuntimeRoot != oldRoot || ledger.Consumers["claude-hooks"].RuntimeRoot != newRoot {
		t.Fatalf("fixture did not retain old runtime: %+v", ledger)
	}
	svc := Service{}
	_, reservation, err := svc.PublishConfirmedIntent(ctx, ConfirmedIntent{
		ControlRoot: control, RuntimeRoot: oldRoot, Owner: owner, ExpectedGeneration: ledger.Generation,
		Action: "uninstall", Stage: "confirmed", Targets: []IntentTarget{{Client: "claude", Units: []string{"agent-notify"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, RuntimeRoot: oldRoot, Owner: owner, ConsumerID: "portable:claude",
		RemoveConsumer: true, Reservation: reservation,
	}); err != nil {
		t.Fatal(err)
	}
	if err = svc.FinishConfirmedIntent(ctx, ConfirmedIntent{ControlRoot: control, RuntimeRoot: filepath.Join(root, "unrelated"), Owner: owner}, reservation); err == nil {
		t.Fatal("unrelated runtime root finalized the intent")
	}
	if err = svc.FinishConfirmedIntent(ctx, ConfirmedIntent{ControlRoot: control, RuntimeRoot: oldRoot, Owner: owner}, reservation); err != nil {
		t.Fatalf("finish after removing last old-root consumer: %v", err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Ledger.PendingMutation != nil || snap.Ledger.Consumers["claude-hooks"].RuntimeRoot != newRoot {
		t.Fatalf("cleanup changed relocated hook or left reservation: %+v", snap.Ledger)
	}
	if _, err := os.Lstat(IntentPath(control)); !os.IsNotExist(err) {
		t.Fatalf("cleanup retained intent: %v", err)
	}
}

func TestPatchIntentGlobalConfigFreezesLegacyPendingIntent(t *testing.T) {
	b, ledger := bindingFixture(t)
	ctx := testCtx(t)
	svc := Service{}
	_, res, err := svc.PublishConfirmedIntent(ctx, ConfirmedIntent{
		ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Owner: b.Owner,
		ExpectedGeneration: ledger.Generation, Action: "install", Stage: "confirmed",
		Targets: []IntentTarget{{Client: "claude", Units: []string{"hooks"}},
			{Client: "codex", Units: []string{"agent-notify"}}},
	})
	if err != nil || res == nil {
		t.Fatalf("publish old intent: %v", err)
	}
	if err := svc.PatchIntentGlobalConfig(ctx, b.ControlRoot, b.RuntimeRoot, b.Owner, b.InstallationID, b.GlobalConfig); err != nil {
		t.Fatalf("freeze path: %v", err)
	}
	intent, err := ReadIntent(b.ControlRoot)
	if err != nil || intent.GlobalConfig != b.GlobalConfig || intent.SetupIntentID != res.ID {
		t.Fatalf("frozen intent: %+v %v", intent, err)
	}
	other := filepath.Join(filepath.Dir(b.GlobalConfig), "other.json")
	if err := svc.PatchIntentGlobalConfig(ctx, b.ControlRoot, b.RuntimeRoot, b.Owner, b.InstallationID, other); !errors.Is(err, ErrIntentConflict) {
		t.Fatalf("changed path accepted: %v", err)
	}
	intent, err = ReadIntent(b.ControlRoot)
	if err != nil || intent.GlobalConfig != b.GlobalConfig {
		t.Fatalf("conflict changed intent: %+v %v", intent, err)
	}
}
