package cursorinstall

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/atomicfile"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/directoryidentity"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/pathpolicy"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/cursorhooks"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/installer"
)

const foreignDocument = `{"version":1,"foreign":{"keep":1e+02,"text":"\u0061"},"hooks":{"stop":[{"command":"foreign-stop","custom":false}],"other":[{"opaque":null}]}}`

func fixture(t *testing.T) (*Adapter, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(root, "TEST-Cursor-profile")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "TEST-observer")
	body := []byte("TEST fixed executable bytes; never executed\n")
	write(t, executable, body, 0700)
	fixed := Authority{ProfileRoot: profile, CursorVersion: cursorVersion, QualificationID: "TEST-vendor-mechanics-only",
		Executable: executable, ExecutableDigest: fmt.Sprintf("sha256:%x", sha256.Sum256(body)),
		Selector: filepath.Join(root, "TEST-binding.json"), ObjectID: "TEST-owned-Cursor-Stop"}
	a, err := New(nativeconfig.New(), pathpolicy.Policy{}, &fixed)
	if err != nil {
		t.Fatal(err)
	}
	return a, filepath.Join(profile, "hooks.json")
}

func write(t *testing.T, path string, body []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func packet(t *testing.T, a *Adapter, objects []domain.NativeObjectOwnership) domain.SelectedDelivery {
	t.Helper()
	selected, err := a.plan("sha256:"+strings.Repeat("1", 64), objects)
	if err != nil {
		t.Fatal(err)
	}
	selected, err = selected.WithProjectionDigest("sha256:" + strings.Repeat("2", 64))
	if err != nil {
		t.Fatal(err)
	}
	return selected
}

// Independent native grammar expectation, without using the planner/renderer.
func expectedEntry(t *testing.T, a *Adapter) string {
	t.Helper()
	command := fmt.Sprintf("'%s' 'cursor-event' 'stop' '--binding' '%s'", a.fixed.Executable, a.fixed.Selector)
	encoded, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`{"type":"command","command":%s,"timeout":5,"failClosed":false}`, encoded)
}

// Regression: identical unowned entries were treated as registration authority.
func TestPlanRefusesUnownedIdenticalCommandBeforePacketGrant(t *testing.T) {
	a, path := fixture(t)
	body := []byte(`{"version":1,"hooks":{"stop":[` + expectedEntry(t, a) + `]}}`)
	write(t, path, body, 0600)
	selected, err := a.plan("sha256:"+strings.Repeat("1", 64), nil)
	if !errors.Is(err, cursorhooks.ErrConflict) || !selected.IsZero() {
		t.Fatalf("unowned collision granted packet: %v, %v", selected, err)
	}
	if !bytes.Equal(body, read(t, path)) {
		t.Fatal("planner changed the unowned config")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("read-only refusal created effects: %v, %v", entries, err)
	}
}

// Regression: zero-length present files and malformed foreign shape became absence.
func TestPlanRefusesPresentEmptyAndMalformedForeignConfig(t *testing.T) {
	for _, body := range []string{"", " \n", `{"version":1,"hooks":{"other":{}}}`, `{"version":1,"foreign":1,"foreign":2}`} {
		t.Run(fmt.Sprintf("bytes-%d-%x", len(body), sha256.Sum256([]byte(body))), func(t *testing.T) {
			a, path := fixture(t)
			write(t, path, []byte(body), 0600)
			selected, err := a.plan("sha256:"+strings.Repeat("1", 64), nil)
			if err == nil || !selected.IsZero() || string(read(t, path)) != body {
				t.Fatalf("malformed current config granted/mutated: %v", err)
			}
		})
	}
}

// Regression: an exact raw/existence grant was silently refreshed at effect time.
func TestFrozenOriginalRejectsRawAndExistenceDrift(t *testing.T) {
	for _, change := range []string{"absent-to-empty", "present-to-absent", "raw-only", "planned-remainder"} {
		t.Run(change, func(t *testing.T) {
			a, path := fixture(t)
			if change != "absent-to-empty" {
				write(t, path, []byte(foreignDocument), 0600)
			}
			selected := packet(t, a, nil)
			var expected []byte
			switch change {
			case "absent-to-empty":
				expected = []byte{}
				write(t, path, expected, 0600)
			case "present-to-absent":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "raw-only":
				expected = []byte(foreignDocument + "\n")
				write(t, path, expected, 0600)
			case "planned-remainder":
				expected = []byte(foreignDocument)
				f, _ := selected.CursorFacts()
				f.PlannedReceipt.RemainderDigest = "sha256:" + strings.Repeat("3", 64)
				var err error
				selected, err = domain.NewCursorDelivery(f)
				if err != nil {
					t.Fatal(err)
				}
			}
			objects, effect, err := a.applyRegistration(t.Context(), a.native, selected, nil)
			if err == nil || len(objects) != 0 || effect != domain.NativeEffectUnchanged {
				t.Fatalf("drift acknowledged: objects=%v effect=%s err=%v", objects, effect, err)
			}
			if change == "present-to-absent" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("absent config resent: %v", err)
				}
			} else if !bytes.Equal(expected, read(t, path)) {
				t.Fatal("effect changed drifted bytes")
			}
		})
	}
}

