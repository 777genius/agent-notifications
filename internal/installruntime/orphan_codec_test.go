package installruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func orphanEnvelope(t *testing.T, body []byte) []byte {
	t.Helper()
	sum := sha256.Sum256(body)
	data, err := json.Marshal(transactionEnvelope{SHA256: hex.EncodeToString(sum[:]), Transaction: body})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestOrphanSchema5ChecksumValidMalformedDecisionsCannotReplay(t *testing.T) {
	f := newOrphanFixture(t)
	f.abandon(t, true)
	tx := f.interrupt(t, "transaction")
	body, err := marshalTransaction(tx)
	if err != nil {
		t.Fatal(err)
	}
	object := func(m map[string]any, k string) map[string]any { return m[k].(map[string]any) }
	mutations := map[string]func(map[string]any){
		"unknown-decision-field": func(m map[string]any) { object(m, "OrphanRecovery")["Force"] = true },
		"case-folded-request-field": func(m map[string]any) {
			r := object(object(m, "OrphanRecovery"), "Request")
			r["runtimeRoot"] = r["RuntimeRoot"]
			delete(r, "RuntimeRoot")
		},
		"unknown-consumer-field": func(m map[string]any) {
			object(object(object(m, "OrphanRecovery"), "Request"), "Consumer")["Force"] = true
		},
		"duplicate-selected-path": func(m map[string]any) { d := object(m, "OrphanRecovery"); p := d["Files"].([]any); p[1] = p[0] },
		"unsorted-selected-path": func(m map[string]any) {
			d := object(m, "OrphanRecovery")
			p := d["Files"].([]any)
			p[0], p[1] = p[1], p[0]
		},
		"namespace-escape": func(m map[string]any) {
			object(m, "OrphanRecovery")["Files"].([]any)[0] = filepath.Join(f.primary, "asset-00")
		},
		"external-registration-in-removal-set": func(m map[string]any) { object(m, "OrphanRecovery")["Files"].([]any)[0] = f.registration },
		"missing-registration-observation":     func(m map[string]any) { object(m, "OrphanRecovery")["RegistrationAbsence"] = nil },
		"missing-runtime-observation": func(m map[string]any) {
			d := object(m, "OrphanRecovery")
			d["RuntimeAbsences"] = d["RuntimeAbsences"].([]any)[1:]
		},
		"absence-missing-escape": func(m map[string]any) {
			object(m, "OrphanRecovery")["RuntimeAbsences"].([]any)[0].(map[string]any)["Missing"] = filepath.Join(f.root, "other")
		},
		"missing-policy-preimage": func(m map[string]any) { delete(object(m, "OrphanRecovery"), "Policy") },
		"identity-changed": func(m map[string]any) {
			object(m, "OrphanRecovery")["Identities"].([]any)[0].(map[string]any)["Mode"] = float64(0777)
		},
		"enabled-change":      func(m map[string]any) { object(m, "After")["Enabled"] = false },
		"owner-change":        func(m map[string]any) { object(m, "After")["Owner"] = "foreign" },
		"installation-change": func(m map[string]any) { object(m, "After")["ID"] = "foreign" },
		"floor-change":        func(m map[string]any) { object(m, "After")["WriterFloor"] = float64(SupportedWriterFloor) },
		"primary-change":      func(m map[string]any) { object(m, "After")["RuntimeRoot"] = f.runtime },
		"retained-file-removed": func(m map[string]any) {
			delete(object(object(m, "After"), "Files"), filepath.Join(f.primary, "asset-00"))
		},
		"retained-consumer-removed": func(m map[string]any) { delete(object(object(m, "After"), "Consumers"), "retained") },
		"unrequested-native-record": func(m map[string]any) { object(m, "After")["Native"] = map[string]any{} },
		"extra-generation":          func(m map[string]any) { a := object(m, "After"); a["Generation"] = a["Generation"].(float64) + 1 },
		"policy-generation-jump": func(m map[string]any) {
			a := object(m, "After")
			a["PolicyGeneration"] = a["PolicyGeneration"].(float64) + 2
		},
		"extra-config":         func(m map[string]any) { m["ConfigPaths"] = []any{f.registration} },
		"file-publication":     func(m map[string]any) { object(m, "Files")["Changes"] = []any{map[string]any{}} },
		"legacy-files-carrier": func(m map[string]any) { m["Files"] = []any{} },
		"null-scalar":          func(m map[string]any) { object(m, "After")["Enabled"] = nil },
		"null-map":             func(m map[string]any) { object(m, "After")["Files"] = nil },
		"fake-rollback":        func(m map[string]any) { m["Rollback"] = true },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			var m map[string]any
			if err := json.Unmarshal(body, &m); err != nil {
				t.Fatal(err)
			}
			mutate(m)
			badBody, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			bad := orphanEnvelope(t, badBody)
			if _, err := decodeTransaction(bad); err == nil {
				t.Fatal("checksum-valid invalid decision decoded")
			}
			if err := os.WriteFile(filepath.Join(f.control, "transaction.json"), bad, 0600); err != nil {
				t.Fatal(err)
			}
			images := orphanControlImages(t, f.control)
			if _, err := Recover(f.ctx, f.control); err == nil {
				t.Fatal("checksum-valid invalid decision replayed")
			}
			assertOrphanImages(t, images)
		})
	}
	// Duplicate keys are invalid even when their values agree and checksum is valid.
	duplicate := bytes.Replace(body, []byte(`"Schema":5`), []byte(`"Schema":5,"Schema":5`), 1)
	if _, err := decodeTransaction(orphanEnvelope(t, duplicate)); err == nil {
		t.Fatal("checksum-valid duplicate field decoded")
	}
}

