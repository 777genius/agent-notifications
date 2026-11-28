package installruntime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/windowscallback"
)

func windowsJournalFixture() transaction {
	tx := recoveryTransaction()
	tx.Schema = WindowsGenerationTransactionSchema
	tx.After.Schema = WindowsGenerationLedgerSchema
	tx.After.WriterFloor = WindowsGenerationWriterFloor
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
	if e != nil || got.Schema != WindowsGenerationTransactionSchema || got.Windows == nil || got.Windows.Phase != "preparing" {
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
	tx.After.Schema = 4
	tx.After.WriterFloor = 4
	tx.After.WindowsRetained = nil
	if e = writeTransaction(marker, tx); e != nil {
		t.Fatal(e)
	}
	if _, e = readTransactionFile(marker); e == nil || !strings.Contains(e.Error(), "windows participant requires schema6") {
		t.Fatalf("schema4 participant rejected for the wrong reason: %v", e)
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
	if next.Schema != WindowsGenerationLedgerSchema || next.WriterFloor != WindowsGenerationWriterFloor || transactionSchemaFor(next, Request{}) != transactionSchemaV4 {
		t.Fatal(next)
	}
	readerFloor, declared := windowsLedgerReaderFloor(next.Schema)
	if next.DecoderFloor != 0 || !declared || readerFloor != WindowsGenerationLedgerReaderFloor {
		t.Fatalf("Windows ledger reader borrowed native decoder floor: native=%d schema=%d", next.DecoderFloor, next.Schema)
	}
	current = windowsJournalFixture().After
	next = current
	applyReservationProtocol(&next, Request{}, current)
	if next.Schema != WindowsGenerationLedgerSchema || next.WriterFloor != WindowsGenerationWriterFloor || !reflect.DeepEqual(next.WindowsRetained, current.WindowsRetained) {
		t.Fatal(next)
	}
}

// Breakage: charging a Windows generation incorrectly promotes the existing
// NativeRecord decoder floor and therefore blocks or mislabels later native
// updates and schema5 orphan recovery.
func TestWindowsGenerationKeepsNativeDecoderFloorOne(t *testing.T) {
	current := windowsJournalFixture().After
	current.Schema = WindowsGenerationLedgerSchema
	current.WriterFloor = WindowsGenerationWriterFloor
	current.DecoderFloor = 1
	current.Native = &NativeRecord{DecoderFloor: 1}
	next := current
	applyReservationProtocol(&next, Request{}, current)
	if err := validateWindowsRetained(next); err != nil {
		t.Fatal(err)
	}
	if next.DecoderFloor != 1 || next.Native.DecoderFloor != 1 {
		t.Fatalf("post-Windows native floor changed: %+v", next)
	}
	after := *next.Native
	after.DecoderFloor = 1
	if err := validateNativeDecoderFloor(next.DecoderFloor, after.DecoderFloor); err != nil {
		t.Fatalf("post-Windows native update refused: %v", err)
	}
	if err := validateNativeDecoderFloor(1, 0); err == nil {
		t.Fatal("native decoder downgrade admitted")
	}
	if floor, ok := windowsLedgerReaderFloor(WindowsGenerationLedgerSchema); !ok || floor != 6 {
		t.Fatal("Windows ledger reader floor is not separately named")
	}
	if _, ok := windowsLedgerReaderFloor(transactionSchemaV4); ok {
		t.Fatal("ordinary schema4 acquired a Windows ledger reader declaration")
	}
}

// Breakage: a v4 writer can be admitted to the Windows6 destructive floor, or
// the new declaration loses any of the public historical marker contracts.
func TestFormerWritersRefuseWindowsGenerationFloor6(t *testing.T) {
	v1 := []byte(WriterProtocolMarker)
	v3 := append(append([]byte(nil), v1...), []byte(LocalWriterProtocolMarker)...)
	v4 := append(append([]byte(nil), v3...), []byte(OpenCodeWriterProtocolMarker)...)
	v6 := append(append([]byte(nil), v4...), []byte(WindowsWriterProtocolMarker)...)
	for _, old := range [][]byte{v1, v3, v4} {
		if WriterCompatibleAtFloor(old, WindowsGenerationWriterFloor) {
			t.Fatalf("former writer admitted at floor6: %q", old)
		}
	}
	for floor, compatible := range map[int][][]byte{
		1: {v1, v3, v4, v6}, 2: {v1, v3, v4, v6}, 3: {v3, v4, v6},
		4: {v4, v6}, 5: {v4, v6}, 6: {v6},
	} {
		for _, data := range compatible {
			if !WriterCompatibleAtFloor(data, floor) {
				t.Fatalf("published marker contract refused at floor%d: %q", floor, data)
			}
		}
	}
	if WriterCompatibleAtFloor(v6, 7) {
		t.Fatal("unknown writer floor7 admitted")
	}
}

// Breakage: an ordinary participant-free update after a Windows generation is
// promoted to schema6, loses the retained generation, or lets a reverse journal
// forget that permanent capacity charge.
func TestOrdinaryJournalRemainsSchema4AfterWindowsGeneration(t *testing.T) {
	windows := windowsJournalFixture()
	ordinary := windows
	ordinary.Windows = nil
	ordinary.Schema = transactionSchemaV4
	if transactionSchemaFor(ordinary.After, Request{}) != transactionSchemaV4 {
		t.Fatal("ordinary journal acquired Windows6 schema")
	}
	marker := filepath.Join(t.TempDir(), "transaction.json")
	if err := writeTransaction(marker, ordinary); err != nil {
		t.Fatal(err)
	}
	got, err := readTransactionFile(marker)
	if err != nil || got.Schema != transactionSchemaV4 || got.Windows != nil ||
		!reflect.DeepEqual(got.After.WindowsRetained, windows.After.WindowsRetained) {
		t.Fatalf("ordinary schema4 round trip lost floor6 ledger: %+v %v", got, err)
	}
	reversed, err := reverseTransaction(got.Before, got)
	if err != nil || reversed.Schema != transactionSchemaV4 || reversed.Windows != nil ||
		!reflect.DeepEqual(reversed.After.WindowsRetained, windows.After.WindowsRetained) {
		t.Fatalf("ordinary reverse dropped permanent generation: %+v %v", reversed, err)
	}
	if err := writeTransaction(marker, reversed); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(marker)
	var envelope struct {
		Transaction map[string]json.RawMessage
	}
	var carrier map[string]json.RawMessage
	var changes []json.RawMessage
	if err != nil || json.Unmarshal(raw, &envelope) != nil ||
		json.Unmarshal(envelope.Transaction["Files"], &carrier) != nil || len(carrier) != 1 ||
		json.Unmarshal(carrier["Changes"], &changes) != nil || changes == nil || len(changes) != 0 {
		t.Fatalf("ordinary journal lost public schema4 carrier: %s %v", raw, err)
	}
	if got, err = readTransactionFile(marker); err != nil || !got.Rollback ||
		!reflect.DeepEqual(got.After.WindowsRetained, windows.After.WindowsRetained) {
		t.Fatalf("durable ordinary reverse lost retained generation: %+v %v", got, err)
	}
}
