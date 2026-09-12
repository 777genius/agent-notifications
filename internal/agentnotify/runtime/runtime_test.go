//go:build linux || darwin

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
)

type boot struct{}

func (boot) Now() (string, float64, error) { return "boot", 100, nil }

type fakeDelivery struct {
	ready func(notification.Request)
	send  func(notification.Request) string
}

func (f fakeDelivery) CheckReadiness(_ context.Context, r notification.Request) notification.Readiness {
	if f.ready != nil {
		f.ready(r)
	}
	return notification.Readiness{CorrelationID: r.CorrelationID, Status: "ready", Reason: "permission_authorized", Navigation: nav(r)}
}
func nav(r notification.Request) notification.NavigationResult {
	if r.Target.ThreadID != "" {
		return notification.NavigationResult{Capability: "available", Precision: "chat_id", Scope: "local_current_profile"}
	}
	return notification.NavigationResult{Capability: "disabled", Precision: "none"}
}
func (f fakeDelivery) Deliver(_ context.Context, r notification.Request) notification.Receipt {
	s := "submitted"
	if f.send != nil {
		s = f.send(r)
	}
	return notification.Receipt{CorrelationID: r.CorrelationID, Status: s, Reason: "test_outcome", Navigation: nav(r)}
}
func snapshot(route string) installruntime.PolicySnapshot {
	return installruntime.PolicySnapshot{Installation: installruntime.InstalledSnapshot{Enabled: true, Ledger: installruntime.Ledger{ID: route}}, Fields: map[string]json.RawMessage{"schemaVersion": json.RawMessage(`1`), "enabled": json.RawMessage(`true`), "route": json.RawMessage(fmt.Sprintf(`{"localRouting":true,"applicationPath":%q,"teamID":"TEAM"}`, route))}}
}

const global = `{"foreign":{"keep":true},"notifications":{"desktop":{"enabled":true,"sound":true,"clickToFocus":true}}}`

