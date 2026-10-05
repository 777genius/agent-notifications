package copilotvscodeinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/copilotvscodeevent"
	"github.com/777genius/agent-notifications/internal/cursorevent"
	"github.com/777genius/agent-notifications/internal/cursorinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	uapinstaller "github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/installer"
)

func cursorContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}

type cursorFixture struct {
	b     portable.Binding
	cfg   uapinstaller.Config
	fixed cursorinstall.Authority
	g     CursorGate
	pkg   string
}

func cursorWrite(t *testing.T, path string, body []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatal(err)
	}
}

func cursorRead(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// All writer effects here belong to explicit TEST fixture construction, never
// the observer. The primary is harmless TEST bytes and is never executed.
func newCursorFixture(t *testing.T) cursorFixture {
	t.Helper()
	base, err := installruntime.CanonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, runtimeRoot, profile := filepath.Join(base, "control"), filepath.Join(base, "runtime"), filepath.Join(base, "TEST-profile")
	for _, dir := range []string{runtimeRoot, profile} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	primary := filepath.Join(runtimeRoot, "primary")
	body := []byte("TEST owned observer bytes; never executed")
	ledger, err := installruntime.Commit(cursorContext(t), installruntime.Request{ControlRoot: root, RuntimeRoot: runtimeRoot,
		Owner: "existing-installer", ConsumerID: "sibling", Consumer: installruntime.Consumer{Registration: "accepted-peer"},
		Files: []installruntime.File{{Path: primary, Data: body, Mode: 0700}}})
	if err != nil {
		t.Fatal(err)
	}
	i := "00000000-0000-4000-8000-000000000181"
	artifact := domain.ComputePhysicalArtifactID("agent-notify", i)
	stateRoot := filepath.Join(base, "uap")
	cfg := uapinstaller.Config{TrustedLocalPackages: true, OpenCodeProbeEnvironment: []string{"PATH="}, StateRoot: stateRoot, StateFile: filepath.Join(stateRoot, "state-v2.json"),
		LockFile: filepath.Join(stateRoot, "mutation.lock"), OperationsDir: filepath.Join(stateRoot, "operations"),
		PluginDataBase: filepath.Join(stateRoot, "plugin-data"), ManagedRoot: filepath.Join(stateRoot, "managed"), TempRoot: filepath.Join(stateRoot, "tmp")}
	b := portable.Binding{Version: 1, Integration: portable.Cursor, InstallationID: i,
		BindingID: domain.ComputeClientBindingID(i, "cursor", "user", filepath.Join(profile, "plugins", "local", artifact)),
		ScopeID:   "user", ScopeRoot: profile, DataRoot: filepath.Join(cfg.PluginDataBase, artifact),
		ControlRoot: root, GlobalConfig: filepath.Join(base, "global.json"), RuntimeRoot: ledger.RuntimeRoot,
		ComponentID: ledger.ID, Owner: ledger.Owner, Primary: "primary"}
	key, consumer, _, err := b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	s, err := installruntime.ReadPolicySnapshot(cursorContext(t), root)
	if err != nil {
		t.Fatal(err)
	}
	patch, err := CursorPolicyPatch(b, CursorChoices{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = installruntime.Commit(cursorContext(t), installruntime.Request{ControlRoot: root, RuntimeRoot: ledger.RuntimeRoot,
		Owner: ledger.Owner, ConsumerID: key, Consumer: consumer,
		ExpectedGeneration: &ledger.Generation, ExpectedPolicy: &s.Preimage, PolicyFields: patch})
	if err != nil {
		t.Fatal(err)
	}
	name, err := b.Filename()
	if err != nil {
		t.Fatal(err)
	}
	fixed := cursorinstall.Authority{ProfileRoot: profile, CursorVersion: "2026.09.28-64d2043",
		QualificationID: "TEST-source-only-not-native-qualification", Executable: primary,
		ExecutableDigest: "sha256:" + rawDigest(body), Selector: filepath.Join(b.DataRoot, name), ObjectID: "TEST-cursor-stop"}
	cursorWrite(t, b.GlobalConfig, []byte(`{"schemaVersion":2,"agents":{"cursor":{"notifications":{"desktop":{"enabled":true},"webhook":{"enabled":true,"url":"http://127.0.0.1:18181"}}}}}`), 0600)
	f := cursorFixture{b: b, cfg: cfg, fixed: fixed, pkg: filepath.Join(base, "canonical")}
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		f.g, err = NewCursorGate(b, cfg, fixed)
		if err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func cursorPolicy(t *testing.T, f cursorFixture, route string) installruntime.PolicySnapshot {
	t.Helper()
	s, err := installruntime.ReadPolicySnapshot(cursorContext(t), f.b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	key, c, _, err := f.b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	_, err = installruntime.Commit(cursorContext(t), installruntime.Request{ControlRoot: f.b.ControlRoot, RuntimeRoot: f.b.RuntimeRoot,
		Owner: f.b.Owner, ConsumerID: key, Consumer: c, RefreshOnly: true, PolicyOnly: true,
		ExpectedGeneration: &s.Installation.Ledger.Generation, ExpectedPolicy: &s.Preimage,
		PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(route)}})
	if err != nil {
		t.Fatal(err)
	}
	s, err = installruntime.ReadPolicySnapshot(cursorContext(t), f.b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Regression: shared enabled/Local/manual consent or a foreign registration
// enables Cursor. Expect only exact Cursor true leaves. Local policy tests do
// not exercise this integration or the new two-leaf desired-patch API.
func TestCursorConsentAndBindingIsolation(t *testing.T) {
	f := newCursorFixture(t)
	s := cursorPolicy(t, f, `{"cursorNotifications":{"desktop":true,"webhook":false,"manual":{"enabled":true},"foreign":{"keep":7}},"copilotVSCodeNotifications":{"desktop":true,"webhook":true},"peer":{"keep":[1,2]}}`)
	c, err := ReadCursorConsent(s, f.b)
	if err != nil || !c.desktop || c.webhook || c.manual {
		t.Fatalf("Cursor consent: %+v %v", c, err)
	}
	if _, err := ReadConsent(s, f.b); err == nil {
		t.Fatal("Local borrowed Cursor registration")
	}
	other := f.b
	other.BindingID = "foreign"
	if _, err := ReadCursorConsent(s, other); err == nil {
		t.Fatal("foreign registration retained consent")
	}
	if _, err := CursorPolicyPatch(other, CursorChoices{}, &c); err == nil {
		t.Fatal("omission borrowed another binding")
	}
	patch, err := CursorPolicyPatch(f.b, CursorChoices{}, &c)
	if err != nil || string(patch["route"]) != `{"cursorNotifications":{"desktop":true,"webhook":false}}` {
		t.Fatalf("retained desired leaves: %s %v", patch["route"], err)
	}
	fresh, err := CursorPolicyPatch(f.b, CursorChoices{}, nil)
	if err != nil || string(fresh["route"]) != `{"cursorNotifications":{"desktop":false,"webhook":false}}` {
		t.Fatalf("fresh binding opt-in: %s %v", fresh["route"], err)
	}
	before := cursorRead(t, filepath.Join(f.b.ControlRoot, "agent-notifications.json"))
	cursorPolicy(t, f, string(fresh["route"]))
	after := cursorRead(t, filepath.Join(f.b.ControlRoot, "agent-notifications.json"))
	var old, current map[string]any
	if json.Unmarshal(before, &old) != nil || json.Unmarshal(after, &current) != nil {
		t.Fatal("policy JSON")
	}
	oldCursor := old["route"].(map[string]any)["cursorNotifications"].(map[string]any)
	oldCursor["desktop"], oldCursor["webhook"] = false, false
	// Generation changes are kernel-owned; compare just the route leaf merger.
	if !reflect.DeepEqual(old["route"], current["route"]) {
		t.Fatal("Cursor desired patch erased foreign nested/sibling leaves")
	}
}

// Regression: nonboolean/null/unknown leaves become opt-in through defaults.
// Expect independent explicit true only; existing parser tests cover typed
// configuration, not raw managed native policy or private retained consent.
func TestCursorConsentRequiresExplicitTrue(t *testing.T) {
	f := newCursorFixture(t)
	s, err := installruntime.ReadPolicySnapshot(cursorContext(t), f.b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{`{}`, `{"cursorNotifications":{"desktop":null,"webhook":"true"}}`, `{"cursorNotifications":{"Desktop":true,"webhook":1}}`, `{"copilotVSCodeNotifications":{"desktop":true,"webhook":true}}`} {
		s.Fields = map[string]json.RawMessage{"route": json.RawMessage(route)}
		c, err := ReadCursorConsent(s, f.b)
		if err != nil || c.desktop || c.webhook {
			t.Fatalf("implicit native consent: %s %+v %v", route, c, err)
		}
	}
	for _, route := range []string{`null`, `[]`, `{"cursorNotifications":null}`, `{"cursorNotifications":true}`} {
		s.Fields = map[string]json.RawMessage{"route": json.RawMessage(route)}
		if _, err := ReadCursorConsent(s, f.b); err == nil {
			t.Fatalf("malformed container admitted: %s", route)
		}
	}
}

// Regression: ambient config or a mutated Config pointer hides Cursor opt-out.
// Expect bound bytes reloaded and independent runtime values; current consumer
// tests inject a Config and cannot detect an installed owner's stale read.
func TestCursorEffectiveConfigReloadsBoundBytes(t *testing.T) {
	f := newCursorFixture(t)
	a, identity, err := readCursorConfig(f.b)
	if err != nil || !a.IsStatusDesktopEnabled("agent_stopping") {
		t.Fatalf("bound effective config: %v", err)
	}
	a.Notifications.Desktop.Enabled = false
	cursorWrite(t, f.b.GlobalConfig, []byte(`{"schemaVersion":2,"agents":{"cursor":{"statuses":{"agent_stopping":{"desktop":{"enabled":false}}}},"copilot-vscode":{"notifications":{"desktop":{"enabled":true}}}}}`), 0600)
	b, next, err := readCursorConfig(f.b)
	if err != nil || b.IsStatusDesktopEnabled("agent_stopping") || identity == next {
		t.Fatalf("opt-out ignored: identity changed=%v %v", identity != next, err)
	}
	if err := os.Remove(f.b.GlobalConfig); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readCursorConfig(f.b); err == nil {
		t.Fatal("missing config used defaults")
	}
}

type cursorSink struct{ calls int }

func (s *cursorSink) Deliver(_ context.Context, r notification.Request) notification.Receipt {
	s.calls++
	return notification.Receipt{CorrelationID: r.CorrelationID, Status: "submitted"}
}

// Regression: a registered binding, true consent, shaped cfg or zero proof
// grants effects. Expect denial and byte preservation. Vendor mechanics tests
// do not pass through this typed consumer/effect owner or its policy lease.
func TestCursorMissingProofHasNoEffects(t *testing.T) {
	f := newCursorFixture(t)
	cursorPolicy(t, f, `{"cursorNotifications":{"desktop":true,"webhook":true}}`)
	before := cursorRead(t, filepath.Join(f.b.ControlRoot, "agent-notifications.json"))
	if b, err := f.g.ConsumerBinding(cursorContext(t)); err == nil || b != (cursorevent.Binding{}) {
		t.Fatalf("unproved binding: %+v %v", b, err)
	}
	if c, err := f.g.EffectiveConfig(cursorContext(t)); err == nil || c != nil {
		t.Fatal("unproved config granted")
	}
	sink := &cursorSink{}
	p := NewCursorDesktop(f.g, cursorevent.Binding{}, sink)
	p.Deliver(cursorContext(t), notification.Request{Navigation: notification.None, Silent: true, Policy: notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true}})
	if sink.calls != 0 || !bytes.Equal(before, cursorRead(t, filepath.Join(f.b.ControlRoot, "agent-notifications.json"))) {
		t.Fatal("unproved effect or policy write")
	}
	local := Gate{Binding: f.b}
	if local.Channels(cursorContext(t), copilotvscodeevent.Binding{}) != (copilotvscodeevent.Channels{}) {
		t.Fatal("missing Local proof granted")
	}
	if (CursorGate{}).Channels(cursorContext(t), cursorevent.Binding{}) != (cursorevent.Channels{}) {
		t.Fatal("zero CursorGate granted")
	}
}
