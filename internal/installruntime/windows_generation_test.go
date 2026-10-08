package installruntime

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/internal/windowscallback"
)

func windowsJournalFixture() transaction {
	tx := recoveryTransaction()
	tx.Schema = 5
	tx.After.Schema = 5
	tx.After.WriterFloor = 5
	manifest := windowscallback.Snapshot{Generation: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CanonicalRoot: `\\?\C:\TEST\windows-callback\aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`, OwnerSID: "S-1-5-21-1", HelperSHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", AUMID: "AgentNotifications.TEST", CLSID: "{11111111-1111-1111-1111-111111111111}", VendorName: windowscallback.VendorName, Publisher: windowscallback.VendorPublisher, Family: windowscallback.VendorFamily, FullName: "OpenAI.Codex_26.930.7945.0_x64__2p2nqsd0c76g0"}
	encoded, _ := windowscallback.EncodeSnapshot(manifest)
	binding := windowscallback.Binding{SnapshotPath: `\\?\C:\TEST\windows-callback\aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\generation.wne`, SHA256: windowscallback.Digest(encoded)}
	tx.After.WindowsRetained = []windowscallback.Binding{binding}
	tx.Windows = &WindowsChange{Snapshot: manifest, Binding: binding, Phase: "preparing"}
	return tx
}

// Breakage: a floor4 reader can silently ignore the concrete participant, or
// persisted rollback loses the charged private generation after interrupted
// setup. Assertions exercise actual journal persistence and reverse decisions.
func TestWindowsJournalDecisionAndRetention(t *testing.T) {
	tx := windowsJournalFixture()
	marker := filepath.Join(t.TempDir(), "transaction.json")
	if e := writeTransaction(marker, tx); e != nil {
		t.Fatal(e)
	}
	got, e := readTransactionFile(marker)
	if e != nil || got.Schema != 5 || got.Windows == nil || got.Windows.Phase != "preparing" {
		t.Fatal(got, e)
	}
	reversed, e := reverseTransaction(tx.Before, got)
	if e != nil || !reversed.Rollback || reversed.Windows == nil || reversed.Windows.Phase != "rollback_decided" || !reflect.DeepEqual(reversed.After.WindowsRetained, tx.After.WindowsRetained) {
		t.Fatal(reversed, e)
	}
	if e = writeTransaction(marker, reversed); e != nil {
		t.Fatal(e)
	}
	got, e = readTransactionFile(marker)
	if e != nil || !got.Rollback || got.Windows.Phase != "rollback_decided" {
		t.Fatal(got, e)
	}
	tx.Windows.Applied = 3
	tx.Windows.Observed = true
	for _, phase := range []string{"commit_decided", "binding_published", "committed"} {
		tx.Windows.Phase = phase
		if _, e = reverseTransaction(tx.Before, tx); !errors.Is(e, ErrPolicyRecovery) {
			t.Fatal("durable forward decision reversed", phase, e)
		}
	}
	tx.Windows.Phase = "preparing"
	tx.Schema = 4
	if e = writeTransaction(marker, tx); e != nil {
		t.Fatal(e)
	}
	if _, e = readTransactionFile(marker); e == nil {
		t.Fatal("participant admitted under reader-ignorable schema4")
	}
}

// Breakage: Windows floor promotion erases an existing retained binding or
// promotes ordinary non-Windows writes from their existing schema4 protocol.
func TestWindowsWriterFloorPreservesOrdinaryProtocol(t *testing.T) {
	current := Ledger{Schema: 4, WriterFloor: 4}
	next := current
	applyReservationProtocol(&next, Request{}, current)
	if next.Schema != 4 || next.WriterFloor != 4 {
		t.Fatal(next)
	}
	next = current
	applyReservationProtocol(&next, Request{Windows: &WindowsChange{}}, current)
	if next.Schema != 5 || next.WriterFloor != 5 || transactionSchemaFor(next, Request{}) != 5 {
		t.Fatal(next)
	}
	current = windowsJournalFixture().After
	next = current
	applyReservationProtocol(&next, Request{}, current)
	if next.Schema != 5 || next.WriterFloor != 5 || !reflect.DeepEqual(next.WindowsRetained, current.WindowsRetained) {
		t.Fatal(next)
	}
}