func options(t *testing.T) Options {
	t.Helper()
	// Journal paths are physical no-follow paths; macOS TempDir may use /var.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	return Options{ControlRoot: filepath.Join(root, "control"), JournalRoot: filepath.Join(root, "journal"), GlobalConfig: filepath.Join(root, "global.json"), SpoolRoot: filepath.Join(root, "spool"), BootClock: boot{}, JournalClock: journal.ClockFunc(func() journal.Sample { return journal.Sample{Boot: "boot", Seconds: 100, Available: true} }), ReadSnapshot: func(context.Context, string) (installruntime.PolicySnapshot, error) { return snapshot("/A.app"), nil }, ReadGlobal: func(string) ([]byte, error) { return []byte(global), nil }, DeliveryFactory: func(notifier.ManagedInstallation, string, notifier.BootClock) Delivery { return fakeDelivery{} }}
}
func initialize(t *testing.T, o Options) {
	t.Helper()
	if e := os.Mkdir(o.JournalRoot, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := journal.Initialize(testContext(t), journal.Options{Root: o.JournalRoot, Clock: o.JournalClock}); e != nil {
		t.Fatal(e)
	}
}
func backend(t *testing.T, o Options) *Backend {
	t.Helper()
	b, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func notify(b *Backend, id string) agentnotify.Receipt {
	return b.Notify(context.Background(), agentnotify.Payload{Title: "test", Body: "literal", Category: "info", RequestID: &id, Navigation: notification.Required}, origin.Context{Provider: "codex", Namespace: "tests", SessionID: id, Provenance: origin.ClientMetadata, Locality: origin.Local, Interface: origin.Desktop}, notification.Deadline{BootID: "boot", NotAfter: 110})
}
func TestRequestSnapshotsSharedLimiterReplayAndClose(t *testing.T) {
	o := options(t)
	initialize(t, o)
	var loads atomic.Int32
	o.ReadSnapshot = func(context.Context, string) (installruntime.PolicySnapshot, error) {
		n := loads.Add(1)
		return snapshot(fmt.Sprintf("/%d.app", n)), nil
	}
	entered := make(chan string, 2)
	release := make(chan struct{})
	var mu sync.Mutex
	seen := map[string]string{}
	o.DeliveryFactory = func(m notifier.ManagedInstallation, _ string, _ notifier.BootClock) Delivery {
		return fakeDelivery{ready: func(r notification.Request) { entered <- m.Expected.Ledger.ID; <-release }, send: func(r notification.Request) string {
			mu.Lock()
			defer mu.Unlock()
			seen[m.Expected.Ledger.ID] = r.Target.ApplicationPath
			return "submitted"
		}}
	}
	b := backend(t, o)
	results := make(chan agentnotify.Receipt, 2)
	go func() { results <- notify(b, "A") }()
	go func() { results <- notify(b, "B") }()
	<-entered
	<-entered
	if r := notify(b, "C"); r.Reason != "busy" {
		t.Fatalf("third: %+v", r)
	}
	close(release)
	for range 2 {
		if r := <-results; r.Status != "submitted" {
			t.Fatalf("send: %+v", r)
		}
	}
	if loads.Load() != 2 || len(seen) != 2 {
		t.Fatalf("loads %d seen %v", loads.Load(), seen)
	}
	for k, v := range seen {
		if k != v {
			t.Fatalf("cross-route %q %q", k, v)
		}
	}
	// Replay bypasses the new generation and missing global file entirely.
	b.opts.ReadSnapshot = func(context.Context, string) (installruntime.PolicySnapshot, error) {
		t.Error("replay loaded policy")
		return installruntime.PolicySnapshot{}, errConfig
	}
	b.opts.ReadGlobal = func(string) ([]byte, error) { t.Error("replay read global"); return nil, os.ErrNotExist }
	if r := notify(b, "A"); !r.Replayed || r.Status != "submitted" {
		t.Fatalf("replay: %+v", r)
	}
	if e := b.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := b.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	if r := notify(b, "after"); r.Reason != "service_closed" {
		t.Fatal(r)
	}
}
func TestConfigSnapshotNextRequest(t *testing.T) {
	o := options(t)
	initialize(t, o)
	var disabled atomic.Bool
	var sends atomic.Int32
	o.ReadGlobal = func(string) ([]byte, error) {
		if disabled.Load() {
			return []byte(`{"notifications":{"desktop":{"enabled":false,"sound":false,"clickToFocus":false}}}`), nil
		}
		return []byte(global), nil
	}
	o.DeliveryFactory = func(notifier.ManagedInstallation, string, notifier.BootClock) Delivery {
		return fakeDelivery{ready: func(notification.Request) { disabled.Store(true) }, send: func(r notification.Request) string {
			if !r.Policy.SoundEnabled || !r.Policy.ClickToFocus {
				t.Error("policy changed mid-request")
			}
			sends.Add(1)
			return "submitted"
		}}
	}
	b := backend(t, o)
	if r := notify(b, "first"); r.Status != "submitted" {
		t.Fatal(r)
	}
	if r := notify(b, "next"); r.Reason != "disabled" {
		t.Fatal(r)
	}
	if sends.Load() != 1 {
		t.Fatal(sends.Load())
	}
}
func TestConfigurationAndRates(t *testing.T) {
	for _, tc := range []struct {
		name, data, reason string
		err                error
	}{{"missing", "", "configuration_required", os.ErrNotExist}, {"unreadable", "", "configuration_invalid", os.ErrPermission}, {"malformed", "{", "configuration_invalid", nil}, {"missing_bool", `{"notifications":{"desktop":{"enabled":true,"sound":true}}}`, "configuration_invalid", nil}, {"null", `{"notifications":{"desktop":{"enabled":true,"sound":null,"clickToFocus":true}}}`, "configuration_invalid", nil}, {"duplicate", `{"notifications":{},"notifications":{}}`, "configuration_invalid", nil}, {"disabled", `{"notifications":{"desktop":{"enabled":false,"sound":false,"clickToFocus":false}}}`, "disabled", nil}} {
		t.Run(tc.name, func(t *testing.T) {
			o := options(t)
			initialize(t, o)
			o.ReadGlobal = func(string) ([]byte, error) { return []byte(tc.data), tc.err }
			if r := notify(backend(t, o), "x"); r.Reason != tc.reason {
				t.Fatalf("%+v", r)
			}
		})
	}
	b := backend(t, options(t))
	s := snapshot("/A.app")
	s.Fields["rates"] = json.RawMessage(`{"sessionPerMinute":8}`)
	p, e := b.policy(s)
	if e != nil || p.Rates != (journal.RatePolicy{SessionPerMinute: 8, RuntimePerMinute: 30, Burst: 3}) {
		t.Fatalf("%+v %v", p, e)
	}
	if p.Route.AllowCallerAsserted || p.Route.AllowUnknownCaller {
		t.Fatal("implicit opt-in")
	}
	s.Fields["rates"] = json.RawMessage(`{"burst":0}`)
	if _, e = b.policy(s); e == nil {
		t.Fatal("zero rate accepted")
	}
	s.Fields = map[string]json.RawMessage{}
	b.opts.ReadGlobal = func(string) ([]byte, error) { t.Fatal("disabled global read"); return nil, nil }
	p, e = b.policy(s)
	if e != nil || p.Delivery.ExplicitEnabled {
		t.Fatal(p, e)
	}
}

type fileState struct {
	Mode     os.FileMode
	Size     int64
	Modified time.Time
	Data     string
}

func tree(t *testing.T, root string) map[string]fileState {
	t.Helper()
	m := map[string]fileState{}
	e := filepath.Walk(root, func(p string, i os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		v := fileState{Mode: i.Mode(), Size: i.Size(), Modified: i.ModTime()}
		if i.Mode().IsRegular() {
			d, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			v.Data = string(d)
		}
		m[p] = v
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func TestNoCreateAndReadOnlyStatus(t *testing.T) {
	o := options(t)
	o.ReadSnapshot = nil
	o.ReadGlobal = nil
	root := filepath.Dir(o.ControlRoot)
	before := tree(t, root)
	b := backend(t, o)
	s := b.Status(context.Background())
	if s.Configuration != "disabled" || s.Permission != "not_checked" {
		t.Fatal(s)
	}
	r := notify(b, "absent")
	if r.Reason != "state_repair_required" {
		t.Fatal(r)
	}
	if !reflect.DeepEqual(before, tree(t, root)) {
		t.Fatal("missing state changed")
	}
	initialize(t, o)
	if e := os.WriteFile(filepath.Join(o.JournalRoot, "journal.json"), []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	before = tree(t, root)
	_ = b.Status(context.Background())
	if r := notify(b, "corrupt"); r.Reason != "state_repair_required" {
		t.Fatal(r)
	}
	if !reflect.DeepEqual(before, tree(t, root)) {
		t.Fatal("corrupt state changed")
	}
}
func TestCloseDrainsLookup(t *testing.T) {
	o := options(t)
	initialize(t, o)
	entered := make(chan struct{})
	release := make(chan struct{})
	o.OpenJournal = func(c context.Context, o journal.Options) (*journal.Store, error) {
		close(entered)
		<-release
		return journal.Open(c, o)
	}
	b := backend(t, o)
	done := make(chan struct{})
	go func() { notify(b, "x"); close(done) }()
	<-entered
	c, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if !errors.Is(b.Close(c), context.DeadlineExceeded) {
		t.Fatal("did not drain")
	}
	if r := notify(b, "new"); r.Reason != "service_closed" {
		t.Fatal(r)
	}
	close(release)
	<-done
	if e := b.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
}

// Exercise real kernel locking/generation at readiness and handoff, with the OS
// effect replaced by a counter. Native bundle qualification remains notifier's job.
func TestKernelGenerationChangeBetweenReadinessAndDelivery(t *testing.T) {
	for _, disable := range []bool{true, false} {
		t.Run(fmt.Sprint(disable), func(t *testing.T) {
			o := options(t)
			initialize(t, o)
			ctx := testContext(t)
			req := installruntime.Request{ControlRoot: o.ControlRoot, RuntimeRoot: filepath.Join(filepath.Dir(o.ControlRoot), "bin"), Owner: "existing-installer", ConsumerID: "codex"}
			ledger, e := installruntime.Commit(ctx, req)
			if e != nil {
				t.Fatal(e)
			}
			enabled := true
			req.PolicyEnabled = &enabled
			req.ExpectedGeneration = &ledger.Generation
			ledger, e = installruntime.Commit(ctx, req)
			if e != nil {
				t.Fatal(e)
			}
			// Managed policy retains adapter fields; this test uses navigation none.
			o.ReadSnapshot = nil
			var handoffs atomic.Int32
			o.DeliveryFactory = func(m notifier.ManagedInstallation, _ string, _ notifier.BootClock) Delivery {
				return fakeDelivery{
					ready: func(notification.Request) {
						_, release, e := installruntime.AcquireInstalledLease(ctx, m.ControlRoot, m.Expected)
						if e != nil {
							t.Fatal(e)
						}
						release()
						req.ExpectedGeneration = &ledger.Generation
						if disable {
							enabled = false
						}
						if _, e = installruntime.Commit(ctx, req); e != nil {
							t.Fatal(e)
						}
					},
					send: func(notification.Request) string {
						_, release, e := installruntime.AcquireInstalledLease(ctx, m.ControlRoot, m.Expected)
						if e != nil {
							return "rejected"
						}
						release()
						handoffs.Add(1)
						return "submitted"
					}}
			}
			b := backend(t, o)
			id := "fenced"
			r := b.Notify(ctx, agentnotify.Payload{Title: "test", Category: "info", Navigation: notification.None, RequestID: &id}, origin.Context{Provider: "codex", Namespace: "tests", SessionID: id, Provenance: origin.ClientMetadata, Locality: origin.Local, Interface: origin.Desktop}, notification.Deadline{BootID: "boot", NotAfter: 110})
			if r.Status != "rejected" || handoffs.Load() != 0 {
				t.Fatalf("%+v handoffs %d", r, handoffs.Load())
			}
			before := tree(t, filepath.Dir(o.ControlRoot))
			_ = b.Status(ctx)
			if !reflect.DeepEqual(before, tree(t, filepath.Dir(o.ControlRoot))) {
				t.Fatal("status changed installed state")
			}
		})
	}
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestNamespaceLossAfterReadinessNeverRecreated(t *testing.T) {
	o := options(t)
	initialize(t, o)
	var sent atomic.Int32
	o.DeliveryFactory = func(notifier.ManagedInstallation, string, notifier.BootClock) Delivery {
		return fakeDelivery{ready: func(notification.Request) {
			if e := os.Remove(filepath.Join(o.JournalRoot, "namespace")); e != nil {
				t.Fatal(e)
			}
		}, send: func(notification.Request) string { sent.Add(1); return "submitted" }}
	}
	r := notify(backend(t, o), "lost")
	if r.Reason != "state_repair_required" || sent.Load() != 0 {
		t.Fatal(r, sent.Load())
	}
	if _, e := os.Lstat(filepath.Join(o.JournalRoot, "namespace")); !os.IsNotExist(e) {
		t.Fatal("namespace recreated", e)
	}
}
func TestProductionFactoryAndStrictReader(t *testing.T) {
	o := options(t)
	o.DeliveryFactory = nil
	o.ReadGlobal = nil
	b := backend(t, o)
	m := notifier.ManagedInstallation{ControlRoot: o.ControlRoot, Expected: snapshot("A").Installation}
	d, ok := b.opts.DeliveryFactory(m, o.SpoolRoot, o.BootClock).(*notifier.StructuredDelivery)
	if !ok {
		t.Fatal("not production delivery")
	}
	if !reflect.DeepEqual(d.Installation, m) {
		t.Fatal("installation refreshed")
	}
	if d.Spool.(*notifier.PrivateNativeSpool).Root != o.SpoolRoot {
		t.Fatal("wrong spool")
	}
	if e := os.WriteFile(o.GlobalConfig, []byte(global), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := b.policy(snapshot("A")); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(o.GlobalConfig); e != nil {
		t.Fatal(e)
	}
	if _, e := b.policy(snapshot("A")); !errors.Is(e, agentnotify.ErrConfigurationRequired) {
		t.Fatal(e)
	}
	target := filepath.Join(filepath.Dir(o.GlobalConfig), "foreign")
	if e := os.WriteFile(target, []byte(global), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(target, o.GlobalConfig); e != nil {
		t.Fatal(e)
	}
	if _, e := b.policy(snapshot("A")); !errors.Is(e, errConfig) {
		t.Fatal("symlink accepted", e)
	}
}
func TestSoundFocusAndUnknownCallerOptOut(t *testing.T) {
	o := options(t)
	initialize(t, o)
	o.ReadGlobal = func(string) ([]byte, error) {
		return []byte(`{"notifications":{"desktop":{"enabled":true,"sound":false,"clickToFocus":false}}}`), nil
	}
	var sends atomic.Int32
	o.DeliveryFactory = func(notifier.ManagedInstallation, string, notifier.BootClock) Delivery {
		return fakeDelivery{send: func(r notification.Request) string {
			if !r.Silent || r.Target.ThreadID != "" || r.Policy.ClickToFocus {
				t.Error("opt-out ignored")
			}
			sends.Add(1)
			return "submitted"
		}}
	}
	b := backend(t, o)
	originValue := origin.Context{Provider: "codex", Namespace: "tests", SessionID: "s", Provenance: origin.ClientMetadata, Locality: origin.Local, Interface: origin.Desktop}
	p := agentnotify.Payload{Title: "test", Category: "info", Navigation: notification.BestEffort}
	deadline := notification.Deadline{BootID: "boot", NotAfter: 110}
	if r := b.Notify(context.Background(), p, originValue, deadline); r.Status != "submitted" {
		t.Fatal(r)
	}
	originValue.Locality = origin.LocalityUnknown
	if r := b.Notify(context.Background(), p, originValue, deadline); r.Reason != "local_routing_unavailable" {
		t.Fatal(r)
	}
	originValue.Locality = origin.Local
	originValue.Provenance = origin.CallerAsserted
	if r := b.Notify(context.Background(), p, originValue, deadline); r.Reason != "local_routing_unavailable" {
		t.Fatal(r)
	}
	if sends.Load() != 1 {
		t.Fatal(sends.Load())
	}
}

type unavailableBoot struct{}

func (unavailableBoot) Now() (string, float64, error) { return "", 0, errors.New("unavailable") }
func TestUnavailableClockNoEffects(t *testing.T) {
	o := options(t)
	o.BootClock = unavailableBoot{}
	o.OpenJournal = func(context.Context, journal.Options) (*journal.Store, error) {
		t.Fatal("opened journal")
		return nil, nil
	}
	before := tree(t, filepath.Dir(o.ControlRoot))
	b := backend(t, o)
	if r := notify(b, "x"); r.Reason != "unsupported_platform" {
		t.Fatal(r)
	}
	if s := b.Status(context.Background()); s.OfflineCapability != "unsupported_platform" || s.Permission != "not_checked" {
		t.Fatal(s)
	}
	if !reflect.DeepEqual(before, tree(t, filepath.Dir(o.ControlRoot))) {
		t.Fatal("unsupported platform wrote files")
	}
}
