//go:build linux || darwin

package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// A ready config is not proof that enable can safely open the private journal.
// Regressions for #585 must be read-only: even failed inspection cannot rewrite
// the user's old policy, journal or ownership metadata.
func TestInspectChecksInitializationAdmissionBeforeEnable(t *testing.T) {
	t.Run("fresh disabled installation is safe to initialize", func(t *testing.T) {
		o, r := fixture(t)
		before := tree(t, o.ControlRoot)
		if err := Inspect(contextFor(t), o, r, []byte(globalConfig)); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, tree(t, o.ControlRoot)) {
			t.Fatal("read-only preflight mutated fresh installation")
		}
	})
	t.Run("legacy enabled intent without owner refuses", func(t *testing.T) {
		o, r := fixture(t)
		s := snapshot(t, o)
		on := true
		g := s.Installation.Ledger.Generation
		if _, err := installruntime.Commit(contextFor(t), installruntime.Request{
			ControlRoot: o.ControlRoot, RuntimeRoot: o.RuntimeRoot,
			Owner: o.Owner, ConsumerID: o.ConsumerID,
			RefreshOnly: true, PolicyOnly: true,
			ExpectedGeneration: &g, PolicyEnabled: &on,
		}); err != nil {
			t.Fatal(err)
		}
		r.ExpectedGeneration = snapshot(t, o).Installation.Ledger.Generation
		before := tree(t, o.ControlRoot)
		wantReason(t, Inspect(contextFor(t), o, r, []byte(globalConfig)), "initialization_recovery_required")
		if !reflect.DeepEqual(before, tree(t, o.ControlRoot)) {
			t.Fatal("read-only preflight changed legacy enabled installation")
		}
	})
	t.Run("orphan state without owner refuses", func(t *testing.T) {
		o, r := fixture(t)
		if err := os.Mkdir(filepath.Join(o.ControlRoot, "state"), 0700); err != nil {
			t.Fatal(err)
		}
		before := tree(t, o.ControlRoot)
		wantReason(t, Inspect(contextFor(t), o, r, []byte(globalConfig)), "initialization_recovery_required")
		if !reflect.DeepEqual(before, tree(t, o.ControlRoot)) {
			t.Fatal("read-only preflight adopted orphan state")
		}
	})
	t.Run("interrupted setup is not a ready preflight", func(t *testing.T) {
		o, r := fixture(t)
		o.Fault = func(phase string) error {
			if phase == "ownership_started" {
				return fmt.Errorf("test interrupted")
			}
			return nil
		}
		result, err := Apply(contextFor(t), o, r)
		wantReason(t, err, "initialization_interrupted")
		o.Fault = nil
		r.ExpectedGeneration = result.Generation
		before := tree(t, o.ControlRoot)
		wantReason(t, Inspect(contextFor(t), o, r, []byte(globalConfig)), "initialization_recovery_required")
		if !reflect.DeepEqual(before, tree(t, o.ControlRoot)) {
			t.Fatal("read-only preflight replayed incomplete provisioning")
		}
	})
	for _, phase := range []string{"journal_initialized", "ownership_ready"} {
		t.Run("fully staged journal can resume at "+phase, func(t *testing.T) {
			o, r := fixture(t)
			o.Fault = func(step string) error {
				if step == phase {
					return fmt.Errorf("test interrupted")
				}
				return nil
			}
			result, err := Apply(contextFor(t), o, r)
			wantReason(t, err, "initialization_interrupted")
			o.Fault = nil
			r.ExpectedGeneration = result.Generation
			before := tree(t, o.ControlRoot)
			if err := Inspect(contextFor(t), o, r, []byte(globalConfig)); err != nil {
				t.Fatalf("replayable staged state rejected: %v", err)
			}
			if !reflect.DeepEqual(before, tree(t, o.ControlRoot)) {
				t.Fatal("preflight itself resumed the journal")
			}
			if _, err := Apply(contextFor(t), o, r); err != nil {
				t.Fatalf("legitimate interrupted setup could not resume: %v", err)
			}
		})
	}
	t.Run("replaced interrupted stage cannot borrow durable owner identity", func(t *testing.T) {
		o, r := fixture(t)
		o.Fault = func(step string) error {
			if step == "journal_initialized" {
				return fmt.Errorf("test interruption")
			}
			return nil
		}
		result, err := Apply(contextFor(t), o, r)
		wantReason(t, err, "initialization_interrupted")
		o.Fault = nil
		r.ExpectedGeneration = result.Generation
		stage := filepath.Join(o.ControlRoot, stageName)
		if err := os.Rename(stage, stage+"-retired"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(stage, 0700); err != nil {
			t.Fatal(err)
		}
		before := tree(t, o.ControlRoot)
		wantReason(t, Inspect(contextFor(t), o, r, []byte(globalConfig)), "initialization_recovery_required")
		if !reflect.DeepEqual(before, tree(t, o.ControlRoot)) {
			t.Fatal("preflight altered foreign stage on ownership mismatch")
		}
	})
	t.Run("healthy existing initialization remains ready", func(t *testing.T) {
		o, r := fixture(t)
		result, err := Apply(contextFor(t), o, r)
		if err != nil {
			t.Fatal(err)
		}
		r.ExpectedGeneration = result.Generation
		before := tree(t, o.ControlRoot)
		if err := Inspect(contextFor(t), o, r, []byte(globalConfig)); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, tree(t, o.ControlRoot)) {
			t.Fatal("read-only preflight modified healthy existing state")
		}
	})
	t.Run("missing saved journal must not be called ready", func(t *testing.T) {
		o, r := fixture(t)
		result, err := Apply(contextFor(t), o, r)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(filepath.Join(o.ControlRoot, "state", "journal")); err != nil {
			t.Fatal(err)
		}
		r.ExpectedGeneration = result.Generation
		before := tree(t, o.ControlRoot)
		wantReason(t, Inspect(contextFor(t), o, r, []byte(globalConfig)), "initialization_recovery_required")
		if !reflect.DeepEqual(before, tree(t, o.ControlRoot)) {
			t.Fatal("read-only preflight reset missing journal")
		}
	})
}
