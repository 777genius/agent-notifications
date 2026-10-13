//go:build darwin

package installruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Red if private Init keeps historical devices after ordinary file replay,
// or if its parent chain is checked independently of the transaction bijection.
// These are inert files in t.TempDir: no installer/native process is executed.
func TestOpenCodeInitRecoveryAfterDeviceRenumber(t *testing.T) {
	for _, mode := range []string{"renumber", "substituted-inode", "conflicting-device"} {
		t.Run(mode, func(t *testing.T) {
			ctx, r := privateRegistrationRequest(t)
			physicalRoot, err := CanonicalPath(r.ControlRoot)
			if err != nil {
				t.Fatal("canonical private control root", err)
			}
			r.ControlRoot = physicalRoot
			crash := errors.New("private init interruption")
			r.Fault = func(phase string) error {
				if phase == "transaction" {
					return crash
				}
				return nil
			}
			if _, err := Commit(ctx, r); !errors.Is(err, crash) {
				t.Fatal("private transaction did not reach interruption", err)
			}
			marker := filepath.Join(r.ControlRoot, "transaction.json")
			tx, err := readTransactionFile(marker)
			if err != nil || tx.OpenCodeInit == nil || len(tx.OpenCodeInit.Parents) == 0 {
				t.Fatal("missing private Init fixture", err)
			}
			// Model historical boot devices using the existing injective fixture
			// mapping. Real paths/inodes are independently observed by recovery.
			for i := range tx.Files {
				for j := range tx.Files[i].Parents {
					tx.Files[i].Parents[j].Identity = renumberID(tx.Files[i].Parents[j].Identity)
				}
			}
			for i := range tx.OpenCodeInit.Parents {
				tx.OpenCodeInit.Parents[i].Identity = renumberID(tx.OpenCodeInit.Parents[i].Identity)
			}
			last := &tx.OpenCodeInit.Parents[len(tx.OpenCodeInit.Parents)-1]
			switch mode {
			case "substituted-inode":
				last.Identity = objectDevice(last.Identity) + ":0"
			case "conflicting-device":
				// Keep this whole chain independently bijective, but contradict
				// the once-renumbered Files chains at their common current volumes.
				for i := range tx.OpenCodeInit.Parents {
					tx.OpenCodeInit.Parents[i].Identity = renumberID(tx.OpenCodeInit.Parents[i].Identity)
				}
				fresh, err := pathAnchors(tx.OpenCodeInit.Path, false)
				if err != nil || checkPersistedAnchors(tx.OpenCodeInit.Parents, fresh) != nil {
					t.Fatal("fixture Init chain is not independently valid", err)
				}
			}
			if err := writeTransaction(marker, tx); err != nil {
				t.Fatal(err)
			}
			journal, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			current, err := readLedger(r.ControlRoot)
			if err != nil {
				t.Fatal(err)
			}
			supplied, err := json.Marshal(tx)
			if err != nil {
				t.Fatal(err)
			}
			policyPath := filepath.Join(r.ControlRoot, "policy-generation.json")
			policy, err := Fingerprint(policyPath)
			if err != nil {
				t.Fatal(err)
			}
			err = recoverTransaction(ctx, r.ControlRoot, current, tx, func(phase string) error {
				if phase == "opencode-init" {
					return crash
				}
				return nil
			})
			if mode == "renumber" && !errors.Is(err, crash) {
				t.Fatalf("valid renumber did not publish private Init: %v", err)
			}
			if mode != "renumber" && (err == nil || errors.Is(err, crash)) {
				t.Fatal("unsafe private parent reached publication", err)
			}
			after, marshalErr := json.Marshal(tx)
			stored, readErr := os.ReadFile(marker)
			if marshalErr != nil || readErr != nil || !bytes.Equal(supplied, after) || !bytes.Equal(journal, stored) {
				t.Fatal("replay mutated supplied or durable private decision", marshalErr, readErr)
			}
			if mode != "renumber" {
				if _, err := os.Lstat(tx.OpenCodeInit.Path); !os.IsNotExist(err) {
					t.Fatal("refusal created private state", err)
				}
				if _, err := os.Lstat(r.Consumer.Registration); !os.IsNotExist(err) {
					t.Fatal("refusal published registration", err)
				}
				if got, err := Fingerprint(policyPath); err != nil || got != policy {
					t.Fatal("refusal changed policy", err)
				}
				return
			}
			result, err := Recover(ctx, r.ControlRoot)
			if err != nil || result.WriterFloor != OpenCodeWriterFloor || result.Schema != 4 {
				t.Fatal("private forward recovery lost protocol", err)
			}
			registration := result.Consumers[openCodeConsumer].OpenCode
			if registration == nil || *registration != *r.Consumer.OpenCode {
				t.Fatal("private incarnation changed during recovery")
			}
			store, err := AcquireOpenCodeStore(ctx, r.ControlRoot, *registration)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := store.Read()
			store.Close()
			if err != nil || string(payload) != "{}" {
				t.Fatal("private seed changed during replay", err)
			}
			if _, err := os.Lstat(marker); !os.IsNotExist(err) {
				t.Fatal("successful recovery retained journal", err)
			}
		})
	}
}
