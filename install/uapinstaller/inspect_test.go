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
	open, err := dirswap.Manager{JournalDir: eng.cfg.OperationsDir}.ListOpen()
	if err != nil || len(open) != 0 {
		t.Fatalf("journal survived recover: %+v %v", open, err)
	}
	after, err := eng.Inspect(testCtx(t))
	if err != nil || after.Recovery.Required {
		t.Fatalf("post-recover inspect: %+v %v", after, err)
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
}

func plantPendingJournal(t *testing.T) (*Engine, dirswap.Receipt) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	eng, err := New(Config{StateRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	owned := filepath.Join(root, "managed")
	active := filepath.Join(owned, "plugin")
	staging := filepath.Join(owned, ".agentplugins-staging-pending")
	if err := os.MkdirAll(staging, 0700); err != nil {
		t.Fatal(err)
	}
	opID := "pending-journal-op"
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
	return eng, receipt
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
