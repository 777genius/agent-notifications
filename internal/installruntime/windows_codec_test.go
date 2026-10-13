package installruntime

import (
	"bytes"
	"context"
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

func windowsImmutableNativeFixture(t *testing.T) map[string]any {
	t.Helper()
	body := mustWindowsBody(t, windowsFixtureEnvelope(t))
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	native := map[string]any{
		"DirectoryID":         "native-directory",
		"PreviousDirectoryID": "",
		"Attestation":         "",
		"InstalledTreeSHA256": strings.Repeat("1", 64),
		"Path":                `\\?\C:\TEST\native\generation-native-directory.app`,
		"SHA256":              strings.Repeat("2", 64),
		"PreviousPath":        "",
		"PreviousSHA256":      "",
		"DecoderFloor":        float64(1),
		"Published": []any{map[string]any{
			"DirectoryID":         "published-directory",
			"Path":                `\\?\C:\TEST\native\generation-published-directory.app`,
			"SHA256":              strings.Repeat("3", 64),
			"DecoderFloor":        float64(1),
			"Attestation":         "",
			"InstalledTreeSHA256": strings.Repeat("4", 64),
		}},
	}
	encodedNative, err := json.Marshal(native)
	if err != nil {
		t.Fatal(err)
	}
	var decodedNative any
	if err := json.Unmarshal(encodedNative, &decodedNative); err != nil {
		t.Fatal(err)
	}
	for _, side := range []string{"Before", "After"} {
		ledger := value[side].(map[string]any)
		ledger["DecoderFloor"] = float64(1)
		ledger["Native"] = decodedNative
	}
	return value
}

func windowsFixtureMap(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var copy map[string]any
	if err := json.Unmarshal(data, &copy); err != nil {
		t.Fatal(err)
	}
	return copy
}

// Breakage: schema6 uses its separate floor6 declaration to promote the retained
// NativeRecord or a published native generation. These mutations are persisted
// as checksum-valid schema6 journals and must fail decode, full recovery, and
// reverse-decision construction before any policy/ledger/file/Windows effect.
func TestWindows6ImmutableNativeFloorsRejectPersistedDecodeRecoveryReverse(t *testing.T) {
	base := windowsImmutableNativeFixture(t)
	legalBody, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	legal, err := decodeTransaction(orphanEnvelope(t, legalBody))
	if err != nil {
		t.Fatal(err)
	}
	if legal.Before.DecoderFloor != 1 || legal.After.DecoderFloor != 1 ||
		legal.Before.Native == nil || legal.After.Native == nil ||
		legal.Before.Native.DecoderFloor != 1 || legal.After.Native.DecoderFloor != 1 ||
		len(legal.After.Native.Published) != 1 || legal.After.Native.Published[0].DecoderFloor != 1 ||
		!reflect.DeepEqual(legal.Before.Native, legal.After.Native) {
		t.Fatalf("legal Windows journal changed retained native floors: %+v", legal)
	}
	reversed, err := reverseTransaction(legal.Before, legal)
	if err != nil || !reflect.DeepEqual(reversed.Before.Native, legal.Before.Native) ||
		!reflect.DeepEqual(reversed.After.Native, legal.After.Native) ||
		reversed.After.DecoderFloor != 1 || reversed.After.Native.DecoderFloor != 1 ||
		reversed.After.Native.Published[0].DecoderFloor != 1 {
		t.Fatalf("legal Windows reverse changed retained native floors: %+v %v", reversed, err)
	}

	for name, mutate := range map[string]func(map[string]any){
		"decoder-floor-1-to-6": func(v map[string]any) {
			v["After"].(map[string]any)["DecoderFloor"] = float64(6)
		},
		"decoder-floor-1-to-2": func(v map[string]any) {
			v["After"].(map[string]any)["DecoderFloor"] = float64(2)
		},
		"native-record-floor": func(v map[string]any) {
			v["After"].(map[string]any)["Native"].(map[string]any)["DecoderFloor"] = float64(2)
		},
		"native-record-path": func(v map[string]any) {
			v["After"].(map[string]any)["Native"].(map[string]any)["Path"] = `\\?\C:\TEST\foreign\generation.app`
		},
		"native-record-digest": func(v map[string]any) {
			v["After"].(map[string]any)["Native"].(map[string]any)["SHA256"] = strings.Repeat("5", 64)
		},
		"published-generation-floor": func(v map[string]any) {
			published := v["After"].(map[string]any)["Native"].(map[string]any)["Published"].([]any)[0].(map[string]any)
			published["DecoderFloor"] = float64(2)
		},
		"published-generation-history-floor": func(v map[string]any) {
			published := v["Before"].(map[string]any)["Native"].(map[string]any)["Published"].([]any)[0].(map[string]any)
			published["DecoderFloor"] = float64(6)
		},
		"native-change-participant": func(v map[string]any) {
			v["Native"] = map[string]any{"Before": v["Before"].(map[string]any)["Native"], "After": v["After"].(map[string]any)["Native"]}
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := windowsFixtureMap(t, base)
			mutate(value)
			badBody, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			envelope := orphanEnvelope(t, badBody)
			root := t.TempDir()
			marker := filepath.Join(root, "transaction.json")
			if err := os.WriteFile(marker, envelope, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := decodeTransaction(envelope); err == nil {
				t.Fatal("checksum-valid malformed schema6 journal decoded")
			}
			if _, err := readTransactionFile(marker); err == nil {
				t.Fatal("persisted malformed schema6 journal decoded")
			}

			// Full recovery is checked after writing the invalid journal but
			// before the normal decoder can admit it. The direct form below
			// separately proves recoverTransaction is its own validation seam.
			effects := 0
			_, err = Commit(context.Background(), Request{
				ControlRoot: root,
				RecoverOnly: true,
				Fault: func(string) error {
					effects++
					return nil
				},
			})
			if err == nil {
				t.Fatal("recovery accepted malformed schema6 journal")
			}
			raw, err := os.ReadFile(marker)
			if err != nil || !bytes.Equal(raw, envelope) {
				t.Fatalf("rejected journal or recovery changed durable decision: %v", err)
			}
			for _, effect := range []string{"policy-generation.json", "ownership.json"} {
				if _, err := os.Lstat(filepath.Join(root, effect)); !os.IsNotExist(err) {
					t.Fatalf("rejected journal created %s: %v", effect, err)
				}
			}
			if _, err := os.Lstat(transactionBlobDir(marker)); !os.IsNotExist(err) {
				t.Fatalf("rejected journal attached or created blobs: %v", err)
			}
			if effects != 0 {
				t.Fatalf("recovery crossed an effect boundary: %d", effects)
			}

			var unchecked transaction
			wire := windowsTransactionWire{transaction: &unchecked}
			if err := json.Unmarshal(badBody, &wire); err != nil {
				t.Fatal(err)
			}
			unchecked.Files = wire.Files.Changes
			effects = 0
			if err := recoverTransaction(context.Background(), root, unchecked.Before, unchecked, func(string) error {
				effects++
				return nil
			}); err == nil {
				t.Fatal("direct recovery accepted malformed schema6 journal")
			}
			if effects != 0 {
				t.Fatalf("direct recovery crossed an effect boundary: %d", effects)
			}
			if _, err := reverseTransaction(unchecked.Before, unchecked); err == nil {
				t.Fatal("reverse decision accepted malformed schema6 journal")
			}
		})
	}
}