func TestOrphanCodecRoundTripPreservesCompleteDecision(t *testing.T) {
	f := newOrphanFixture(t)
	f.abandon(t, true)
	tx := f.interrupt(t, "transaction")
	body, err := marshalTransaction(tx)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeTransaction(orphanEnvelope(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, tx) {
		t.Fatal("decision/before-images changed across codec")
	}
	if len(tx.OrphanRecovery.Files) != 21 || tx.OrphanRecovery.RegistrationAbsence.Path != f.registration {
		t.Fatal("runtime/external registration scopes lost")
	}
	for _, path := range tx.OrphanRecovery.Files {
		if path == f.registration {
			t.Fatal("external registration added to de-owned files")
		}
	}
	if tx.Before.Schema != tx.After.Schema || tx.Before.WriterFloor != tx.After.WriterFloor {
		t.Fatal("completed ledger unnecessarily upgraded durable protocol")
	}
}

// Frozen former transaction shape: adding an omitted recovery field must never
// change the legacy bytes. The actual frozen base decoder is also executed in
// worker evidence against a schema5 journal, before any publication.
type formerTransaction struct {
	OpenCodeInit  *File      `json:",omitempty"`
	OpenCodePurge *PurgeTree `json:",omitempty"`
	ConfigPaths   []string
	Native        *NativeChange
	Schema        int
	Before, After Ledger
	Files         []File
	Rollback      bool `json:",omitempty"`
}

func TestOrphanSeamPreservesLegacyTransactionBytes(t *testing.T) {
	for _, schema := range []int{1, 2, 3, 4} {
		tx := recoveryTransaction()
		tx.Schema = schema
		tx.Files = []File{{Path: filepath.Join(string(filepath.Separator), "test", "asset"), Data: []byte("payload"), Mode: 0600}}
		if schema == 4 {
			tx.After.WriterFloor = LocalPolicyWriterFloor
		}
		old := formerTransaction{ConfigPaths: tx.ConfigPaths, Native: tx.Native, Schema: tx.Schema, Before: tx.Before, After: tx.After, Files: tx.Files}
		var want []byte
		var err error
		if schema == 4 {
			w := struct {
				*formerTransaction
				Files struct{ Changes []File }
			}{formerTransaction: &old}
			w.Files.Changes = old.Files
			want, err = json.Marshal(w)
		} else {
			want, err = json.Marshal(old)
		}
		if err != nil {
			t.Fatal(err)
		}
		got, err := marshalTransaction(tx)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("legacy schema%d bytes changed", schema)
		}
		decoded, err := decodeTransaction(orphanEnvelope(t, got))
		if err != nil || !reflect.DeepEqual(decoded, tx) {
			t.Fatalf("legacy schema%d round trip failed: %v", schema, err)
		}
	}
}
