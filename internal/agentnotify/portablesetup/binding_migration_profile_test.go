//go:build linux || darwin

package portablesetup

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestRefreshProjectionRequiresFrozenProfile(t *testing.T) {
	mat, req, root := registryMaterializerFixture(t)
	old, err := mat.Install(testCtx(t), req)
	if err != nil {
		t.Fatal(err)
	}
	state, err := mat.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	installation, ok := findInstallation(state, req.Identity.InstallationID)
	if !ok || len(installation.Clients) != 1 {
		t.Fatal("installed Codex binding missing")
	}
	var receiptID string
	for _, client := range installation.Clients {
		if client.ClientID == ClientID(req.Integration) {
			receiptID = client.DataReceiptID
		}
	}
	if receiptID == "" {
		t.Fatal("installed Codex data receipt missing")
	}
	replacement := old
	replacement.GlobalConfig = filepath.Join(root, "explicit-config", "config.json")
	if err := os.MkdirAll(filepath.Dir(replacement.GlobalConfig), 0700); err != nil {
		t.Fatal(err)
	}
	oldKey, _, _, err := old.Registration()
	if err != nil {
		t.Fatal(err)
	}
	newKey, _, _, err := replacement.Registration()
	if err != nil {
		t.Fatal(err)
	}
	confirmed, _, err := (Service{}).PublishConfirmedIntent(testCtx(t), ConfirmedIntent{
		ControlRoot: old.ControlRoot, RuntimeRoot: old.RuntimeRoot, Owner: old.Owner, Action: "update",
		ExpectedGeneration: currentLedgerGeneration(t, old.ControlRoot),
		Targets: []IntentTarget{{
			Client: string(req.Integration), InstallationID: req.Identity.InstallationID,
			BindingID: old.BindingID, DataReceiptID: receiptID, Profile: req.ClientConfigRoot,
			OldConsumerKey: oldKey, NewConsumerKey: newKey, OldBinding: &old, NewBinding: &replacement,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req.Identity.GlobalConfig = replacement.GlobalConfig
	req.ExpectedGeneration = confirmed.Generation
	req.HandoffAction = "update"
	req.OperationID = "TEST-profile-refresh"
	wrong := req
	wrong.ClientConfigRoot = filepath.Join(root, "TEST-other-profile")
	if err := os.MkdirAll(wrong.ClientConfigRoot, 0700); err != nil {
		t.Fatal(err)
	}
	before := registryFixtureFiles(t, root)
	// Admission holds the existing process lease; its owner record is ephemeral.
	before[mat.Roots.LockFile] = strings.SplitN(before[mat.Roots.LockFile], " sha256:", 2)[0]
	if _, err := mat.RefreshProjection(testCtx(t), wrong); !errors.Is(err, ErrPreflight) {
		t.Errorf("refresh outside frozen profile was not refused at admission: %v", err)
	}
	after := registryFixtureFiles(t, root)
	after[mat.Roots.LockFile] = strings.SplitN(after[mat.Roots.LockFile], " sha256:", 2)[0]
	if !reflect.DeepEqual(before, after) {
		t.Fatal("refused profile refresh changed fixture files, directories, or modes")
	}
	got, err := mat.RefreshProjection(testCtx(t), req)
	if err != nil || got != replacement {
		t.Fatalf("exact frozen profile refresh: got=%+v err=%v", got, err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(old.ControlRoot)
	if err != nil || snap.Ledger.PendingMutation == nil || !portable.ExactCommittedBinding(snap.Ledger, old) || !portable.ExactCommittedBinding(snap.Ledger, replacement) {
		t.Fatalf("refresh lost old/new binding or pending migration reservation: %v", err)
	}
}
