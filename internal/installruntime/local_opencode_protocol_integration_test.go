package installruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Independent public contracts: published Local keeps ledger3/floor3; a private
// OpenCode decision requires a higher barrier and survives the same redo journal.
// RED: conflating either floor3 ledger kind, or dropping Init on replay.
func TestLocalOpenCodeRecoveryKeepsDistinctProtocols(t *testing.T) {
	for _, boundary := range []string{"transaction", "opencode-init"} {
		t.Run(boundary, func(t *testing.T) {
			ctx, r := privateRegistrationRequest(t)
			sibling := r
			sibling.ConsumerID = "TEST-local-sibling"
			sibling.Consumer = Consumer{Registration: "TEST-recorded-sibling"}
			keep := filepath.Join(r.RuntimeRoot, "TEST-foreign-kept")
			sibling.Files = []File{{Path: keep, Data: []byte("retained TEST bytes"), Mode: 0600}}
			local, err := Commit(ctx, sibling)
			if err != nil {
				t.Fatal(err)
			}
			patch := Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot, Owner: r.Owner,
				ConsumerID: sibling.ConsumerID, RefreshOnly: true, PolicyOnly: true,
				ExpectedGeneration: &local.Generation,
				PolicyFields:       map[string]json.RawMessage{"route": json.RawMessage(`{"copilotVSCodeNotifications":{"desktop":false,"webhook":false,"manual":{"enabled":true,"foreign":[1,2]},"future":{"keep":7}},"openCodeNotifications":{"desktop":false,"webhook":false}}`)}}
			local, err = Commit(ctx, patch)
			if err != nil {
				t.Fatal(err)
			}
			if local.Schema != 3 || local.WriterFloor != 3 {
				t.Fatalf("Local contract changed: schema%d floor%d", local.Schema, local.WriterFloor)
			}
			policyBefore, err := os.ReadFile(filepath.Join(r.ControlRoot, "agent-notifications.json"))
			if err != nil {
				t.Fatal(err)
			}
			// Force genuine out-of-band journal bytes. A floor gate after blob IO
			// would report missing blobs instead of the compatibility refusal.
			bundle := append([]byte("inert TEST origin"), bytes.Repeat([]byte("TEST"), 300000)...)
			r.Files[0].Data = bundle
			r.Consumer.OpenCode.BundleSHA256 = identity(bundle, r.Files[0].Mode).SHA256
			r.ExpectedGeneration = &local.Generation
			crash := errors.New("TEST interrupted public boundary")
			r.Fault = func(phase string) error {
				if phase == boundary {
					return crash
				}
				return nil
			}
			if _, err = Commit(ctx, r); !errors.Is(err, crash) {
				t.Fatalf("boundary not reached: %v", err)
			}
			journal, err := os.ReadFile(filepath.Join(r.ControlRoot, "transaction.json"))
			if err != nil {
				t.Fatal(err)
			}
			var wire struct {
				Transaction struct {
					Schema       int
					After        struct{ WriterFloor int }
					Files        struct{ Changes []File }
					OpenCodeInit *File
				}
			}
			if json.Unmarshal(journal, &wire) != nil || wire.Transaction.Schema != 4 || wire.Transaction.After.WriterFloor != 4 || wire.Transaction.OpenCodeInit == nil || len(wire.Transaction.Files.Changes) == 0 || wire.Transaction.Files.Changes[0].DataSHA256 == "" {
				t.Fatal("private recovery decision/writer barrier/versioned carrier absent")
			}
			// Optional finite data export for ROOT's separate actual e637 decoder
			// gate. It is not a production hook and no historic kernel runs here.
			if out := os.Getenv("AN_TEST_FLOOR4_EXPORT"); out != "" && boundary == "transaction" {
				if !filepath.IsAbs(out) || !strings.HasPrefix(filepath.Base(out), "TEST-") {
					t.Fatal("fresh absolute TEST export required")
				}
				if err = os.Mkdir(out, 0700); err != nil {
					t.Fatal(err)
				}
				before, err := json.Marshal(local)
				if err != nil {
					t.Fatal(err)
				}
				data, err := json.Marshal(struct{ Journal, Before, Policy json.RawMessage }{journal, before, policyBefore})
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(out, "fixture.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			after, err := Recover(ctx, r.ControlRoot)
			if err != nil {
				t.Fatal(err)
			}
			if after.Schema != 4 || after.WriterFloor != 4 || after.ID != local.ID || !reflect.DeepEqual(after.Consumers[sibling.ConsumerID], local.Consumers[sibling.ConsumerID]) {
				t.Fatal("recovery lost protocol/retained consumer")
			}
			got, err := os.ReadFile(keep)
			if err != nil || string(got) != "retained TEST bytes" {
				t.Fatal("sibling bytes changed", err)
			}
			policyAfter, err := os.ReadFile(filepath.Join(r.ControlRoot, "agent-notifications.json"))
			if err != nil || !bytes.Equal(policyBefore, policyAfter) {
				t.Fatal("shared consent/foreign leaves changed", err)
			}
			reg := after.Consumers[openCodeConsumer].OpenCode
			store, err := AcquireOpenCodeStore(ctx, r.ControlRoot, *reg)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := store.Read()
			store.Close()
			if err != nil || string(payload) != "{}" {
				t.Fatal("initial claims missing", err)
			}
			// Local policy remains editable beside the retained private protocol;
			// its own promotion must never demote OpenCode floor4/ledger4.
			patch.ExpectedGeneration = &after.Generation
			patch.PolicyFields = map[string]json.RawMessage{"route": json.RawMessage(`{"copilotVSCodeNotifications":{"desktop":true}}`)}
			after, err = Commit(ctx, patch)
			if err != nil || after.Schema != 4 || after.WriterFloor != 4 {
				t.Fatal("Local edit demoted private protocol", err)
			}
			var policy map[string]any
			b, err := os.ReadFile(filepath.Join(r.ControlRoot, "agent-notifications.json"))
			if err != nil || json.Unmarshal(b, &policy) != nil {
				t.Fatal("policy unreadable", err)
			}
			localRoute := policy["route"].(map[string]any)["copilotVSCodeNotifications"].(map[string]any)
			if localRoute["desktop"] != true || localRoute["manual"].(map[string]any)["enabled"] != true || localRoute["future"].(map[string]any)["keep"] != float64(7) {
				t.Fatal("Local edit erased retained leaves")
			}
			lockBefore, err := os.Lstat(filepath.Join(r.ControlRoot, OpenCodeStoreLock))
			if err != nil {
				t.Fatal(err)
			}
			remove := Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot, Owner: r.Owner,
				ConsumerID: openCodeConsumer, RemoveConsumer: true, ExpectedGeneration: &after.Generation,
				Files: []File{{Path: r.Consumer.Registration, Before: after.Files[r.Consumer.Registration], Remove: true}},
				Fault: func(phase string) error {
					if phase == "transaction" {
						return crash
					}
					return nil
				}}
			if _, err = Commit(ctx, remove); !errors.Is(err, crash) {
				t.Fatal("purge checkpoint absent", err)
			}
			if after, err = Recover(ctx, r.ControlRoot); err != nil {
				t.Fatal(err)
			}
			if _, exists := after.Consumers[openCodeConsumer]; exists {
				t.Fatal("private purge retained active registration")
			}
			if after.Schema != 4 || after.WriterFloor != 4 || after.ID != local.ID || !reflect.DeepEqual(after.Consumers[sibling.ConsumerID], local.Consumers[sibling.ConsumerID]) {
				t.Fatal("purge lost retained protocol/Local peer")
			}
			if _, err = os.Lstat(filepath.Dir(openCodeStatePath(r.ControlRoot, *reg))); !os.IsNotExist(err) {
				t.Fatal("purge failed to finish private namespace", err)
			}
			lockAfter, err := os.Lstat(filepath.Join(r.ControlRoot, OpenCodeStoreLock))
			if err != nil || !os.SameFile(lockBefore, lockAfter) {
				t.Fatal("permanent admission lock changed", err)
			}
		})
	}
}

// RED: a Local3-only package can be installed over a private OpenCode decision
// even though its public recovery kernel ignores Init/Purge.
func TestOpenCodeFloorRejectsPublishedLocalWriter(t *testing.T) {
	for _, marker := range []string{"agent-notifications-managed-writer-protocol-v1", "agent-notifications-managed-writer-protocol-v3"} {
		t.Run(marker, func(t *testing.T) {
			ctx, r := privateRegistrationRequest(t)
			l, err := Commit(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			before := recoveryFilesystemSnapshot(t, filepath.Dir(r.ControlRoot))
			r.RefreshOnly, r.ExpectedGeneration = true, &l.Generation
			r.Files = []File{{Path: filepath.Join(r.RuntimeRoot, "claude-notifications-linux-amd64"), Data: []byte("inert TEST " + marker), Mode: 0700}}
			if _, err = Commit(ctx, r); err == nil || !strings.Contains(err.Error(), "protocol floor 4") {
				t.Fatalf("unaware package accepted: %v", err)
			}
			if !reflect.DeepEqual(before, recoveryFilesystemSnapshot(t, filepath.Dir(r.ControlRoot))) {
				t.Fatal("writer refusal mutated installed state")
			}
		})
	}
}
