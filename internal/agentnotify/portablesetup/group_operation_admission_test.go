//go:build linux || darwin

package portablesetup

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestMaterializerGroupRejectsMixedOperationsBeforeMutation(t *testing.T) {
	for _, first := range []uapinstaller.Operation{uapinstaller.OpUpdate, uapinstaller.OpRepair} {
		t.Run(string(first), func(t *testing.T) {
			mat, claude, root := registryMaterializerFixture(t)
			claude.Integration = portable.Claude
			mat.Roots.ClaudeRunner = listingRunner{configRoot: claude.ClientConfigRoot}
			codex := claude
			codex.Integration = portable.Codex
			codex.ClientConfigRoot = filepath.Join(root, "TEST-group-operation-codex")
			if err := os.Mkdir(codex.ClientConfigRoot, 0700); err != nil {
				t.Fatal(err)
			}
			// Empty and explicit install select the same operation.
			codex.Operation = uapinstaller.OpInstall
			if bindings, err := mat.ApplyGroup(testCtx(t), []MaterializeRequest{claude, codex}); err != nil || len(bindings) != 2 {
				t.Fatalf("initial group install: %+v %v", bindings, err)
			}
			claude.Operation, codex.Operation = first, uapinstaller.OpRepair
			if first == uapinstaller.OpRepair {
				codex.Operation = uapinstaller.OpUpdate
			}
			claude.OperationID, codex.OperationID = "TEST-mixed-group-operation", "TEST-mixed-group-operation"
			before := registryFixtureFiles(t, root)
			if _, err := mat.ApplyGroup(testCtx(t), []MaterializeRequest{claude, codex}); !errors.Is(err, ErrPreflight) {
				t.Errorf("mixed operations accepted: %v", err)
			}
			if !reflect.DeepEqual(before, registryFixtureFiles(t, root)) {
				t.Fatal("mixed operations changed installed files, journals or managed state")
			}
			// A rejected mismatch does not strand either client's next operation.
			codex.Operation = claude.Operation
			if bindings, err := mat.ApplyGroup(testCtx(t), []MaterializeRequest{claude, codex}); err != nil || len(bindings) != 2 {
				t.Fatalf("matching operations after refusal: %+v %v", bindings, err)
			}
		})
	}
}

func TestMaterializerGroupRecoveryRejectsDifferentControlRoots(t *testing.T) {
	mat, first, firstRoot := registryMaterializerFixture(t)
	_, second, secondRoot := registryMaterializerFixture(t)
	ctx := testCtx(t)
	for _, req := range []MaterializeRequest{first, second} {
		_, err := installruntime.Commit(ctx, installruntime.Request{
			ControlRoot: req.Identity.ControlRoot, RuntimeRoot: req.Identity.RuntimeRoot,
			Owner: req.Identity.Owner, ConsumerID: "existing",
			Files: []installruntime.File{{Path: filepath.Join(req.Identity.RuntimeRoot, "TEST-pending-group-recovery"), Data: []byte("recovered"), Mode: 0700}},
			Fault: func(phase string) error {
				if phase == "transaction" {
					return errors.New("TEST interrupted kernel transaction")
				}
				return nil
			},
		})
		if err == nil {
			t.Fatal("kernel interruption not reached")
		}
		if _, err := os.Stat(filepath.Join(req.Identity.ControlRoot, "transaction.json")); err != nil {
			t.Fatal("pending transaction missing", err)
		}
	}
	beforeFirst, beforeSecond := registryFixtureFiles(t, firstRoot), registryFixtureFiles(t, secondRoot)
	if err := mat.VerifyRecoveryAuthority(nil, first); !errors.Is(err, ErrPreflight) {
		t.Errorf("nil recovery authority context accepted: %v", err)
	}
	if err := mat.RecoverGroupJournals(ctx, []MaterializeRequest{first, second}); !errors.Is(err, ErrPreflight) {
		t.Errorf("different recovery roots accepted: %v", err)
	}
	if !reflect.DeepEqual(beforeFirst, registryFixtureFiles(t, firstRoot)) || !reflect.DeepEqual(beforeSecond, registryFixtureFiles(t, secondRoot)) {
		t.Fatal("mismatched group recovery changed either root")
	}
	selected := first
	selected.Integration = portable.Claude
	if err := mat.RecoverGroupJournals(ctx, []MaterializeRequest{first, selected}); err != nil {
		t.Fatal("shared-root recovery after refusal", err)
	}
	if body, err := os.ReadFile(filepath.Join(first.Identity.RuntimeRoot, "TEST-pending-group-recovery")); err != nil || string(body) != "recovered" {
		t.Fatalf("shared-root recovery did not finish: %q %v", body, err)
	}
	if !reflect.DeepEqual(beforeSecond, registryFixtureFiles(t, secondRoot)) {
		t.Fatal("shared-root recovery changed an unrelated pending root")
	}
}
