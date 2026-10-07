//go:build legacy_uapinstaller

package uapinstaller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/dirswap"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/transaction"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/ports"
)

type countingRunner struct {
	n int
}

func (r *countingRunner) Run(context.Context, ports.Command) (ports.CommandResult, error) {
	r.n++
	return ports.CommandResult{}, errors.New("unexpected runner call")
}

type listingRunner struct {
	configRoot string
}

func (r listingRunner) Run(_ context.Context, _ ports.Command) (ports.CommandResult, error) {
	root := filepath.Join(r.configRoot, "skills")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return ports.CommandResult{Stdout: []byte("[]")}, nil
	}
	if err != nil {
		return ports.CommandResult{}, err
	}
	listed := make([]map[string]any, 0)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name()[0] == '.' {
			continue
		}
		path := filepath.Join(root, entry.Name())
		body, readErr := os.ReadFile(filepath.Join(path, ".claude-plugin", "plugin.json"))
		if readErr != nil {
			continue
		}
		var manifest map[string]any
		if json.Unmarshal(body, &manifest) != nil {
			continue
		}
		name, _ := manifest["name"].(string)
		listed = append(listed, map[string]any{
			"id": name + "@skills-dir", "version": manifest["version"], "scope": "user",
			"enabled": true, "installPath": path,
		})
	}
	body, err := json.Marshal(listed)
	return ports.CommandResult{Stdout: body}, err
}