// Regression: a planned object was acknowledged without complete owned readback.
func TestRegistrationActualReceiptForeignPreservationAndRemoval(t *testing.T) {
	a, path := fixture(t)
	write(t, path, []byte(foreignDocument), 0600)
	selected := packet(t, a, nil)
	f, _ := selected.CursorFacts()
	objects, effect, err := a.applyRegistration(t.Context(), a.native, selected, nil)
	if err != nil || effect != domain.NativeEffectCommitted || len(objects) != 1 {
		t.Fatalf("registration: objects=%v effect=%s err=%v", objects, effect, err)
	}
	if objects[0].CursorReceipt != f.PlannedReceipt || objects[0].Path != path || objects[0].Kind != "cursor_user_stop" {
		t.Fatalf("acknowledged receipt differs from actual successful plan: %+v", objects[0])
	}
	body := read(t, path)
	for _, literal := range []string{`"keep":1e+02`, `"text":"\u0061"`, `{"command":"foreign-stop","custom":false}`, `"other":[{"opaque":null}]`} {
		if !bytes.Contains(body, []byte(literal)) {
			t.Fatalf("foreign lexeme lost: %s in %s", literal, body)
		}
	}
	var actual struct {
		Hooks struct{ Stop []json.RawMessage } `json:"hooks"`
	}
	if err := json.Unmarshal(body, &actual); err != nil || len(actual.Hooks.Stop) != 2 {
		t.Fatalf("actual stop entries: %s, %v", body, err)
	}
	var got, want any
	if err := json.Unmarshal(actual.Hooks.Stop[1], &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(expectedEntry(t, a)), &want); err != nil {
		t.Fatal(err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil || !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("complete independent owned entry mismatch: %s, %v", gotJSON, err)
	}
	if err := a.remove(t.Context(), a.native, path, pureReceipt(objects[0].CursorReceipt)); err != nil {
		t.Fatal(err)
	}
	removed := read(t, path)
	if bytes.Contains(removed, []byte("cursor-event")) || !bytes.Contains(removed, []byte(`{"command":"foreign-stop","custom":false}`)) {
		t.Fatalf("removal lost foreign or retained owned entry: %s", removed)
	}
	intent := domain.PendingNativeIntent{AttemptID: "TEST-remove", Direction: domain.NativeIntentRemove,
		Delivery: selected, RemoveOwnedEntry: true, PreviousCursorObject: objects[0]}
	if recovered, err := a.reconcile(intent); err != nil || len(recovered) != 0 {
		t.Fatalf("recorded removal absence: %v, %v", recovered, err)
	}
	if !bytes.Equal(removed, read(t, path)) {
		t.Fatal("recovery wrote a removal effect")
	}
}

// Regression: fresh current remainder replaced the original acknowledged basis.
func TestAbsentRepairAndRemovalRetainOriginalAcknowledgedRemainder(t *testing.T) {
	a, path := fixture(t)
	write(t, path, []byte(foreignDocument), 0600)
	selected := packet(t, a, nil)
	objects, _, err := a.applyRegistration(t.Context(), a.native, selected, nil)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(read(t, path), []byte(`1e+02`), []byte(`2e+02`), 1)
	write(t, path, changed, 0600)
	newPlan := packet(t, a, objects) // Legitimate foreign edit while owned entry exists.
	f, _ := newPlan.CursorFacts()
	if f.PlannedReceipt.RemainderDigest == objects[0].CursorReceipt.RemainderDigest {
		t.Fatal("fixture failed to change the foreign remainder")
	}
	if err := a.remove(t.Context(), a.native, path, pureReceipt(objects[0].CursorReceipt)); err != nil {
		t.Fatal(err)
	}
	absent := read(t, path)
	intent := domain.PendingNativeIntent{AttemptID: "TEST-original-predecessor", Direction: domain.NativeIntentRemove,
		Delivery: newPlan, RemoveOwnedEntry: true, PreviousCursorObject: objects[0]}
	if _, err := a.reconcile(intent); !errors.Is(err, cursorhooks.ErrAbsenceUnproven) {
		t.Fatalf("recovery replaced original remainder with proposed one: %v", err)
	}
	if _, err := a.plan(f.CanonicalDigest, objects); !errors.Is(err, cursorhooks.ErrAbsenceUnproven) {
		t.Fatalf("repair recaptured absent-entry authority: %v", err)
	}
	if !bytes.Equal(absent, read(t, path)) {
		t.Fatal("unproved absence was repaired")
	}
}

// Regression: recovery resent missing registration or minted a current receipt.
func TestRecordedRecoveryObservesWithoutResendOrRecapture(t *testing.T) {
	a, path := fixture(t)
	write(t, path, []byte(foreignDocument), 0600)
	selected := packet(t, a, nil)
	intent := domain.PendingNativeIntent{AttemptID: "TEST-register", Direction: domain.NativeIntentRegister, Delivery: selected}
	if _, err := a.reconcile(intent); !errors.Is(err, cursorhooks.ErrAbsenceUnproven) {
		t.Fatalf("missing effect was acknowledged: %v", err)
	}
	if string(read(t, path)) != foreignDocument {
		t.Fatal("missing effect was resent")
	}
	objects, _, err := a.applyRegistration(t.Context(), a.native, selected, nil)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(read(t, path), []byte(`1e+02`), []byte(`2e+02`), 1)
	write(t, path, changed, 0600)
	fixed := a.fixed
	fresh, err := New(nativeconfig.New(), pathpolicy.Policy{}, &fixed)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := fresh.reconcile(intent)
	if err != nil || len(actual) != 1 || actual[0] != objects[0] {
		t.Fatalf("recorded receipt was recaptured: %v, %v", actual, err)
	}
	if !bytes.Equal(changed, read(t, path)) {
		t.Fatal("read-only recovery changed current config")
	}
	drift := bytes.Replace(changed, []byte(`"timeout":5`), []byte(`"timeout":6`), 1)
	if bytes.Equal(drift, changed) {
		t.Fatal("fixture failed to drift the owned entry")
	}
	write(t, path, drift, 0600)
	if _, err := fresh.reconcile(intent); err == nil {
		t.Fatal("owned full-entry drift was acknowledged")
	}
	if !bytes.Equal(drift, read(t, path)) {
		t.Fatal("foreign owned-entry drift was overwritten")
	}
}

// This negative FileIO seam writes real files and simulates a foreign writer
// inside the mutation boundary. It supplies no profile-authority affirmation.
type foreignWrite struct{ path string }

func (f foreignWrite) ReadNoFollow(path string) ([]byte, os.FileMode, bool, error) {
	snapshot, err := nativeconfig.New().ReadExactFile(path)
	return snapshot.Body, snapshot.Mode, snapshot.Exists, err
}
func (f foreignWrite) WriteAtomic(path string, body []byte, mode os.FileMode) error {
	if path == f.path {
		body = bytes.Replace(body, []byte(`"timeout":5`), []byte(`"timeout":6`), 1)
	}
	return atomicfile.Write(path, body, mode)
}
func (f foreignWrite) RemoveNoFollow(path string) error { return os.Remove(path) }

func TestForeignWriteReadbackCannotAcknowledgeOrRestoreOverDrift(t *testing.T) {
	a, path := fixture(t)
	write(t, path, []byte(foreignDocument), 0600)
	selected := packet(t, a, nil)
	objects, effect, err := a.applyRegistration(t.Context(), nativeconfig.NewWithFileIO(foreignWrite{path}), selected, nil)
	if err == nil || len(objects) != 0 || effect != domain.NativeEffectUncertain {
		t.Fatalf("foreign readback acknowledged: %v, %s, %v", objects, effect, err)
	}
	if !bytes.Contains(read(t, path), []byte(`"timeout":6`)) {
		t.Fatal("rollback overwrote foreign drift")
	}
}

// Regression: selected callbacks admitted nil/zero authority, or fixed facts
// were silently redirected to a different tuple/root.
func TestPublicBoundariesDenyMissingAndUnqualifiedAuthority(t *testing.T) {
	a, path := fixture(t)
	selected := packet(t, a, nil)
	f, _ := selected.CursorFacts()
	client := domain.DetectedClient{ClientID: domain.ClientCursor, ConfigRoot: a.fixed.ProfileRoot}
	plan := domain.DeliveryPlan{ClientID: domain.ClientCursor, Scope: domain.ScopeUser, NativeRegistryRoot: a.fixed.ProfileRoot, SelectedDelivery: selected}
	if err := a.RefinePlan(t.Context(), clients.PlanInput{Client: client}, &plan); err == nil {
		t.Fatal("missing authority granted a selected plan")
	}
	if err := a.PreflightActivation(clients.Env{NativeConfig: a.native}, domain.ActivationRequest{Client: client, Plan: plan}); err == nil {
		t.Fatal("missing authority passed preflight")
	}
	if out, err := a.Activate(t.Context(), clients.Env{NativeConfig: a.native}, domain.ActivationRequest{Client: client, Plan: plan}); err == nil || len(out.NativeObjects) != 0 {
		t.Fatalf("missing authority acknowledged activation: %v, %v", out, err)
	}
	if out, err := a.ReconcileNativeIntent(t.Context(), domain.PendingNativeIntent{AttemptID: "TEST", Direction: domain.NativeIntentRegister, Delivery: selected}); err == nil || len(out.NativeObjects) != 0 {
		t.Fatalf("missing recorded authority acknowledged recovery: %v, %v", out, err)
	}
	if err := a.RevalidateProfileAuthority(t.Context(), domain.ClientCursor, domain.ProfileAuthority{}); err == nil {
		t.Fatal("zero token revalidated")
	}
	if _, err := New(a.native, a.paths, nil); err == nil {
		t.Fatal("nil fixed authority was admitted")
	}
	fixed := a.fixed
	fixed.CursorVersion = "unqualified"
	if _, err := New(a.native, a.paths, &fixed); err == nil {
		t.Fatal("unqualified tuple was admitted")
	}
	if _, err := a.ResolveProfileRoot(filepath.Dir(f.ProfileRoot)); err == nil {
		t.Fatal("another original canonical root admitted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("missing-authority refusal created config: %v", err)
	}
}

// A genuine public Capture observation: unsupported sandbox identity must be
// an error and zero grant, never a fabricated token or a skipped positive test.
func TestPhysicalCaptureObservation(t *testing.T) {
	a, _ := fixture(t)
	client := domain.DetectedClient{ClientID: domain.ClientCursor, ConfigRoot: a.fixed.ProfileRoot}
	token, err := a.CaptureProfileAuthority(t.Context(), client)
	if err != nil {
		if !errors.Is(err, directoryidentity.ErrUnsupported) || !token.IsZero() {
			t.Fatalf("unexpected capture failure or nonzero grant: %v", err)
		}
		t.Logf("PHYSICAL_CAPTURE_UNAVAILABLE: %v; positive installed proof remains NOT_RUN", err)
		return
	}
	if token.IsZero() || token.Facts().CanonicalRoot != a.fixed.ProfileRoot {
		t.Fatal("capture did not establish the original root")
	}
	if err := a.RevalidateProfileAuthority(t.Context(), domain.ClientCursor, token); err != nil {
		t.Fatal(err)
	}
	t.Log("PHYSICAL_CAPTURE_AVAILABLE; this source test makes no installed/native claim")
}

// Closest public Engine/Store negative admission boundary. No runner, real
// native agent, mocked affirmative authority, or crash/recovery duplication.
func TestEnginePrepareRefusesDifferentProfileBeforeStateEffects(t *testing.T) {
	a, _ := fixture(t)
	root := filepath.Dir(a.fixed.ProfileRoot)
	other := filepath.Join(root, "TEST-other-profile")
	if err := os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	registry, err := clients.NewRegistry(a)
	if err != nil {
		t.Fatal(err)
	}
	stateRoot := filepath.Join(root, "TEST-state")
	engine, err := installer.New(installer.Config{StateRoot: stateRoot, Registry: registry, TrustedLocalPackages: true})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := engine.Prepare(context.Background(), installer.Request{Operation: installer.OpInstall,
		PackageRoot: root, ClientID: "cursor", ClientConfigRoot: other, ClientExecutable: a.fixed.Executable})
	if prepared != nil {
		if closeErr := prepared.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	if err == nil {
		t.Fatal("public Engine admitted another physical owner")
	}
	if _, err := os.Stat(stateRoot); !os.IsNotExist(err) {
		t.Fatalf("refusal created state/lock/journal: %v", err)
	}
	state, err := (statev2.Store{Path: filepath.Join(stateRoot, "state-v2.json")}).Load()
	if err != nil || len(state.Installations) != 0 {
		t.Fatalf("fresh Store contains effects: %+v, %v", state, err)
	}
}
