//go:build linux || darwin

package setup

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	policyruntime "github.com/777genius/agent-notifications/internal/agentnotify/runtime"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

const globalConfig = `{"foreign":{"keep":true},"notifications":{"desktop":{"enabled":false,"sound":false,"clickToFocus":false}}}`

func ptr[T any](v T) *T { return &v }
func contextFor(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}
func write(t *testing.T, p, data string, mode os.FileMode) {
	t.Helper()
	if e := os.WriteFile(p, []byte(data), mode); e != nil {
		t.Fatal(e)
	}
}
func fixture(t *testing.T) (Options, Request) {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	o := Options{ControlRoot: filepath.Join(root, "control"), RuntimeRoot: filepath.Join(root, "runtime"), Owner: "existing-installer", ConsumerID: "codex", GlobalConfig: filepath.Join(root, "global.json"), Platform: "darwin", JournalClock: journal.ClockFunc(func() journal.Sample { return journal.Sample{Boot: "fixture", Seconds: 100, Available: true} }), VerifyApplication: func(_ context.Context, a Application) error {
		if a.Path != "/Applications/Chosen.app" || a.TeamID != "TEAM123456" {
			return fmt.Errorf("fixture identity mismatch")
		}
		return nil
	}}
	// Inert retained native fixture; no binary, permission or signature process is
	// executed. Floor is an explicit hosted qualification stand-in, not macOS E2E.
	native := filepath.Join(root, "Fixture.app")
	bin := filepath.Join(native, "Contents", "MacOS", "terminal-notifier-modern")
	if e = os.MkdirAll(filepath.Dir(bin), 0700); e != nil {
		t.Fatal(e)
	}
	write(t, bin, "#!/bin/sh\nexit 97\n", 0700)
	change, e := installruntime.StageRetainedNative(contextFor(t), o.ControlRoot, native)
	if e != nil {
		t.Fatal(e)
	}
	change.After.DecoderFloor = 1
	hook := filepath.Join(o.RuntimeRoot, "hook")
	k := installruntime.Request{ControlRoot: o.ControlRoot, RuntimeRoot: o.RuntimeRoot, Owner: o.Owner, ConsumerID: o.ConsumerID, Native: change, Files: []installruntime.File{{Path: hook, Data: []byte("unchanged hook fixture"), Mode: 0700}}}
	l, e := installruntime.Commit(contextFor(t), k)
	if e != nil {
		t.Fatal(e)
	}
	write(t, o.GlobalConfig, globalConfig, 0600)
	return o, Request{ExpectedGeneration: l.Generation, Enabled: ptr(true), Route: &Route{LocalRouting: true, ApplicationPath: "/Applications/Chosen.app", TeamID: "TEAM123456"}}
}
func snapshot(t *testing.T, o Options) installruntime.PolicySnapshot {
	t.Helper()
	s, e := installruntime.ReadPolicySnapshot(contextFor(t), o.ControlRoot)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func read(t *testing.T, p string) []byte {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func wantReason(t *testing.T, e error, reason string) {
	t.Helper()
	var se *Error
	if !errors.As(e, &se) || se.Reason != reason {
		t.Fatalf("wanted %s, got %v", reason, e)
	}
}
func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		st, e := os.Lstat(p)
		if e != nil {
			return e
		}
		v := st.Mode().String()
		if st.Mode().IsRegular() {
			b, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			v += fmt.Sprintf(":%x", sha256.Sum256(b))
		}
		if st.Mode()&os.ModeSymlink != 0 {
			v += "symlink"
		}
		out[p] = v
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func TestEnableRepairDisableRetainsNamespaceAndCounters(t *testing.T) {
	o, r := fixture(t)
	global := read(t, o.GlobalConfig)
	hook := read(t, filepath.Join(o.RuntimeRoot, "hook"))
	result, e := Apply(contextFor(t), o, r)
	if e != nil {
		t.Fatal(e)
	}
	if !result.Enabled || result.Namespace == "" {
		t.Fatal(result)
	}
	s := snapshot(t, o)
	p, e := policyruntime.ValidateSetupPolicy(s, o.GlobalConfig)
	if e != nil {
		t.Fatal(e)
	}
	if p.Delivery.DesktopEnabled || p.Delivery.SoundEnabled || p.Delivery.ClickToFocus {
		t.Fatal("global opt-outs lost")
	}
	if p.Route.AllowUnknownCaller || p.Route.AllowCallerAsserted {
		t.Fatal("implicit caller consent")
	}
	jroot := filepath.Join(o.ControlRoot, "state", "journal")
	store, e := journal.Open(contextFor(t), journal.Options{Root: jroot, Clock: o.JournalClock})
	if e != nil {
		t.Fatal(e)
	}
	a := journal.Admission{Key: journal.Key{Source: "codex/local", Session: "session", Kind: journal.Explicit, Request: "request"}, Digest: sha256.Sum256([]byte("payload")), TrackingID: "tracking"}
	admitted, e := store.Admit(contextFor(t), a)
	if e != nil || !admitted.Fresh {
		t.Fatal(admitted, e)
	}
	before := read(t, filepath.Join(jroot, "journal.json"))
	repair := Request{ExpectedGeneration: result.Generation, Rates: &Rates{Burst: ptr(2)}}
	result, e = Apply(contextFor(t), o, repair)
	if e != nil {
		t.Fatal(e)
	}
	if !result.Enabled || result.Namespace != store.Namespace() {
		t.Fatal("repair lost intent or namespace", result)
	}
	if !reflect.DeepEqual(before, read(t, filepath.Join(jroot, "journal.json"))) {
		t.Fatal("repair rewrote counters/history")
	}
	p, e = policyruntime.ValidateSetupPolicy(snapshot(t, o), o.GlobalConfig)
	if e != nil || p.Rates != (journal.RatePolicy{SessionPerMinute: 6, RuntimePerMinute: 30, Burst: 2}) {
		t.Fatal(p.Rates, e)
	}
	result, e = Apply(contextFor(t), o, Request{ExpectedGeneration: result.Generation, Enabled: ptr(false)})
	if e != nil || result.Enabled {
		t.Fatal(result, e)
	}
	result, e = Apply(contextFor(t), o, Request{ExpectedGeneration: result.Generation})
	if e != nil || result.Enabled {
		t.Fatal("repair auto-enabled", result, e)
	}
	if !reflect.DeepEqual(before, read(t, filepath.Join(jroot, "journal.json"))) || !reflect.DeepEqual(global, read(t, o.GlobalConfig)) || !reflect.DeepEqual(hook, read(t, filepath.Join(o.RuntimeRoot, "hook"))) {
		t.Fatal("unowned inputs/history changed")
	}
}
func TestMissingAndInvalidGlobalNoTouch(t *testing.T) {
	for _, data := range []string{"missing", `{}`, `null`, `{"notifications":{"desktop":{"enabled":true,"sound":true,"clickToFocus":true,"enabled":false}}}`, `{"notifications":{"desktop":{"enabled":true,"sound":null,"clickToFocus":true}}}`} {
		t.Run(data, func(t *testing.T) {
			o, r := fixture(t)
			if data == "missing" {
				if e := os.Remove(o.GlobalConfig); e != nil {
					t.Fatal(e)
				}
			} else {
				write(t, o.GlobalConfig, data, 0600)
			}
			before := tree(t, filepath.Dir(o.ControlRoot))
			_, e := Apply(contextFor(t), o, r)
			reason := "configuration_invalid"
			if data == "missing" {
				reason = "configuration_required"
			}
			wantReason(t, e, reason)
			if !reflect.DeepEqual(before, tree(t, filepath.Dir(o.ControlRoot))) {
				t.Fatal("failed global validation touched inputs")
			}
		})
	}
}
func TestInvalidRatesAndRoutesNoTouch(t *testing.T) {
	for _, name := range []string{"zero", "all-zero", "negative", "over-runtime", "relative", "missing-team", "bad-team", "unverified", "unrouted-app-consent"} {
		t.Run(name, func(t *testing.T) {
			o, r := fixture(t)
			reason := "configuration_invalid"
			switch name {
			case "zero":
				r.Rates = &Rates{Burst: ptr(0)}
			case "all-zero":
				r.Rates = &Rates{Burst: ptr(0), SessionPerMinute: ptr(0), RuntimePerMinute: ptr(0)}
			case "negative":
				r.Rates = &Rates{Burst: ptr(-1)}
			case "over-runtime":
				r.Rates = &Rates{RuntimePerMinute: ptr(2)}
			case "relative":
				r.Route.ApplicationPath = "Chosen.app"
				reason = "invalid_route"
			case "missing-team":
				r.Route.TeamID = ""
				reason = "invalid_route"
			case "bad-team":
				r.Route.TeamID = "bad-team!!"
				reason = "invalid_route"
			case "unverified":
				o.VerifyApplication = nil
				reason = "application_verification_required"
			case "unrouted-app-consent":
				r.Route = &Route{AllowUnknownCaller: true, ApplicationPath: "/Applications/Chosen.app"}
				reason = "invalid_route"
			}
			before := tree(t, filepath.Dir(o.ControlRoot))
			_, e := Apply(contextFor(t), o, r)
			wantReason(t, e, reason)
			if !reflect.DeepEqual(before, tree(t, filepath.Dir(o.ControlRoot))) {
				t.Fatal("invalid setup touched inputs")
			}
		})
	}
}
func TestInitializationCrashRetryAndLostJournal(t *testing.T) {
	for _, phase := range []string{"stage_created", "ownership_started", "before_initialize", "journal_initialized", "ownership_ready", "state_published"} {
		t.Run(phase, func(t *testing.T) {
			o, r := fixture(t)
			o.Fault = func(p string) error {
				if p == phase {
					return fmt.Errorf("inert crash")
				}
				return nil
			}
			result, e := Apply(contextFor(t), o, r)
			wantReason(t, e, "initialization_interrupted")
			s := snapshot(t, o)
			if s.Policy.Enabled || s.Installation.Enabled {
				t.Fatal("failure activated feature")
			}
			o.Fault = nil
			r.ExpectedGeneration = result.Generation
			if phase == "stage_created" || phase == "ownership_started" || phase == "before_initialize" {
				before := tree(t, o.ControlRoot)
				_, e = Apply(contextFor(t), o, r)
				wantReason(t, e, "initialization_recovery_required")
				if !reflect.DeepEqual(before, tree(t, o.ControlRoot)) {
					t.Fatal("incomplete first init silently repaired")
				}
				return
			}
			var owner ownership
			if e = json.Unmarshal(s.Fields["setupState"], &owner); e != nil {
				t.Fatal(e)
			}
			root := filepath.Join(o.ControlRoot, stageName, "journal")
			if phase == "state_published" {
				root = filepath.Join(o.ControlRoot, "state", "journal")
			}
			before := read(t, filepath.Join(root, "journal.json"))
			result, e = Apply(contextFor(t), o, r)
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(before, read(t, filepath.Join(o.ControlRoot, "state", "journal", "journal.json"))) {
				t.Fatal("retry reset journal")
			}
			if owner.Namespace != "" && result.Namespace != owner.Namespace {
				t.Fatal("namespace replaced")
			}
			// Removing the entire expected journal must never create a fresh namespace.
			if e = os.RemoveAll(filepath.Join(o.ControlRoot, "state", "journal")); e != nil {
				t.Fatal(e)
			}
			r.ExpectedGeneration = result.Generation
			beforeTree := tree(t, o.ControlRoot)
			_, e = Apply(contextFor(t), o, r)
			wantReason(t, e, "initialization_recovery_required")
			if !reflect.DeepEqual(beforeTree, tree(t, o.ControlRoot)) {
				t.Fatal("lost journal reinitialized")
			}
		})
	}
}
func TestForeignStateAndSymlinks(t *testing.T) {
	for _, kind := range []string{"directory", "file", "symlink", "stage", "private-mode"} {
		t.Run(kind, func(t *testing.T) {
			o, r := fixture(t)
			state := filepath.Join(o.ControlRoot, "state")
			switch kind {
			case "directory":
				if e := os.Mkdir(state, 0700); e != nil {
					t.Fatal(e)
				}
				write(t, filepath.Join(state, "foreign"), "keep", 0600)
			case "file":
				write(t, state, "foreign", 0600)
			case "symlink":
				if e := os.Symlink(filepath.Dir(o.ControlRoot), state); e != nil {
					t.Fatal(e)
				}
			case "stage":
				if e := os.Mkdir(filepath.Join(o.ControlRoot, stageName), 0700); e != nil {
					t.Fatal(e)
				}
			case "private-mode":
				if e := os.Chmod(o.ControlRoot, 0755); e != nil {
					t.Fatal(e)
				}
			}
			before := tree(t, o.ControlRoot)
			_, e := Apply(contextFor(t), o, r)
			if e == nil {
				t.Fatal("unsafe ownership accepted")
			}
			after := tree(t, o.ControlRoot)
			delete(after, filepath.Join(o.ControlRoot, ".setup.lock"))
			delete(before, filepath.Join(o.ControlRoot, ".setup.lock"))
			if !reflect.DeepEqual(before, after) {
				t.Fatal("foreign state changed")
			}
		})
	}
}
func TestConcurrentEnableDisableAndPolicyCAS(t *testing.T) {
	o, r := fixture(t)
	result, e := Apply(contextFor(t), o, r)
	if e != nil {
		t.Fatal(e)
	}
	generation := result.Generation
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, enabled := range []bool{true, false} {
		wg.Add(1)
		go func(enabled bool) {
			defer wg.Done()
			_, e := Apply(contextFor(t), o, Request{ExpectedGeneration: generation, Enabled: ptr(enabled)})
			results <- e
		}(enabled)
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("CAS winners: %d", success)
	}
	// Simulate a manual edit after setup captured its exact preimage, before
	// the final kernel transaction. No generation change is needed for this CAS.
	s := snapshot(t, o)
	r.ExpectedGeneration = s.Installation.Ledger.Generation
	o.Fault = func(phase string) error {
		if phase == "state_published" {
			path := filepath.Join(o.ControlRoot, "agent-notifications.json")
			var fields map[string]json.RawMessage
			if e := json.Unmarshal(read(t, path), &fields); e != nil {
				return e
			}
			fields["foreign"] = json.RawMessage(`{"keep":"manual"}`)
			b, _ := json.Marshal(fields)
			return os.WriteFile(path, b, 0600)
		}
		return nil
	}
	_, e = Apply(contextFor(t), o, r)
	wantReason(t, e, "policy_commit_failed")
	s = snapshot(t, o)
	if string(s.Fields["foreign"]) != `{"keep":"manual"}` {
		t.Fatal("manual edit overwritten")
	}
}
func TestDisableWithoutNativeGlobalOrJournal(t *testing.T) {
	o, r := fixture(t)
	result, e := Apply(contextFor(t), o, r)
	if e != nil {
		t.Fatal(e)
	}
	s := snapshot(t, o)
	for _, p := range []string{s.Installation.Ledger.Native.Path, o.GlobalConfig, filepath.Join(o.ControlRoot, "state")} {
		if e = os.RemoveAll(p); e != nil {
			t.Fatal(e)
		}
	}
	o.VerifyApplication = nil
	o.Platform = "unsupported"
	result, e = Apply(contextFor(t), o, Request{ExpectedGeneration: result.Generation, Enabled: ptr(false)})
	if e != nil || result.Enabled {
		t.Fatal(result, e)
	}
	policy, e := installruntime.ReadUserPolicy(o.ControlRoot)
	if e != nil || policy.Enabled {
		t.Fatal(policy, e)
	}
}

func TestGlobalConfigWriterAndSetupShareCAS(t *testing.T) {
	o, r := fixture(t)
	result, e := Apply(contextFor(t), o, r)
	if e != nil {
		t.Fatal(e)
	}
	r.ExpectedGeneration = result.Generation
	atCommit := make(chan struct{})
	continueSetup := make(chan struct{})
	done := make(chan error, 1)
	o.Fault = func(phase string) error {
		if phase == "state_published" {
			close(atCommit)
			select {
			case <-continueSetup:
				return nil
			case <-time.After(5 * time.Second):
				return fmt.Errorf("test barrier timeout")
			}
		}
		return nil
	}
	go func() { _, e := Apply(contextFor(t), o, r); done <- e }()
	select {
	case <-atCommit:
	case <-time.After(5 * time.Second):
		t.Fatal("setup did not reach barrier")
	}
	id, e := installruntime.Fingerprint(o.GlobalConfig)
	if e != nil {
		t.Fatal(e)
	}
	changed := `{"foreign":{"keep":"writer"},"notifications":{"desktop":{"enabled":false,"sound":true,"clickToFocus":false}}}`
	k := o.kernel(result.Generation)
	k.PolicyOnly = false // this fixture is a generic managed config writer
	k.ConfigPaths = []string{o.GlobalConfig}
	k.Files = []installruntime.File{{Path: o.GlobalConfig, Before: id, Data: []byte(changed), Mode: 0600}}
	l, e := installruntime.Commit(contextFor(t), k)
	if e != nil {
		t.Fatal(e)
	}
	close(continueSetup)
	if e = <-done; e == nil {
		t.Fatal("setup ignored config transaction generation")
	}
	if string(read(t, o.GlobalConfig)) != changed {
		t.Fatal("global managed edit overwritten")
	}
	if snapshot(t, o).Installation.Ledger.Generation != l.Generation {
		t.Fatal("stale setup advanced generation")
	}
}
func TestGlobalDisappearsBeforeActivation(t *testing.T) {
	o, r := fixture(t)
	o.Fault = func(phase string) error {
		if phase == "state_published" {
			return os.Remove(o.GlobalConfig)
		}
		return nil
	}
	_, e := Apply(contextFor(t), o, r)
	wantReason(t, e, "configuration_required")
	s := snapshot(t, o)
	if s.Policy.Enabled || s.Installation.Enabled {
		t.Fatal("missing global activated feature")
	}
	if _, e = os.Stat(o.GlobalConfig); !os.IsNotExist(e) {
		t.Fatal("global defaults created")
	}
}
func TestStateLostBeforeActivation(t *testing.T) {
	o, r := fixture(t)
	o.Fault = func(phase string) error {
		if phase == "state_published" {
			return os.RemoveAll(filepath.Join(o.ControlRoot, "state", "journal"))
		}
		return nil
	}
	_, e := Apply(contextFor(t), o, r)
	wantReason(t, e, "initialization_recovery_required")
	s := snapshot(t, o)
	if s.Policy.Enabled || s.Installation.Enabled {
		t.Fatal("lost state activated feature")
	}
}
func TestSetupGenerationAndForeignPolicy(t *testing.T) {
	o, r := fixture(t)
	path := filepath.Join(o.ControlRoot, "agent-notifications.json")
	write(t, path, `{"schemaVersion":1,"enabled":false,"foreign":{"keep":true},"route":{"future":"keep"},"rates":{"future":42}}`, 0600)
	before := tree(t, filepath.Dir(o.ControlRoot))
	stale := r
	stale.ExpectedGeneration++
	_, e := Apply(contextFor(t), o, stale)
	wantReason(t, e, "generation_changed")
	if !reflect.DeepEqual(before, tree(t, filepath.Dir(o.ControlRoot))) {
		t.Fatal("generation mismatch touched files")
	}
	result, e := Apply(contextFor(t), o, r)
	if e != nil {
		t.Fatal(e)
	}
	s := snapshot(t, o)
	if !s.Policy.Enabled || !s.Installation.Enabled || s.Installation.Ledger.Generation != result.Generation {
		t.Fatal("incoherent enabled generation")
	}
	var route, rates map[string]any
	_ = json.Unmarshal(s.Fields["route"], &route)
	_ = json.Unmarshal(s.Fields["rates"], &rates)
	if route["future"] != "keep" || rates["future"] != float64(42) || string(s.Fields["foreign"]) == "" {
		t.Fatal("foreign policy fields removed")
	}
	if _, e = Apply(contextFor(t), o, r); e == nil {
		t.Fatal("stale initial request retried")
	}
}
func TestReadyOwnershipRejectsReplacedNamespaceAndUnsafeLock(t *testing.T) {
	for _, kind := range []string{"namespace", "symlink", "mode", "hardlink", "owner", "missing-lock", "missing-state"} {
		t.Run(kind, func(t *testing.T) {
			o, r := fixture(t)
			result, e := Apply(contextFor(t), o, r)
			if e != nil {
				t.Fatal(e)
			}
			r.ExpectedGeneration = result.Generation
			state := filepath.Join(o.ControlRoot, "state")
			jroot := filepath.Join(state, "journal")
			lock := filepath.Join(state, "native-spool", ".spool.lock")
			switch kind {
			case "namespace":
				if e = os.RemoveAll(jroot); e != nil {
					t.Fatal(e)
				}
				if e = os.Mkdir(jroot, 0700); e != nil {
					t.Fatal(e)
				}
				if _, e = journal.Initialize(contextFor(t), journal.Options{Root: jroot, Clock: o.JournalClock}); e != nil {
					t.Fatal(e)
				}
			case "symlink":
				if e = os.Remove(lock); e != nil {
					t.Fatal(e)
				}
				if e = os.Symlink(o.GlobalConfig, lock); e != nil {
					t.Fatal(e)
				}
			case "mode":
				if e = os.Chmod(lock, 0644); e != nil {
					t.Fatal(e)
				}
			case "hardlink":
				if e = os.Link(lock, filepath.Join(state, "foreign-link")); e != nil {
					t.Fatal(e)
				}
			case "owner":
				if os.Geteuid() != 0 {
					t.Skip("changing UID requires root; type/mode/link checks still execute")
				}
				if e = os.Chown(lock, 65534, -1); e != nil {
					t.Fatal(e)
				}
			case "missing-lock":
				if e = os.Remove(lock); e != nil {
					t.Fatal(e)
				}
			case "missing-state":
				if e = os.RemoveAll(state); e != nil {
					t.Fatal(e)
				}
			}
			before := tree(t, o.ControlRoot)
			_, e = Apply(contextFor(t), o, r)
			wantReason(t, e, "initialization_recovery_required")
			if !reflect.DeepEqual(before, tree(t, o.ControlRoot)) {
				t.Fatal("unsafe expected state modified")
			}
		})
	}
}
func TestMissingGlobalForDisabledRepairNoTouch(t *testing.T) {
	o, r := fixture(t)
	if e := os.Remove(o.GlobalConfig); e != nil {
		t.Fatal(e)
	}
	r.Enabled = nil
	r.Route = nil
	before := tree(t, filepath.Dir(o.ControlRoot))
	_, e := Apply(contextFor(t), o, r)
	wantReason(t, e, "configuration_required")
	if !reflect.DeepEqual(before, tree(t, filepath.Dir(o.ControlRoot))) {
		t.Fatal("repair created missing global defaults/locks")
	}
}
func TestEnableRequiresChosenRoute(t *testing.T) {
	o, r := fixture(t)
	r.Route = nil
	_, e := Apply(contextFor(t), o, r)
	wantReason(t, e, "route_required")
}

func TestSetupNeverRecoversPendingHookMutation(t *testing.T) {
	o, r := fixture(t)
	hook := filepath.Join(o.RuntimeRoot, "hook")
	before := read(t, hook)
	id, e := installruntime.Fingerprint(hook)
	if e != nil {
		t.Fatal(e)
	}
	k := o.kernel(r.ExpectedGeneration)
	k.PolicyOnly = false
	k.Files = []installruntime.File{{Path: hook, Before: id, Data: []byte("pending hook change"), Mode: 0700}}
	k.Fault = func(phase string) error {
		if phase == "transaction" {
			return fmt.Errorf("crash before hook promotion")
		}
		return nil
	}
	if _, e = installruntime.Commit(contextFor(t), k); e == nil {
		t.Fatal("missing pending transaction")
	}
	for _, enabled := range []bool{true, false} {
		r.Enabled = ptr(enabled)
		r.Route = nil
		_, e = Apply(contextFor(t), o, r)
		wantReason(t, e, "recovery_required")
		if !reflect.DeepEqual(before, read(t, hook)) {
			t.Fatal("setup recovered unrelated hook changes")
		}
		if _, e = os.Stat(filepath.Join(o.ControlRoot, "transaction.json")); e != nil {
			t.Fatal("setup removed installer recovery evidence")
		}
	}
}
func TestSetupDisableNeverRecreatesLostKernelLocks(t *testing.T) {
	for _, name := range []string{".component-install.lock", "agent-notifications.json.lock"} {
		t.Run(name, func(t *testing.T) {
			o, r := fixture(t)
			if e := os.Remove(filepath.Join(o.ControlRoot, name)); e != nil {
				t.Fatal(e)
			}
			before := tree(t, o.ControlRoot)
			r.Enabled = ptr(false)
			r.Route = nil
			if _, e := Apply(contextFor(t), o, r); e == nil {
				t.Fatal("lost kernel lock accepted")
			}
			if !reflect.DeepEqual(before, tree(t, o.ControlRoot)) {
				t.Fatal("lost kernel lock recreated")
			}
		})
	}
}

func TestInterruptedInitializationCannotActivateThroughMetadataRepair(t *testing.T) {
	o, r := fixture(t)
	o.Fault = func(phase string) error {
		if phase == "journal_initialized" {
			return fmt.Errorf("crash")
		}
		return nil
	}
	result, e := Apply(contextFor(t), o, r)
	wantReason(t, e, "initialization_interrupted")
	path := filepath.Join(o.ControlRoot, "agent-notifications.json")
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(read(t, path), &fields); e != nil {
		t.Fatal(e)
	}
	fields["enabled"] = json.RawMessage(`true`)
	data, _ := json.Marshal(fields)
	write(t, path, string(data), 0600)
	o.Fault = nil
	r.ExpectedGeneration = result.Generation
	_, e = Apply(contextFor(t), o, r)
	wantReason(t, e, "initialization_recovery_required")
	s := snapshot(t, o)
	if s.Installation.Ledger.Enabled {
		t.Fatal("ownership metadata repair activated an unpublished journal")
	}
	// Explicit disable resolves inconsistent intent without resetting history.
	disabled, e := Apply(contextFor(t), o, Request{ExpectedGeneration: result.Generation, Enabled: ptr(false)})
	if e != nil {
		t.Fatal(e)
	}
	r.ExpectedGeneration = disabled.Generation
	if _, e = Apply(contextFor(t), o, r); e != nil {
		t.Fatal(e)
	}
}