func TestInspectDoesNotRunHelperOrCreateState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing-state")
	runner := &countingRunner{}
	eng, err := New(Config{
		StateRoot: root, Runner: runner,
		HelperExecutable: filepath.Join(t.TempDir(), "helper"),
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := eng.Inspect(testCtx(t))
	if err != nil || view.Recovery.Required {
		t.Fatalf("inspect: %+v %v", view, err)
	}
	if runner.n != 0 {
		t.Fatalf("inspect ran helper %d times", runner.n)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("inspect created state root")
	}
}

func TestRecoverDoesNotActivateClient(t *testing.T) {
	eng, _ := plantPendingJournal(t)
	runner := &countingRunner{}
	commits := 0
	recovered, err := New(Config{
		StateRoot: eng.cfg.StateRoot, Runner: runner,
		HelperExecutable: filepath.Join(t.TempDir(), "helper"),
		OnCommittedBinding: func(context.Context, BindingFacts) error {
			commits++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := recovered.Inspect(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recovered.Recover(testCtx(t), view); err != nil {
		t.Fatal(err)
	}
	if runner.n != 0 || commits != 0 {
		t.Fatalf("recover activated client runner=%d commits=%d", runner.n, commits)
	}
}

func TestInspectReportsPendingJournalWithoutMutating(t *testing.T) {
	eng, journal := plantPendingJournal(t)
	view, err := eng.Inspect(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if !view.Recovery.Required || len(view.Recovery.Journals) != 1 {
		t.Fatalf("missing journal observation: %+v", view.Recovery)
	}
	got := view.Recovery.Journals[0]
	if got.OperationID != journal.OperationID || got.Phase != dirswap.PhaseIntent || got.BindingID != journal.ClientBindingID {
		t.Fatalf("journal: %+v", got)
	}
	if got.Digest == "" || got.TargetPath != journal.ActivePath {
		t.Fatalf("journal identity: %+v", got)
	}
	if _, err := os.Lstat(eng.cfg.LockFile); !os.IsNotExist(err) {
		t.Fatal("inspect acquired mutation lock file")
	}
	open, err := dirswap.Manager{JournalDir: eng.cfg.OperationsDir}.ListOpen()
	if err != nil || len(open) != 1 {
		t.Fatalf("inspect recovered journal: %+v %v", open, err)
	}
}

func TestInspectReportsBothPendingJournalsWithoutMutating(t *testing.T) {
	eng, first := plantPendingJournal(t)
	second := plantOpenJournal(t, eng, "pending-journal-op-2")
	before, err := dirswap.Manager{JournalDir: eng.cfg.OperationsDir}.ListOpen()
	if err != nil || len(before) != 2 {
		t.Fatalf("planted journals: %+v %v", before, err)
	}
	view, err := eng.Inspect(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if !view.Recovery.Required || len(view.Recovery.Journals) != 2 {
		t.Fatalf("missing group journal observation: %+v", view.Recovery)
	}
	seen := map[string]bool{}
	for _, journal := range view.Recovery.Journals {
		seen[journal.OperationID] = true
		if journal.Digest == "" || journal.Phase != dirswap.PhaseIntent {
			t.Fatalf("journal identity: %+v", journal)
		}
	}
	if !seen[first.OperationID] || !seen[second.OperationID] {
		t.Fatalf("inspect hid a pending journal: %+v", view.Recovery.Journals)
	}
	open, err := dirswap.Manager{JournalDir: eng.cfg.OperationsDir}.ListOpen()
	if err != nil || len(open) != 2 {
		t.Fatalf("inspect recovered a journal: %+v %v", open, err)
	}
	result, err := eng.Recover(testCtx(t), view)
	if err != nil || result.Outcome != OutcomeCompleted {
		t.Fatalf("recover both journals: %+v %v", result, err)
	}
	if len(result.Recovery.Resolved) != 2 {
		t.Fatalf("resolved receipts: %+v", result.Recovery)
	}
	after, err := eng.Inspect(testCtx(t))
	if err != nil || after.Recovery.Required {
		t.Fatalf("post-recover inspect: %+v %v", after, err)
	}
}

func TestRecoverMatchingPendingJournal(t *testing.T) {
	eng, _ := plantPendingJournal(t)
	view, err := eng.Inspect(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	result, err := eng.Recover(testCtx(t), view)
	if err != nil || result.Outcome != OutcomeCompleted {
		t.Fatalf("recover: %+v %v", result, err)
	}
	if len(result.Recovery.Resolved) != 1 || result.Recovery.Resolved[0].OperationID != view.Recovery.Journals[0].OperationID {
		t.Fatalf("resolved receipts: %+v", result.Recovery)
	}
	if len(result.Recovery.Remaining) != 0 || len(result.Recovery.Unknown) != 0 {
		t.Fatalf("leftover receipts: %+v", result.Recovery)
	}
	open, err := dirswap.Manager{JournalDir: eng.cfg.OperationsDir}.ListOpen()
	if err != nil || len(open) != 0 {
		t.Fatalf("journal survived recover: %+v %v", open, err)
	}
	after, err := eng.Inspect(testCtx(t))
	if err != nil || after.Recovery.Required {
		t.Fatalf("post-recover inspect: %+v %v", after, err)
	}
	again, err := eng.Recover(testCtx(t), after)
	if err != nil || again.Outcome != OutcomeUnchanged || again.Reason != "already_recovered" {
		t.Fatalf("clean observation after recover: %+v %v", again, err)
	}
}

func TestRecoverDeletedJournalWithStagingIsNotUnchanged(t *testing.T) {
	eng, journal := plantPendingJournal(t)
	view, err := eng.Inspect(testCtx(t))
	if err != nil || !view.Recovery.Required || len(view.Recovery.Journals) != 1 {
		t.Fatalf("inspect: %+v %v", view, err)
	}
	if err := os.Remove(filepath.Join(eng.cfg.OperationsDir, journal.OperationID+".json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(journal.StagingPath); err != nil {
		t.Fatalf("staging missing before recover: %v", err)
	}
	result, err := eng.Recover(testCtx(t), view)
	if !errors.Is(err, ErrRecoveryRequired) || result.Outcome != OutcomeRecovery || result.Reason != "incomplete_recovery" {
		t.Fatalf("deleted journal leftover staging: %+v %v", result, err)
	}
	if len(result.Recovery.Unknown) == 0 && len(result.Recovery.Remaining) == 0 {
		t.Fatalf("incomplete recovery hid vanished journal: %+v", result.Recovery)
	}
	if _, err := os.Stat(journal.StagingPath); err != nil {
		t.Fatalf("recover mutated leftover staging: %v", err)
	}
}

func TestRecoverDeletedJournalWithBackupIsNotUnchanged(t *testing.T) {
	eng, journal := plantPendingJournal(t)
	view, err := eng.Inspect(testCtx(t))
	if err != nil || !view.Recovery.Required || len(view.Recovery.Journals) != 1 {
		t.Fatalf("inspect: %+v %v", view, err)
	}
	if err := os.Remove(filepath.Join(eng.cfg.OperationsDir, journal.OperationID+".json")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(journal.StagingPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(journal.BackupPath, 0700); err != nil {
		t.Fatal(err)
	}
	result, err := eng.Recover(testCtx(t), view)
	if !errors.Is(err, ErrRecoveryRequired) || result.Outcome != OutcomeRecovery || result.Reason != "incomplete_recovery" {
		t.Fatalf("deleted journal leftover backup: %+v %v", result, err)
	}
	if len(result.Recovery.Unknown) == 0 && len(result.Recovery.Remaining) == 0 {
		t.Fatalf("incomplete recovery hid vanished journal: %+v", result.Recovery)
	}
}

func TestRecoverStaleObservationWithoutLeftoverIsUnchanged(t *testing.T) {
	eng, journal := plantPendingJournal(t)
	view, err := eng.Inspect(testCtx(t))
	if err != nil || !view.Recovery.Required || len(view.Recovery.Journals) != 1 {
		t.Fatalf("inspect: %+v %v", view, err)
	}
	if err := os.Remove(filepath.Join(eng.cfg.OperationsDir, journal.OperationID+".json")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(journal.StagingPath); err != nil {
		t.Fatal(err)
	}
	result, err := eng.Recover(testCtx(t), view)
	if err != nil || result.Outcome != OutcomeUnchanged || result.Reason != "already_recovered" {
		t.Fatalf("vanished journal without leftover: %+v %v", result, err)
	}
}

func TestRecoverRejectsUnobservedPendingJournal(t *testing.T) {
	eng, _ := plantPendingJournal(t)
	result, err := eng.Recover(testCtx(t), Inspection{StateRoot: eng.cfg.StateRoot})
	if !errors.Is(err, ErrPlanChanged) || result.Outcome != OutcomeConflict || result.Reason != "plan_changed" {
		t.Fatalf("empty observation: %+v %v", result, err)
	}
	open, err := dirswap.Manager{JournalDir: eng.cfg.OperationsDir}.ListOpen()
	if err != nil || len(open) != 1 {
		t.Fatalf("plan_changed recovered journal: %+v %v", open, err)
	}
}

func TestRecoverRejectsNewlyObservedPendingJournal(t *testing.T) {
	eng, _ := plantPendingJournal(t)
	view, err := eng.Inspect(testCtx(t))
	if err != nil || len(view.Recovery.Journals) != 1 {
		t.Fatalf("inspect: %+v %v", view, err)
	}
	second := plantOpenJournal(t, eng, "pending-journal-op-2")
	result, err := eng.Recover(testCtx(t), view)
	if !errors.Is(err, ErrPlanChanged) || result.Outcome != OutcomeConflict || result.Reason != "plan_changed" {
		t.Fatalf("expanded observation: %+v %v", result, err)
	}
	open, err := dirswap.Manager{JournalDir: eng.cfg.OperationsDir}.ListOpen()
	if err != nil || len(open) != 2 {
		t.Fatalf("plan_changed recovered journals: %+v %v", open, err)
	}
	seen := map[string]bool{}
	for _, journal := range open {
		seen[journal.OperationID] = true
	}
	if !seen[view.Recovery.Journals[0].OperationID] || !seen[second.OperationID] {
		t.Fatalf("missing planted journal after plan_changed: %+v", open)
	}
}

func TestInspectReportsStateCommittedReceiptWithoutJournal(t *testing.T) {
	eng, receipt := plantStateCommittedReceipt(t)
	view, err := eng.Inspect(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if !view.Recovery.Required || len(view.Recovery.Receipts) != 1 {
		t.Fatalf("missing receipt observation: %+v", view.Recovery)
	}
	got := view.Recovery.Receipts[0]
	if got.OperationID != receipt.OperationID || got.Phase != transaction.ReceiptPhaseStateCommitted || got.JournalPresent {
		t.Fatalf("receipt: %+v", got)
	}
	if len(view.Installations) != 1 || view.Installations[0].TreeDigest != "sha256:tree" {
		t.Fatalf("inspect omitted recorded tree digest: %+v", view.Installations)
	}
	open, err := dirswap.Manager{JournalDir: eng.cfg.OperationsDir}.ListOpen()
	if err != nil || len(open) != 0 {
		t.Fatalf("unexpected journal: %+v %v", open, err)
	}
}

func TestRecoverFinalizesStateCommittedReceipt(t *testing.T) {
	eng, _ := plantStateCommittedReceipt(t)
	view, err := eng.Inspect(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	result, err := eng.Recover(testCtx(t), view)
	if err != nil || result.Outcome != OutcomeCompleted {
		t.Fatalf("recover: %+v %v", result, err)
	}
	if len(result.Recovery.Resolved) != 1 || result.Recovery.Resolved[0].OperationID != view.Recovery.Receipts[0].OperationID {
		t.Fatalf("resolved receipts: %+v", result.Recovery)
	}
	if len(result.Recovery.Remaining) != 0 || len(result.Recovery.Unknown) != 0 {
		t.Fatalf("leftover receipts: %+v", result.Recovery)
	}
	state, err := statev2.Store{Path: eng.cfg.StateFile}.Load()
	if err != nil {
		t.Fatal(err)
	}
	gotPhase := ""
	for _, installation := range state.Installations {
		for _, binding := range installation.Clients {
			if len(binding.Receipts) > 0 {
				gotPhase = binding.Receipts[0].Phase
			}
		}
	}
	if gotPhase != transaction.ReceiptPhaseCommitted {
		t.Fatalf("receipt phase: %s", gotPhase)
	}
	after, err := eng.Inspect(testCtx(t))
	if err != nil || after.Recovery.Required {
		t.Fatalf("post-recover inspect: %+v %v", after, err)
	}
}

func TestInspectCorruptJournalIsUntrustworthy(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	eng, err := New(Config{StateRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	ops := filepath.Join(root, "operations")
	if err := os.MkdirAll(ops, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ops, "broken-op.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	view, err := eng.Inspect(testCtx(t))
	if err == nil || !view.Recovery.Required || view.Recovery.Reason == "" {
		t.Fatalf("corrupt journal: %+v %v", view, err)
	}
	result, recoverErr := eng.RecoverCurrent(testCtx(t))
	if recoverErr == nil || result.Outcome != OutcomeRecovery {
		t.Fatalf("corrupt recover: %+v %v", result, recoverErr)
	}
	if len(result.Recovery.Resolved) != 0 {
		t.Fatalf("corrupt recover claimed resolved: %+v", result.Recovery)
	}
}

func plantPendingJournal(t *testing.T) (*Engine, dirswap.Receipt) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	eng, err := New(Config{StateRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	return eng, plantOpenJournal(t, eng, "pending-journal-op")
}

func plantOpenJournal(t *testing.T, eng *Engine, opID string) dirswap.Receipt {
	t.Helper()
	owned := eng.cfg.ManagedRoot
	active := filepath.Join(owned, "plugin")
	staging := filepath.Join(owned, ".agentplugins-staging-pending")
	if err := os.MkdirAll(staging, 0700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(opID))
	receipt := dirswap.Receipt{
		SchemaVersion: 3, Operation: dirswap.OperationSwap, OperationID: opID,
		ClientBindingID: "client-binding-1", Sequence: 1, OwnedBase: owned,
		ActivePath: active, StagingPath: staging,
		BackupPath: filepath.Join(owned, ".agentplugins-backup-"+hex.EncodeToString(sum[:8])),
		Phase:      dirswap.PhaseIntent,
	}
	body, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(eng.cfg.OperationsDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(eng.cfg.OperationsDir, opID+".json"), append(body, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func plantStateCommittedReceipt(t *testing.T) (*Engine, domain.MutationReceipt) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	eng, err := New(Config{StateRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	installationID := "00000000-0000-4000-8000-000000000001"
	target := filepath.Join(root, "client")
	clientID := domain.ComputeClientBindingID(installationID, "codex", "user", target)
	receipt := domain.MutationReceipt{
		OperationID: "committed-without-journal", Sequence: 1, MutationType: "directory_swap",
		ClientBindingID: clientID, ActivePath: target, Phase: transaction.ReceiptPhaseStateCommitted,
	}
	state := domain.StateFileV2{
		SchemaVersion: domain.StateSchemaVersion,
		Installations: []domain.Installation{{
			InstallationID: installationID,
			DeclaredName:   "demo",
			Source: domain.SourceBinding{
				SourceBindingID: "src_demo", RequestedSource: "demo", CanonicalSource: "https://example.com/demo",
				ResolvedRevision: "abc", TreeDigest: "sha256:tree",
			},
			Package: domain.PackageBinding{
				LoaderKind: domain.LoaderKindAgentPlugins, FormatID: domain.FormatIDAgentPluginsV1,
				SchemaURI: domain.PluginSchemaV1, DeclaredName: "demo", ManifestDigest: "sha256:manifest",
			},
			Clients: map[string]domain.ClientBinding{
				clientID: {
					ClientBindingID: clientID, ClientID: "codex", Scope: "user", TargetLocator: target,
					PhysicalArtifact: domain.ComputePhysicalArtifactID("demo", installationID),
					Materialization:  domain.MaterializationStaged, Activation: domain.ActivationPrepared,
					Authentication: domain.AuthenticationNotRequired, Policy: domain.PolicyAllowed,
					Verification: domain.VerificationPackageValid,
					Receipts:     []domain.MutationReceipt{receipt},
				},
			},
		}},
	}
	if err := (statev2.Store{Path: eng.cfg.StateFile}).Save(state); err != nil {
		t.Fatal(err)
	}
	return eng, receipt
}

func plantRetainedInstallation(t *testing.T) *Engine {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	runner := &countingRunner{}
	eng, err := New(Config{StateRoot: root, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	state := domain.StateFileV2{
		SchemaVersion: domain.StateSchemaVersion,
		Installations: []domain.Installation{{
			InstallationID: "00000000-0000-4000-8000-000000000070",
			DeclaredName:   "demo",
			DataRetained:   true,
			Source: domain.SourceBinding{
				SourceBindingID: "src_demo", RequestedSource: "demo", CanonicalSource: "https://example.com/demo",
				ResolvedRevision: "abc", TreeDigest: "sha256:tree",
			},
			Package: domain.PackageBinding{
				LoaderKind: domain.LoaderKindAgentPlugins, FormatID: domain.FormatIDAgentPluginsV1,
				SchemaURI: domain.PluginSchemaV1, DeclaredName: "demo", ManifestDigest: "sha256:manifest",
			},
			Clients: map[string]domain.ClientBinding{},
			DataReceipts: map[string]domain.DataReceipt{
				"data_demo": {
					DataReceiptID: "data_demo", PhysicalBackend: "local", Scope: "user",
					Locator: filepath.Join(root, "plugin-data", "keep"), OwnershipDigest: "sha256:retained",
					State: domain.DataReceiptOwned,
				},
			},
		}},
	}
	if err := (statev2.Store{Path: eng.cfg.StateFile}).Save(state); err != nil {
		t.Fatal(err)
	}
	return eng
}

func TestRecordedBindingDigestPrefersPackageRevision(t *testing.T) {
	binding := domain.ClientBinding{
		PackageRevision: &domain.ClientPackageRevision{TreeDigest: "sha256:binding"},
	}
	if got := recordedBindingDigest(binding, "sha256:fallback"); got != "sha256:binding" {
		t.Fatalf("recorded digest: %s", got)
	}
	if got := recordedBindingDigest(domain.ClientBinding{}, "sha256:fallback"); got != "sha256:fallback" {
		t.Fatalf("fallback digest: %s", got)
	}
}

func TestPlanClientDigestUsesMatchingTarget(t *testing.T) {
	plan := Plan{
		TreeDigest: "sha256:group",
		Targets: []PlanTarget{
			{ClientID: "codex", TreeDigest: "sha256:codex"},
			{ClientID: "claude", TreeDigest: "sha256:claude"},
		},
	}
	if got := planClientDigest(plan, "claude"); got != "sha256:claude" {
		t.Fatalf("claude digest: %s", got)
	}
	if got := planClientDigest(plan, "cursor"); got != "sha256:group" {
		t.Fatalf("unknown client digest: %s", got)
	}
}
