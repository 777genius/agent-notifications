package installruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func windowsFixtureEnvelope(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "windows6-transaction.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Breakage: a fixture-independent Windows6 carrier is silently treated as
// orphan schema5, loses its exact retained binding, or permits a malformed
// participant to reach recovery.
func TestWindows6CarrierRoundTripAndParticipantBinding(t *testing.T) {
	data := windowsFixtureEnvelope(t)
	tx, err := decodeTransaction(data)
	if err != nil {
		t.Fatal(err)
	}
	if tx.Schema != WindowsGenerationTransactionSchema || tx.After.Schema != WindowsGenerationLedgerSchema ||
		tx.After.WriterFloor != WindowsGenerationWriterFloor || tx.After.DecoderFloor != 0 || tx.Windows == nil ||
		tx.Windows.Binding.SHA256 != "b1269751f5db4fcc0032708ccaa2fa7dea40f56497b20fcdb475e703c9924267" ||
		len(tx.After.WindowsRetained) != 1 || !reflect.DeepEqual(tx.After.WindowsRetained[0], tx.Windows.Binding) {
		t.Fatalf("decoded Windows6 fixture: %+v", tx)
	}
	reverse, err := reverseTransaction(tx.Before, tx)
	if err != nil || !reverse.Rollback || reverse.Windows == nil || reverse.Windows.Phase != "rollback_decided" ||
		!reflect.DeepEqual(reverse.After.WindowsRetained, tx.After.WindowsRetained) {
		t.Fatalf("retained participant rollback changed binding: %+v %v", reverse, err)
	}
	if _, err = decodeTransaction(orphanEnvelope(t, mustWindowsBody(t, data))); err != nil {
		t.Fatal(err)
	}
}

func mustWindowsBody(t *testing.T, envelope []byte) []byte {
	t.Helper()
	var wire transactionEnvelope
	if err := json.Unmarshal(envelope, &wire); err != nil {
		t.Fatal(err)
	}
	return wire.Transaction
}

// Breakage: schema5's strict absence codec accepts a Windows/native/file
// carrier, while schema6 accepts a missing or mismatched participant. These
// mutations use the checked-in carrier body, not production expected builders.
func TestWindows6AndOrphanParticipantSeparation(t *testing.T) {
	base := mustWindowsBody(t, windowsFixtureEnvelope(t))
	var body map[string]any
	if err := json.Unmarshal(base, &body); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"missing-participant": func(m map[string]any) { delete(m, "Windows") },
		"orphan-carrier":      func(m map[string]any) { m["OrphanRecovery"] = map[string]any{} },
		"mismatched-floor": func(m map[string]any) {
			after := m["After"].(map[string]any)
			after["WriterFloor"] = float64(OpenCodeWriterFloor)
		},
		"mismatched-binding": func(m map[string]any) {
			after := m["After"].(map[string]any)
			after["WindowsRetained"] = []any{}
		},
		"mismatched-immutable-digest": func(m map[string]any) {
			binding := m["Windows"].(map[string]any)["Binding"].(map[string]any)
			binding["SHA256"] = strings.Repeat("0", 64)
		},
		"mismatched-immutable-path": func(m map[string]any) {
			binding := m["Windows"].(map[string]any)["Binding"].(map[string]any)
			binding["SnapshotPath"] = `\\?\C:\TEST\foreign\generation.wne`
		},
		"unknown-retained-binding": func(m map[string]any) {
			binding := m["After"].(map[string]any)["WindowsRetained"].([]any)[0].(map[string]any)
			binding["Unknown"] = true
		},
		"casefold-retained-binding": func(m map[string]any) {
			binding := m["After"].(map[string]any)["WindowsRetained"].([]any)[0].(map[string]any)
			binding["sha256"] = binding["SHA256"]
			delete(binding, "SHA256")
		},
		"unknown-transaction-field": func(m map[string]any) {
			m["FutureReplay"] = map[string]any{}
		},
		"unknown-files-field": func(m map[string]any) {
			m["Files"].(map[string]any)["FutureChanges"] = []any{}
		},
	} {
		t.Run(name, func(t *testing.T) {
			var copy map[string]any
			if err := json.Unmarshal(base, &copy); err != nil {
				t.Fatal(err)
			}
			mutate(copy)
			badBody, err := json.Marshal(copy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeTransaction(orphanEnvelope(t, badBody)); err == nil {
				t.Fatalf("malformed schema6 accepted: %s", name)
			}
		})
	}

	// The schema5 body is independently generated from the fixture and gains
	// an ordinary payload. Exact orphan JSON must reject it before replay.
	five := map[string]any{}
	if err := json.Unmarshal(base, &five); err != nil {
		t.Fatal(err)
	}
	five["Schema"] = float64(transactionSchemaV5)
	five["OrphanRecovery"] = map[string]any{"ControlRoot": "foreign"}
	five["Windows"] = body["Windows"]
	fiveBody, err := json.Marshal(five)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeTransaction(orphanEnvelope(t, fiveBody)); err == nil || !strings.Contains(err.Error(), "schema5") {
		t.Fatalf("schema5 admitted Windows carrier: %v", err)
	}
}
