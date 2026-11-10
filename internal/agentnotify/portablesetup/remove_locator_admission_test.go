//go:build linux || darwin

package portablesetup

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Exercise public removal after a genuine install: a foreign locator must be
// rejected before the host publishes any durable uninstall intent.
func TestMaterializerRemoveRejectsChangedLocatorBeforeIntent(t *testing.T) {
	mat, req, _ := registryMaterializerFixture(t)
	req.Integration = portable.Claude
	mat.Roots.ClaudeRunner = listingRunner{configRoot: req.ClientConfigRoot}
	binding, err := mat.Install(testCtx(t), req)
	if err != nil {
		t.Fatal(err)
	}
	name, err := binding.Filename()
	if err != nil {
		t.Fatal(err)
	}
	locator := filepath.Join(binding.DataRoot, name)
	original, err := os.ReadFile(locator)
	if err != nil {
		t.Fatal(err)
	}
	foreign := []byte("TEST changed locator\n")
	if err := os.WriteFile(locator, foreign, 0600); err != nil {
		t.Fatal(err)
	}
	stateBefore, err := os.ReadFile(mat.Roots.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	before, err := installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
	if err != nil || before.Recovery || before.Ledger.PendingMutation != nil {
		t.Fatalf("installed fixture not settled: %+v %v", before, err)
	}
	if _, err := os.Lstat(IntentPath(req.Identity.ControlRoot)); !os.IsNotExist(err) {
		t.Fatalf("fixture already has an uninstall intent: %v", err)
	}
	req.OperationID = "TEST-remove-changed-locator"
	if err := mat.Remove(testCtx(t), req); !errors.Is(err, ErrPreflight) {
		t.Errorf("changed locator was not refused by preflight: %v", err)
	}
	after, err := installruntime.ReadInstalledSnapshot(req.Identity.ControlRoot)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("refusal changed managed snapshot: before=%+v after=%+v err=%v", before, after, err)
	}
	if _, err := os.Lstat(IntentPath(req.Identity.ControlRoot)); !os.IsNotExist(err) {
		t.Fatalf("refusal published an uninstall intent: %v", err)
	}
	stateAfter, err := os.ReadFile(mat.Roots.StateFile)
	if err != nil || !bytes.Equal(stateBefore, stateAfter) {
		t.Fatalf("refusal changed UAP state: %v", err)
	}
	got, err := os.ReadFile(locator)
	if err != nil || !bytes.Equal(got, foreign) {
		t.Fatalf("refusal changed foreign locator: %q %v", got, err)
	}
	// Returning the exact owned locator restores ordinary removal, without a
	// leftover reservation requiring a separate recovery or a new operation.
	if err := os.WriteFile(locator, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := mat.Remove(testCtx(t), req); err != nil {
		t.Fatalf("exact locator did not restore removal: %v", err)
	}
	state, err := mat.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	installation, ok := findInstallation(state, req.Identity.InstallationID)
	if !ok || len(installation.Clients) != 0 || !installation.DataRetained {
		t.Fatalf("removal did not retain only plugin data: %+v", installation)
	}
	if _, err := os.Lstat(locator); !os.IsNotExist(err) {
		t.Fatalf("owned locator survived removal: %v", err)
	}
}
