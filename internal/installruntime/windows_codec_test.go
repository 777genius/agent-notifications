package installruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/strictjson"
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
		tx.After.WriterFloor != WindowsGenerationWriterFloor || tx.Windows == nil ||
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

// frozenFormerSchema4Decode copies the former v4 dispatch order: validate the
// checksum and supported schema, decode its versioned Files carrier, and only
// then attach referenced blobs. It intentionally has no schema6 branch.
func frozenFormerSchema4Decode(data []byte, openBlob func(string) ([]byte, error)) ([]File, error) {
	if strictjson.Validate(data, strictjson.Budget{Bytes: maxManagedFile, Depth: 32, Entries: 100000}) != nil {
		return nil, os.ErrInvalid
	}
	var envelope transactionEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, envelope.Transaction); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(compact.Bytes())
	if envelope.SHA256 != hex.EncodeToString(digest[:]) {
		return nil, os.ErrInvalid
	}
	var header struct{ Schema int }
	if err := json.Unmarshal(envelope.Transaction, &header); err != nil {
		return nil, err
	}
	if header.Schema < 1 || header.Schema > 5 {
		return nil, os.ErrInvalid
	}
	if header.Schema == 5 {
		return nil, os.ErrInvalid
	}
	var wire transactionV4
	if err := json.Unmarshal(envelope.Transaction, &wire); err != nil {
		return nil, err
	}
	for _, file := range wire.Files.Changes {
		if file.DataSHA256 != "" {
			if _, err := openBlob(file.DataSHA256); err != nil {
				return nil, err
			}
		}
	}
	return wire.Files.Changes, nil
}

// frozenFormerSchema5Decode is the former orphan-only reader's exact strict
// admission codec. It has no ordinary write or Windows participant branch.
func frozenFormerSchema5Decode(data []byte) error {
	var envelope transactionEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	var header struct{ Schema int }
	if err := json.Unmarshal(envelope.Transaction, &header); err != nil {
		return err
	}
	if header.Schema != transactionSchemaV5 {
		return os.ErrInvalid
	}
	if err := validateOrphanEnvelope(data); err != nil {
		return err
	}
	return unmarshalOrphanTransaction(envelope.Transaction, new(transaction))
}

// Breakage: an old reader merely increments its schema gate and opens blobs or
// performs product mutation before discovering the Windows6 carrier. These
// frozen implementations prove refusal at the historical decode boundary.
func TestFrozenFormerReadersRejectWindows6BeforeBlobs(t *testing.T) {
	if acceptedTransactionSchema(7) {
		t.Fatal("unsupported schema7 accepted")
	}
	base := mustWindowsBody(t, windowsFixtureEnvelope(t))
	var body map[string]any
	if err := json.Unmarshal(base, &body); err != nil {
		t.Fatal(err)
	}
	body["Files"].(map[string]any)["Changes"] = []any{map[string]any{
		"Path": filepath.Join(t.TempDir(), "must-not-publish"), "Data": []byte("must-not-publish"),
		"DataSHA256": strings.Repeat("a", 64), "Mode": float64(0600),
	}}
	mutated, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	withBlob := orphanEnvelope(t, mutated)
	touched := false
	if _, err := frozenFormerSchema4Decode(withBlob, func(string) ([]byte, error) {
		touched = true
		return nil, os.ErrNotExist
	}); err == nil || touched {
		t.Fatalf("former schema4 reader reached blob attachment: touched=%v err=%v", touched, err)
	}
	if err := frozenFormerSchema5Decode(withBlob); err == nil {
		t.Fatal("former schema5 reader admitted an ordinary Windows6 carrier")
	}

	// The v1-v3 []File carrier cannot represent the schema4/6 versioned object.
	// The former type error occurs before any blob or product operation.
	var legacy struct {
		Schema int
		Files  []File
	}
	if err := json.Unmarshal(mutated, &legacy); err == nil {
		t.Fatal("former v1-v3 reader decoded the versioned Files object")
	}

	body["Schema"] = float64(7)
	badBody, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeTransaction(orphanEnvelope(t, badBody)); err == nil || !strings.Contains(err.Error(), "unsupported transaction schema") {
		t.Fatalf("schema7 reached decoder: %v", err)
	}
}
