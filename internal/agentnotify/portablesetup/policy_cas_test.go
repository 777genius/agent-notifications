//go:build linux || darwin

package portablesetup

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Old publication ignores the verified policy identity: a manual same-generation
// enabled=false edit still publishes an intent. Old CommitBinding likewise adds
// a consumer/locator after reservation. Both must refuse through the kernel CAS.
func TestBootstrapPolicySameGenerationProtectedCAS(t *testing.T) {
	for _, boundary := range []string{"reservation", "binding", "patch", "finalization"} {
		t.Run(boundary, func(t *testing.T) {
			ctx := testCtx(t)
			b, l := bindingFixture(t)
			on := true
			gen := l.Generation
			l, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Owner: b.Owner, ConsumerID: "existing", RefreshOnly: true, PolicyOnly: true, ExpectedGeneration: &gen, PolicyEnabled: &on})
			if err != nil {
				t.Fatal(err)
			}
			observed, err := installruntime.ReadPolicySnapshot(ctx, b.ControlRoot)
			if err != nil {
				t.Fatal(err)
			}
			req := ConfirmedIntent{ControlRoot: b.ControlRoot, RuntimeRoot: b.RuntimeRoot, Owner: b.Owner, ExpectedGeneration: l.Generation, ExpectedPolicy: &observed.Preimage, Action: "install", Targets: []IntentTarget{{Client: "codex", Units: []string{"agent-notify"}}}}
			service := Service{ExpectedPolicy: &observed.Preimage}
			var reservation *installruntime.PendingMutation
			if boundary != "reservation" {
				l, reservation, err = service.PublishConfirmedIntent(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
			}
			policyPath := filepath.Join(b.ControlRoot, "agent-notifications.json")
			raw, err := os.ReadFile(policyPath)
			if err != nil {
				t.Fatal(err)
			}
			edited := bytes.Replace(raw, []byte(`"enabled": true`), []byte(`"enabled": false`), 1)
			if bytes.Equal(raw, edited) {
				t.Fatalf("fixture lacks enabled field: %s", raw)
			}
			if err := os.WriteFile(policyPath, edited, 0600); err != nil {
				t.Fatal(err)
			}
			ownership, err := os.ReadFile(filepath.Join(b.ControlRoot, "ownership.json"))
			if err != nil {
				t.Fatal(err)
			}
			var intent []byte
			if boundary != "reservation" {
				intent, err = os.ReadFile(IntentPath(b.ControlRoot))
				if err != nil {
					t.Fatal(err)
				}
			}
			switch boundary {
			case "reservation":
				_, _, err = service.PublishConfirmedIntent(ctx, req)
			case "binding":
				_, err = service.CommitBinding(ctx, Request{Binding: b, ExpectedGeneration: l.Generation, Reservation: reservation})
			case "patch":
				err = service.PatchIntentReceipt(ctx, b.ControlRoot, b.RuntimeRoot, b.Owner, "codex", "TEST-receipt", reservation)
			default:
				_, _, err = service.FinishConfirmedIntent(ctx, req, reservation)
			}
			if !errors.Is(err, ErrConcurrentChange) || !errors.Is(err, installruntime.ErrPolicyConflict) {
				t.Fatalf("manual opt-out passed %s CAS: %v", boundary, err)
			}
			after, e := os.ReadFile(filepath.Join(b.ControlRoot, "ownership.json"))
			if e != nil || !bytes.Equal(after, ownership) {
				t.Fatalf("refused CAS changed ledger: %v", e)
			}
			after, e = os.ReadFile(policyPath)
			if e != nil || !bytes.Equal(after, edited) {
				t.Fatalf("refused CAS changed opt-out: %v", e)
			}
			if boundary == "reservation" {
				if _, e := os.Lstat(IntentPath(b.ControlRoot)); !os.IsNotExist(e) {
					t.Fatalf("intent created: %v", e)
				}
			}
			if boundary != "reservation" {
				after, e = os.ReadFile(IntentPath(b.ControlRoot))
				if e != nil || !bytes.Equal(after, intent) {
					t.Fatalf("refused CAS changed intent: %v", e)
				}
			}
			filename, e := b.Filename()
			if e != nil {
				t.Fatal(e)
			}
			if _, e := os.Lstat(filename); !os.IsNotExist(e) {
				t.Fatalf("locator published: %v", e)
			}
		})
	}
}
