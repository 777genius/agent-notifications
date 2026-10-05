package copilotvscodeinstall

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/copilotvscodeevent"
	"github.com/777genius/agent-notifications/internal/cursorevent"
	"github.com/777genius/agent-notifications/internal/cursorsource"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notification/observation"
	"github.com/777genius/agent-notifications/internal/notifier"
	"github.com/777genius/agent-notifications/internal/webhook"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/directoryidentity"
	processadapter "github.com/777genius/plugin-kit-ai/install/integrationctl/adapters/process"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/profileauthority"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/statev2"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/codex"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/cursor"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/shared"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	uapinstaller "github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/installer"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/providers"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/packagesnapshot"
)

func cursorPackage(t *testing.T, f cursorFixture) {
	t.Helper()
	cursorWrite(t, filepath.Join(f.pkg, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-notify","version":"1.0.0"}`), 0600)
	name, err := f.b.Filename()
	if err != nil {
		t.Fatal(err)
	}
	// Selected Cursor's public native-only staging deliberately bypasses
	// Config.ProjectArgs. The canonical TEST input therefore already carries
	// the reserved binding's portable selector; it is not guessed on delivery.
	mcp, err := json.Marshal(map[string]any{"$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json", "mcpServers": map[string]any{
		"agent-notify": map[string]any{"type": "stdio", "command": "./bin/claude-notifications", "args": []string{"portable-launch", "--locator", name}, "env": map[string]string{}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	cursorWrite(t, filepath.Join(f.pkg, "mcp.json"), mcp, 0600)
	cursorWrite(t, filepath.Join(f.pkg, "bin", "claude-notifications"), []byte("TEST package executable; never executed"), 0700)
	cursorWrite(t, filepath.Join(f.pkg, "skills", "agent-notify", "SKILL.md"), []byte("---\nname: agent-notify\ndescription: TEST fixture\n---\n"), 0600)
}

func cursorPublicInstall(t *testing.T, f cursorFixture, registry *clients.Registry) *uapinstaller.Engine {
	t.Helper()
	cursorPackage(t, f)
	return cursorPublicInstallInput(t, f, registry)
}

func cursorPublicInstallInput(t *testing.T, f cursorFixture, registry *clients.Registry) *uapinstaller.Engine {
	t.Helper()
	cfg := f.cfg
	cfg.HelperExecutable = f.fixed.Executable
	cfg.Runner = processadapter.OS{}
	cfg.Registry, cfg.TrustedLocalPackages, cfg.ServerName = registry, true, "agent-notify"
	name, err := f.b.Filename()
	if err != nil {
		t.Fatal(err)
	}
	cfg.ProjectArgs = func(uapinstaller.BindingFacts) ([]string, error) {
		return []string{"portable-launch", "--locator", name}, nil
	}
	engine, err := uapinstaller.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// This filesystem fixture includes directory verification on Windows under
	// race/coverage. Its setup budget is separate from event admission deadlines.
	installCtx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	p, err := engine.Prepare(installCtx, uapinstaller.Request{Operation: uapinstaller.OpInstall,
		PackageRoot: f.pkg, ClientID: "cursor", ClientConfigRoot: f.b.ScopeRoot,
		ClientExecutable: f.fixed.Executable, InstallationID: f.b.InstallationID, RequiredComponents: []string{"mcp"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	}()
	result, err := engine.Apply(installCtx, p, uapinstaller.Decision{Confirmed: true})
	if err != nil || result.Binding.BindingID != f.b.BindingID || result.Binding.DataRoot != f.b.DataRoot {
		t.Fatalf("public TEST filesystem install: %+v %v", result, err)
	}
	if _, err := portable.Publish(f.b); err != nil {
		t.Fatal(err)
	}
	return engine
}

// Regression: public Capture gives a token on an unsupported ancestor, or
// revalidation accepts a same-path replacement. Expect zero+ErrUnsupported or
// original-token refusal. Shaped selected/vendor facts cannot exercise ioctl
// and the full actual ancestry algorithm; this test supplies no mock token.
func TestCursorActualHostPhysicalBoundary(t *testing.T) {
	f := newCursorFixture(t)
	token, err := profileauthority.Capture(cursorContext(t), f.b.ScopeRoot)
	if errors.Is(err, directoryidentity.ErrUnsupported) {
		if !token.IsZero() {
			t.Fatal("unsupported Capture supplied affirmative token")
		}
		t.Logf("ACTUAL_CAPTURE=UNSUPPORTED; ZERO_PROOF=true; selected_tuple=%s/%s; positive installed filesystem cases NOT_RUN: %v", runtime.GOOS, runtime.GOARCH, err)
		return
	}
	if err != nil || token.IsZero() {
		t.Fatalf("actual Capture: %v", err)
	}
	if err := profileauthority.Revalidate(cursorContext(t), token); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.b.ScopeRoot, f.b.ScopeRoot+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.b.ScopeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := profileauthority.Revalidate(cursorContext(t), token); err == nil {
		t.Fatal("same-path replacement kept original authority")
	}
}

// Regression: a real legacy/prepared Cursor package and source pin are treated
// as selected installed proof. Expect public Inspect/Store to show preparation
// while the event owner denies with no byte changes. Vendor tests exercise
// selection packets; they do not cover admission of a real legacy installation.
func TestCursorPublicPreparedInstallationCannotGrant(t *testing.T) {
	f := newCursorFixture(t)
	registry, err := clients.NewRegistry(cursor.New())
	if err != nil {
		t.Fatal(err)
	}
	engine := cursorPublicInstall(t, f, registry)
	view, err := engine.Inspect(cursorContext(t))
	if err != nil || view.Recovery.Required || len(view.Installations) != 1 {
		t.Fatalf("public prepared state: %+v %v", view, err)
	}
	state, err := (statev2.Store{Path: f.cfg.StateFile}).Load()
	if err != nil || !state.Installations[0].Clients[f.b.BindingID].SelectedDelivery.IsZero() {
		t.Fatalf("real legacy fixture unexpectedly selected: %v", err)
	}
	cursorPolicy(t, f, `{"cursorNotifications":{"desktop":true,"webhook":true}}`)
	before := cursorRead(t, f.cfg.StateFile)
	if _, err := f.g.ConsumerBinding(cursorContext(t)); err == nil {
		t.Fatal("prepared zero physical/selected proof granted")
	}
	if !bytes.Equal(before, cursorRead(t, f.cfg.StateFile)) {
		t.Fatal("event admission changed public state")
	}
}

// Regression: the new reader assumes .mcp.json or accepts a foreign portable
// selector; a source digest is reused as projected ownership. Expect actual
// public Cursor projection to pass its own selector/digest checks, and drift to
// fail, while still granting no physical authority. Existing Claude/Codex
// projected-selector tests never read Cursor's public layout.
func TestCursorPublicProjectionBindsPortableSelector(t *testing.T) {
	f := newCursorFixture(t)
	registry, err := clients.NewRegistry(cursor.New())
	if err != nil {
		t.Fatal(err)
	}
	engine := cursorPublicInstall(t, f, registry)
	state, err := (statev2.Store{Path: f.cfg.StateFile}).Load()
	if err != nil {
		t.Fatal(err)
	}
	i := state.Installations[0]
	c := i.Clients[f.b.BindingID]
	r := cursorRecord{DeclaredName: i.DeclaredName, Binding: c, Data: i.DataReceipts[c.DataReceiptID]}
	if err := verifyCursorProjection(f.b, r); err != nil {
		t.Fatalf("actual public Cursor projection rejected: %v", err)
	}
	var projectedDigest string
	for _, object := range c.NativeObjects {
		if object.Kind == "managed_package_directory" {
			projectedDigest = object.ManagedDigest
		}
	}
	stager := providers.Stager{SnapshotBuilder: packagesnapshot.Builder{TempRoot: f.cfg.TempRoot}}
	canonicalDigest, canonicalErr := engine.LocalPackageTreeDigest(cursorContext(t), f.pkg)
	projectedErr := stager.Verify(cursorContext(t), c.TargetLocator, projectedDigest)
	if projectedDigest == i.Source.TreeDigest || canonicalDigest != i.Source.TreeDigest || canonicalErr != nil || projectedErr != nil {
		t.Fatalf("separate canonical/projected public digests not verified: canonical=%s projected=%s canonicalErr=%v projectedErr=%v", i.Source.TreeDigest, projectedDigest, canonicalErr, projectedErr)
	}
	path := filepath.Join(c.TargetLocator, "mcp.json")
	body := cursorRead(t, path)
	name, err := f.b.Filename()
	if err != nil {
		t.Fatal(err)
	}
	drift := bytes.ReplaceAll(body, []byte(name), []byte("agent-notify-foreign.json"))
	if bytes.Equal(drift, body) {
		t.Fatal("public projection contains no expected selector")
	}
	cursorWrite(t, path, drift, 0600)
	if verifyCursorProjection(f.b, r) == nil || stager.Verify(cursorContext(t), c.TargetLocator, projectedDigest) == nil {
		t.Fatal("foreign selector/projection bytes accepted")
	}
	if !bytes.Equal(drift, cursorRead(t, path)) {
		t.Fatal("projection observation repaired drift")
	}
}

// A qualified filesystem fixture is optional only in the sense that an actual
// unsupported Capture is *negative* evidence. Its caller logs each concrete
// NOT_RUN boundary, never t.Skip, a fake token, or a passing positive assertion.
// No editor/agent/helper/MCP process or native desktop is run on either branch.
func installedCursorFilesystem(t *testing.T, archive ...bool) (cursorFixture, cursorevent.Binding, bool) {
	t.Helper()
	f := newCursorFixture(t)
	token, err := profileauthority.Capture(cursorContext(t), f.b.ScopeRoot)
	if errors.Is(err, directoryidentity.ErrUnsupported) {
		if !token.IsZero() {
			t.Fatal("unsupported host produced physical token")
		}
		t.Logf("NOT_RUN positive public installed filesystem/effect case: actual full-ancestry Capture unsupported; requires accepted Ubuntu24.04 qualification; %v", err)
		return f, cursorevent.Binding{}, false
	}
	if err != nil || token.IsZero() {
		t.Fatalf("physical TEST fixture: %v", err)
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Log("NOT_RUN positive Cursor tuple: actual physical host is outside selected Linux amd64 target")
		return f, cursorevent.Binding{}, false
	}
	osRelease := cursorRead(t, "/etc/os-release")
	if !strings.Contains(string(osRelease), `ID=ubuntu`) || !strings.Contains(string(osRelease), `VERSION_ID="24.04"`) {
		t.Log("NOT_RUN positive Cursor fixture: physical host is outside accepted Ubuntu24.04 qualification")
		return f, cursorevent.Binding{}, false
	}
	t.Logf("ACTUAL_TEST_PHYSICAL_HOST: tuple=%s/%s kernel=%s os=%s ancestry=%+v", runtime.GOOS, runtime.GOARCH,
		cursorRead(t, "/proc/sys/kernel/osrelease"), osRelease, token.Facts())
	producer := f.g.gate.Proof.(*cursorProof)
	registry, err := clients.NewRegistry(producer.adapter, codex.New())
	if err != nil {
		t.Fatal(err)
	}
	cursorPackage(t, f)
	if len(archive) != 0 && archive[0] {
		f.pkg = cursorArchiveInput(t, f.pkg)
	}
	// Expected identity comes from original input and the independent public
	// canonical algorithm, before invoking the tested observation/gate.
	canonical, err := producer.engine.LocalPackageTreeDigest(cursorContext(t), f.pkg)
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(cursorRead(t, filepath.Join(f.pkg, "plugin.json"))))
	engine := cursorPublicInstallInput(t, f, registry)
	producer.cfg.Registry, producer.engine = registry, engine
	cursorPolicy(t, f, `{"cursorNotifications":{"desktop":true,"webhook":true}}`)
	state, err := (statev2.Store{Path: f.cfg.StateFile}).Load()
	if err != nil || !state.Installations[0].Clients[f.b.BindingID].ProfileAuthority.Equal(token) {
		t.Fatal("public engine did not retain actual original authority")
	}
	c := state.Installations[0].Clients[f.b.BindingID]
	facts, selected := c.SelectedDelivery.CursorFacts()
	if !selected || !reflect.DeepEqual(c.PackageRevision, &domain.ClientPackageRevision{
		Version: "1.0.0", TreeDigest: canonical, ManifestDigest: manifestDigest}) ||
		facts.CanonicalDigest != canonical || facts.ProjectionDigest == canonical {
		t.Fatalf("selected revision differs from independently hashed input: %+v %+v", c.PackageRevision, facts)
	}
	if err := cursorStager(f).Verify(cursorContext(t), c.TargetLocator, facts.ProjectionDigest); err != nil {
		t.Fatal(err)
	}
	b, err := f.g.ConsumerBinding(cursorContext(t))
	if err != nil || b.Product != "cursor" || b.Generation == 0 {
		t.Fatalf("public installed TEST proof: %+v %v", b, err)
	}
	if err := profileauthority.Revalidate(cursorContext(t), token); err != nil {
		t.Fatal(err)
	}
	t.Log("P1_ORIGINAL_TOKEN_REVALIDATED=true; PUBLIC_SELECTED_ACK_PROJECTION_DATA_ADMISSION=true; TEST filesystem proof only, native qualification NOT_RUN")
	return f, b, true
}

// Regression: typed conversion drops product/profile/registration/generation,
// or physical/receipt/package drift passes an effect check. Expect zero sink
// submissions and unchanged observed bytes after each deliberate corruption.
// Legacy denial and vendor unit tests miss affirmative installed admission.
func TestCursorInstalledProofRejectsDrift(t *testing.T) {
	for _, change := range []string{"product", "binding", "installation", "profile", "generation", "zero-token", "foreign-token", "receipt", "projection", "installed-executable", "data", "primary", "locator", "profile-replacement", "pending-attempt", "needs-rebind"} {
		t.Run(change, func(t *testing.T) {
			f, b, ready := installedCursorFilesystem(t)
			if !ready {
				return
			}
			store := statev2.Store{Path: f.cfg.StateFile}
			state, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			c := state.Installations[0].Clients[f.b.BindingID]
			originalBinding := b
			switch change {
			case "pending-attempt":
				c.NativeActivationAttempt = "TEST-unresolved"
			case "needs-rebind":
				state.Installations[0].NeedsRebind = true
			case "product":
				b.Product = "copilot-vscode"
			case "binding":
				b.BindingID = "foreign"
			case "installation":
				b.InstallationID = "foreign"
			case "profile":
				b.ProfileIdentity = "foreign"
			case "generation":
				b.Generation++
			case "zero-token":
				c.ProfileAuthority = nil
			case "foreign-token":
				foreign := filepath.Join(filepath.Dir(f.b.ScopeRoot), "TEST-foreign-profile")
				if err := os.Mkdir(foreign, 0700); err != nil {
					t.Fatal(err)
				}
				token, err := profileauthority.Capture(cursorContext(t), foreign)
				if err != nil {
					t.Fatal(err)
				}
				c.ProfileAuthority = &token
			case "receipt":
				for i, object := range c.NativeObjects {
					if object.Kind == "cursor_user_stop" {
						c.NativeObjects = append(c.NativeObjects[:i:i], c.NativeObjects[i+1:]...)
						break
					}
				}
			case "projection":
				cursorWrite(t, filepath.Join(c.TargetLocator, "foreign-added"), []byte("changed projection"), 0600)
			case "installed-executable":
				path := filepath.Join(c.TargetLocator, "bin", "claude-notifications")
				cursorWrite(t, path, []byte("TEST tampered installed executable"), 0700)
				facts, _ := c.SelectedDelivery.CursorFacts()
				if cursorStager(f).Verify(cursorContext(t), c.TargetLocator, facts.ProjectionDigest) == nil {
					t.Fatal("actual Stager accepted installed executable drift")
				}
			case "data":
				cursorWrite(t, filepath.Join(f.b.DataRoot, ".agentplugins-data-owner.json"), []byte(`{}`), 0600)
			case "primary":
				cursorWrite(t, f.fixed.Executable, []byte("changed primary"), 0700)
			case "locator":
				cursorWrite(t, f.fixed.Selector, []byte(`{}`), 0600)
			case "profile-replacement":
				if err := os.Rename(f.b.ScopeRoot, f.b.ScopeRoot+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(f.b.ScopeRoot, 0700); err != nil {
					t.Fatal(err)
				}
			}
			state.Installations[0].Clients[f.b.BindingID] = c
			// Model external persisted drift, including linkage the public
			// Store would reject on Save. Admission must use Store.Load.
			body, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			cursorWrite(t, f.cfg.StateFile, body, 0600)
			before := cursorRead(t, f.cfg.StateFile)
			if _, err := f.g.ConsumerBinding(cursorContext(t)); b == originalBinding && err == nil {
				t.Fatal("installed drift admitted consumer")
			}
			sink := &cursorSink{}
			p := NewCursorDesktop(f.g, b, sink)
			out := p.Deliver(cursorContext(t), notification.Request{Silent: true, Policy: notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true}})
			if sink.calls != 0 || out.Status == "submitted" || !bytes.Equal(before, cursorRead(t, f.cfg.StateFile)) {
				t.Fatalf("drift granted or changed state: %+v calls=%d", out, sink.calls)
			}
			if change == "installed-executable" && string(cursorRead(t, filepath.Join(c.TargetLocator, "bin", "claude-notifications"))) != "TEST tampered installed executable" {
				t.Fatal("delivery repaired installed executable")
			}
		})
	}
}

// Regression: acquisition releases/reacquires the policy lock mid-effect or
// recursively acquires it on completion. Expect competing actual CAS mutation
// to time out while the original lease's completion remains prompt. Generic
// policy lock tests cannot prove the Cursor owner retains exactly this lease.
func TestCursorRetainedLeaseFencesPolicyMutation(t *testing.T) {
	f, b, ready := installedCursorFilesystem(t)
	if !ready {
		return
	}
	s, err := installruntime.ReadPolicySnapshot(cursorContext(t), f.b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.g.gate.acquire(cursorContext(t), localCursorBinding(b), copilotvscodeevent.DesktopChannel)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	key, c, _, err := f.b.Registration()
	if err != nil {
		t.Fatal(err)
	}
	patch, err := CursorPolicyPatch(f.b, CursorChoices{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(cursorContext(t), 150*time.Millisecond)
	defer cancel()
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: f.b.ControlRoot, RuntimeRoot: f.b.RuntimeRoot,
		Owner: f.b.Owner, ConsumerID: key, Consumer: c, RefreshOnly: true, PolicyOnly: true,
		ExpectedGeneration: &s.Installation.Ledger.Generation, ExpectedPolicy: &s.Preimage, PolicyFields: patch})
	if err == nil || ctx.Err() == nil {
		t.Fatalf("cooperative mutation escaped retained lease: %v", err)
	}
	checkCtx, checkCancel := context.WithTimeout(cursorContext(t), time.Second)
	defer checkCancel()
	if err := lease.BeforeHandoff(checkCtx); err != nil {
		t.Fatal(err)
	}
	if err := lease.Complete(checkCtx); err != nil {
		t.Fatal(err)
	}
}

type cursorEffect func(context.Context, notification.Request) notification.Receipt

func (f cursorEffect) Deliver(ctx context.Context, r notification.Request) notification.Receipt {
	return f(ctx, r)
}

// This wrapper can only revoke. Every grant comes from the actual installed
// owner; the hook uses the consumer's public Recheck seam after claim release.
type cursorRevokingGate struct {
	gate   CursorGate
	revoke func()
}

func (g *cursorRevokingGate) Channels(ctx context.Context, b cursorevent.Binding) cursorevent.Channels {
	return g.gate.Channels(ctx, b)
}
func (g *cursorRevokingGate) Recheck(ctx context.Context, b cursorevent.Binding, ch cursorevent.Channel) bool {
	if g.revoke != nil {
		revoke := g.revoke
		g.revoke = nil
		revoke()
	}
	return g.gate.Recheck(ctx, b, ch)
}

// Regression: the fixed JSON sender silently discards a selected preset,
// format or authentication headers. Real installed admission must reject only
// that webhook channel while preserving independent desktop consent.
func TestCursorWebhookConfigurationEligibility(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		webhook      bool
	}{
		{"default-json", "", true},
		{"custom-json", `,"preset":"custom","format":"json"`, true},
		{"slack", `,"preset":"slack"`, false},
		{"text", `,"preset":"custom","format":"text"`, false},
		{"headers", `,"headers":{"Authorization":"TEST-only"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, b, ready := installedCursorFilesystem(t)
			body := `{"schemaVersion":2,"agents":{"cursor":{"notifications":{"desktop":{"enabled":true},"webhook":{"enabled":true,"url":"http://127.0.0.1:18181"` + tc.fields + `}}}}}`
			cursorWrite(t, f.b.GlobalConfig, []byte(body), 0600)
			if _, _, err := readCursorConfig(f.b); err != nil {
				t.Fatal("invalid configuration fixture", err)
			}
			if !ready {
				return
			}
			got := f.g.Channels(cursorContext(t), b)
			if !got.Desktop || got.Webhook != tc.webhook {
				t.Fatalf("channel eligibility: %+v, expected webhook=%t", got, tc.webhook)
			}
			if f.g.Recheck(cursorContext(t), b, cursorevent.WebhookChannel) != tc.webhook {
				t.Fatal("effect recheck disagrees with configured webhook support")
			}
		})
	}
}

// Regression: consent/config revocation after the claim is ignored, or policy
// CAS deadlocks under a leaked claim lock. Expect a retained claim and zero
// submissions through the actual installed owner. Consumer fake-gate tests
// cannot verify this owner's fresh config and registered generation checks.
func TestCursorRevocationAfterClaimSuppresses(t *testing.T) {
	for _, kind := range []string{"consent", "config"} {
		t.Run(kind, func(t *testing.T) {
			f, b, ready := installedCursorFilesystem(t)
			if !ready {
				return
			}
			c, err := f.g.EffectiveConfig(cursorContext(t))
			if err != nil {
				t.Fatal(err)
			}
			clock := notifier.SystemBootClock{}
			cacheRoot := filepath.Join(f.b.DataRoot, "TEST-revocation-claims")
			if err := os.Mkdir(cacheRoot, 0700); err != nil {
				t.Fatal(err)
			}
			gate := &cursorRevokingGate{gate: f.g, revoke: func() {
				if kind == "consent" {
					cursorPolicy(t, f, `{"cursorNotifications":{"desktop":false,"webhook":false}}`)
				} else {
					cursorWrite(t, f.b.GlobalConfig, []byte(`{"schemaVersion":2,"agents":{"cursor":{"statuses":{"agent_stopping":{"enabled":false}}}}}`), 0600)
				}
			}}
			sink := &cursorSink{}
			consumer := cursorevent.Consumer{Binding: b, Gate: gate, Config: c, Clock: clock,
				Cache: &observation.RecentCache{Root: cacheRoot, Clock: clock}, Desktop: NewCursorDesktop(f.g, b, sink)}
			facts, err := cursorsource.Decode(cursorContext(t), cursorsource.Selector, []byte(`{"hook_event_name":"stop","conversation_id":"TEST-revoke","generation_id":"TEST-revoke","status":"aborted"}`))
			if err != nil {
				t.Fatal(err)
			}
			ctx, deadline, cancel, err := observation.Admission(cursorContext(t), clock)
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()
			out := consumer.Consume(ctx, facts, deadline)
			if out.Desktop != "suppressed" || sink.calls != 0 || len(cursorRead(t, filepath.Join(cacheRoot, "observations.json"))) == 0 {
				t.Fatalf("post-claim revoke: %+v calls=%d", out, sink.calls)
			}
		})
	}
}

// Regression: config drift after a possible effect reports success or retries,
// uses stale Consumer.Config, loses the claim, or falls back to webhook. Expect
// unknown, one actual sink call, no POST and retained duplicate claims. Consumer
// tests use fake authority; this version requires actual public installed proof.
func TestCursorCompletionDriftRetainsClaim(t *testing.T) {
	for _, drift := range []string{"config", "profile"} {
		t.Run(drift, func(t *testing.T) {
			f, b, ready := installedCursorFilesystem(t)
			if !ready {
				return
			}
			c, err := f.g.EffectiveConfig(cursorContext(t))
			if err != nil {
				t.Fatal(err)
			}
			original := cursorRead(t, f.b.GlobalConfig)
			clock := notifier.SystemBootClock{}
			cacheRoot := filepath.Join(f.b.DataRoot, "TEST-claims")
			if err := os.Mkdir(cacheRoot, 0700); err != nil {
				t.Fatal(err)
			}
			calls, posts := 0, 0
			sink := cursorEffect(func(context.Context, notification.Request) notification.Receipt {
				calls++
				if drift == "config" {
					cursorWrite(t, f.b.GlobalConfig, []byte(`{"schemaVersion":2,"agents":{"cursor":{"notifications":{"desktop":{"enabled":false},"webhook":{"enabled":false}}}}}`), 0600)
				} else {
					if err := os.Rename(f.b.ScopeRoot, f.b.ScopeRoot+"-original"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(f.b.ScopeRoot, 0700); err != nil {
						t.Fatal(err)
					}
				}
				return notification.Receipt{Status: "submitted"}
			})
			consumer := cursorevent.Consumer{Binding: b, Gate: f.g, Config: c, Clock: clock,
				Cache: &observation.RecentCache{Root: cacheRoot, Clock: clock}, Desktop: NewCursorDesktop(f.g, b, sink),
				SendWebhook: NewCursorWebhookSender(f.g, b, func(context.Context, *config.Config, webhook.SendContext) error { posts++; return nil })}
			facts, err := cursorsource.Decode(cursorContext(t), cursorsource.Selector, []byte(`{"hook_event_name":"stop","conversation_id":"TEST-conversation","generation_id":"TEST-generation","status":"completed"}`))
			if err != nil {
				t.Fatal(err)
			}
			ctx, deadline, cancel, err := observation.Admission(cursorContext(t), clock)
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()
			first := consumer.Consume(ctx, facts, deadline)
			if first.Status != "unknown" || calls != 1 || posts != 0 {
				t.Fatalf("completion drift: %+v calls=%d posts=%d", first, calls, posts)
			}
			if drift == "config" {
				cursorWrite(t, f.b.GlobalConfig, original, 0600)
			} else {
				if err := os.Remove(f.b.ScopeRoot); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(f.b.ScopeRoot+"-original", f.b.ScopeRoot); err != nil {
					t.Fatal(err)
				}
			}
			second := consumer.Consume(ctx, facts, deadline)
			if second.Desktop != "suppressed" || second.Webhook != "suppressed" || calls != 1 || posts != 0 {
				t.Fatalf("claim retried/fell back: %+v calls=%d posts=%d", second, calls, posts)
			}
		})
	}
}

func cursorStager(f cursorFixture) providers.Stager {
	return providers.Stager{SnapshotBuilder: packagesnapshot.Builder{TempRoot: f.cfg.TempRoot}}
}

// This archive is only caller-owned TEST acquisition input. OpenArchive is the
// existing product extractor; no retained canonical source or cache is added.
func cursorArchiveInput(t *testing.T, root string) string {
	t.Helper()
	var body bytes.Buffer
	writer := zip.NewWriter(&body)
	for _, rel := range []string{"plugin.json", "mcp.json", "bin/claude-notifications", "skills/agent-notify/SKILL.md"} {
		info, err := os.Stat(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		header := &zip.FileHeader{Name: rel, Method: zip.Deflate}
		header.SetMode(info.Mode())
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(cursorRead(t, filepath.Join(root, rel))); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(filepath.Dir(root), "TEST-input.zip")
	cursorWrite(t, archive, body.Bytes(), 0600)
	extracted, err := portableasset.OpenArchive(archive, filepath.Join(filepath.Dir(root), "TEST-extraction"), fmt.Sprintf("%x", sha256.Sum256(body.Bytes())))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	return extracted
}

func TestCursorClosedAcquisitionIsIndependent(t *testing.T) {
	for _, archive := range []bool{false, true} {
		t.Run(fmt.Sprintf("archive=%t", archive), func(t *testing.T) {
			f, b, ready := installedCursorFilesystem(t, archive)
			if !ready {
				return
			}
			producer := f.g.gate.Proof.(*cursorProof)
			before, err := producer.record(cursorContext(t), f.b)
			if err != nil {
				t.Fatal(err)
			}
			lease, err := f.g.gate.acquire(cursorContext(t), localCursorBinding(b), copilotvscodeevent.DesktopChannel)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			// cursorPublicInstallInput has already closed the prepared snapshot.
			// Mutating then deleting the only caller input cannot revoke live r1.
			cursorWrite(t, filepath.Join(f.pkg, "foreign-added"), []byte("TEST closed source drift"), 0600)
			if err := lease.BeforeHandoff(cursorContext(t)); err != nil {
				t.Fatalf("closed acquisition mutation revoked selected r1: %v", err)
			}
			if err := os.RemoveAll(f.pkg); err != nil {
				t.Fatal(err)
			}
			if archive {
				if err := os.Remove(filepath.Join(filepath.Dir(filepath.Dir(f.pkg)), "TEST-input.zip")); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := os.Stat(f.pkg); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("caller input remains: %v", err)
			}
			after, err := producer.record(cursorContext(t), f.b)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("cleanup changed selected revision/token/objects/data: %v", err)
			}
			if err := lease.BeforeHandoff(cursorContext(t)); err != nil {
				t.Fatal(err)
			}
			if err := lease.Complete(cursorContext(t)); err != nil {
				t.Fatal(err)
			}
			lease.Release()
			current, err := f.g.ConsumerBinding(cursorContext(t))
			if err != nil || current != b {
				t.Fatalf("input cleanup revoked admission: %+v %v", current, err)
			}
			t.Log("CLOSED_ACQUISITION_POSITIVE=true; canonical/manifest independently hashed before installation; original token/ack/projection/data survive input deletion; TEST filesystem only")
		})
	}
}

// The public adapter drives only this explicit TEST CLI shim. Its listing is
// stateful so pre-install observes absence and post-add observes installation;
// it supplies no Cursor physical token and never runs a real agent.
func cursorCodexShim(t *testing.T, f cursorFixture) (string, string) {
	t.Helper()
	profile := filepath.Join(filepath.Dir(f.pkg), "TEST-codex-profile")
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	marketplace := shared.ManagedMarketplaceName(domain.ComputePhysicalArtifactID("agent-notify", f.b.InstallationID))
	listing := fmt.Sprintf(`{"installed":[{"name":"agent-notify","marketplaceName":%q,"pluginId":%q,"installed":true,"enabled":true}]}`, marketplace, "agent-notify@"+marketplace)
	shim := filepath.Join(filepath.Dir(f.pkg), "TEST-codex-cli")
	cursorWrite(t, shim, []byte(fmt.Sprintf(`#!/bin/sh
case "$*" in
  'plugin list --json')
    if [ -f "$CODEX_HOME/TEST-listed.json" ]; then cat "$CODEX_HOME/TEST-listed.json"; else printf '%%s\n' '{"installed":[]}'; fi ;;
  'plugin add '*)
    printf '%%s\n' '%s' > "$CODEX_HOME/TEST-listed.json"
    source=$(cat "$CODEX_HOME/TEST-source")
    printf '%%s\n' '[marketplaces."%s"]' "source=\"$source\"" 'source_type="local"' '[plugins."agent-notify@%s"]' 'enabled=true' > "$CODEX_HOME/config.toml"
    printf '%%s\n' '{"ok":true}' ;;
  'plugin marketplace add '*) printf '%%s' "$4" > "$CODEX_HOME/TEST-source"; printf '%%s\n' '{"ok":true}' ;;
  'plugin marketplace update '*) printf '%%s\n' '{"ok":true}' ;;
  *) exit 1 ;;
esac
`, listing, marketplace, marketplace)), 0700)
	return profile, shim
}

func cursorApplySibling(t *testing.T, f cursorFixture, operation uapinstaller.Operation, root, profile, executable string) {
	t.Helper()
	engine := f.g.gate.Proof.(*cursorProof).engine
	prepared, err := engine.Prepare(cursorContext(t), uapinstaller.Request{Operation: operation,
		InstallationID: f.b.InstallationID, PackageRoot: root, ClientID: "codex",
		ClientConfigRoot: profile, ClientExecutable: executable, RequiredComponents: []string{"mcp"},
		KnownTargets: []uapinstaller.TargetFacts{{ClientID: "cursor", BindingID: f.b.BindingID,
			ConfigRoot: f.b.ScopeRoot, Executable: f.fixed.Executable}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := prepared.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := engine.Apply(cursorContext(t), prepared, uapinstaller.Decision{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
}

func TestCursorSelectedRevisionSurvivesPublicSiblingUpdate(t *testing.T) {
	f, b, ready := installedCursorFilesystem(t)
	if !ready {
		return
	}
	profile, shim := cursorCodexShim(t, f)
	cursorApplySibling(t, f, uapinstaller.OpInstall, f.pkg, profile, shim)
	producer := f.g.gate.Proof.(*cursorProof)
	store := statev2.Store{Path: f.cfg.StateFile}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	before := state.Installations[0]
	observation, err := producer.record(cursorContext(t), f.b)
	if err != nil {
		t.Fatal(err)
	}
	anBefore, err := installruntime.ReadInstalledSnapshot(f.b.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	configBefore := cursorRead(t, f.b.GlobalConfig)
	hookBefore := cursorRead(t, filepath.Join(f.b.ScopeRoot, "hooks.json"))
	lease, err := f.g.gate.acquire(cursorContext(t), localCursorBinding(b), copilotvscodeevent.DesktopChannel)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	r2 := f
	r2.pkg = filepath.Join(filepath.Dir(f.pkg), "TEST-r2")
	cursorPackage(t, r2)
	cursorWrite(t, filepath.Join(r2.pkg, "plugin.json"), bytes.ReplaceAll(cursorRead(t, filepath.Join(r2.pkg, "plugin.json")), []byte("1.0.0"), []byte("2.0.0")), 0600)
	canonicalR2, err := producer.engine.LocalPackageTreeDigest(cursorContext(t), r2.pkg)
	if err != nil {
		t.Fatal(err)
	}
	cursorApplySibling(t, f, uapinstaller.OpUpdate, r2.pkg, profile, shim)
	state, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	after := state.Installations[0]
	selected := after.Clients[f.b.BindingID]
	if len(after.Clients) != 2 || !reflect.DeepEqual(selected, before.Clients[f.b.BindingID]) ||
		!reflect.DeepEqual(after.DataReceipts[selected.DataReceiptID], before.DataReceipts[selected.DataReceiptID]) ||
		after.Source.TreeDigest != canonicalR2 || after.Source.TreeDigest == before.Source.TreeDigest ||
		after.Package.Version != "2.0.0" || after.Package.ManifestDigest == before.Package.ManifestDigest ||
		after.OperationGroupID == "" || after.OperationGroupID == before.OperationGroupID {
		t.Fatal("public sibling Update did not advance desired r2 while preserving complete Cursor r1")
	}
	for _, c := range after.Clients {
		if c.ClientID == "codex" && (c.PackageRevision == nil || c.PackageRevision.TreeDigest != canonicalR2 || c.PackageRevision.Version != "2.0.0") {
			t.Fatal("Codex did not receive r2")
		}
	}
	view, err := producer.engine.Inspect(cursorContext(t))
	if err != nil || view.Recovery.Required {
		t.Fatalf("sibling update not terminal: %+v %v", view, err)
	}
	for _, c := range view.Installations[0].Bindings {
		if c.BindingID == f.b.BindingID && c.TreeDigest != selected.PackageRevision.TreeDigest {
			t.Fatal("Inspect collapsed selected r1 into desired r2")
		}
	}
	current, err := producer.record(cursorContext(t), f.b)
	if err != nil || !reflect.DeepEqual(observation, current) {
		t.Fatalf("terminal sibling desired revision revoked immutable Cursor r1 (old proof :106): %v", err)
	}
	anAfter, err := installruntime.ReadInstalledSnapshot(f.b.ControlRoot)
	if err != nil || !reflect.DeepEqual(anBefore, anAfter) || !bytes.Equal(configBefore, cursorRead(t, f.b.GlobalConfig)) ||
		!bytes.Equal(hookBefore, cursorRead(t, filepath.Join(f.b.ScopeRoot, "hooks.json"))) {
		t.Fatal("sibling update changed AN registration/generation/config or acknowledged hook")
	}
	if err := lease.BeforeHandoff(cursorContext(t)); err != nil {
		t.Fatal(err)
	}
	if err := lease.Complete(cursorContext(t)); err != nil {
		t.Fatal(err)
	}
	lease.Release()
	currentBinding, err := f.g.ConsumerBinding(cursorContext(t))
	if err != nil || currentBinding != b {
		t.Fatalf("selected r1 admission after sibling r2: %+v %v", currentBinding, err)
	}
	t.Log("SIBLING_R2_SELECTED_R1_POSITIVE=true; full revision/token/objects/data/ANbinding unchanged; retained lease handoff/completion and admission succeeded; TEST filesystem only")
}

func TestCursorSelectedRevisionDriftUnderRetainedLease(t *testing.T) {
	for _, change := range []string{"nil", "incomplete", "missing-tree", "malformed-manifest", "canonical-link", "manifest", "version", "resolved", "catalog", "distribution", "release"} {
		t.Run(change, func(t *testing.T) {
			f, b, ready := installedCursorFilesystem(t)
			if !ready {
				return
			}
			lease, err := f.g.gate.acquire(cursorContext(t), localCursorBinding(b), copilotvscodeevent.DesktopChannel)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			state, err := (statev2.Store{Path: f.cfg.StateFile}).Load()
			if err != nil {
				t.Fatal(err)
			}
			c := state.Installations[0].Clients[f.b.BindingID]
			switch change {
			case "nil":
				c.PackageRevision = nil
			case "incomplete":
				c.PackageRevision.ManifestDigest = ""
			case "missing-tree":
				c.PackageRevision.TreeDigest = ""
			case "malformed-manifest":
				c.PackageRevision.ManifestDigest = "TEST not a digest"
			case "canonical-link":
				c.PackageRevision.TreeDigest = "sha256:" + rawDigest([]byte("TEST different canonical"))
			case "manifest":
				c.PackageRevision.ManifestDigest = "sha256:" + rawDigest([]byte("TEST different manifest"))
			case "version":
				c.PackageRevision.Version = "TEST different version"
			case "resolved":
				c.PackageRevision.ResolvedRevision = "TEST different resolved revision"
			case "catalog":
				c.PackageRevision.CatalogEvidence = &domain.CatalogEvidence{SchemaVersion: 1, CatalogVersion: "TEST changed provenance"}
			case "distribution":
				c.PackageRevision.DistributionID = "TEST different distribution"
			case "release":
				c.PackageRevision.ReleaseSequence++
			}
			state.Installations[0].Clients[f.b.BindingID] = c
			body, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			// External negative corruption, never a fabricated positive baseline.
			cursorWrite(t, f.cfg.StateFile, body, 0600)
			if lease.BeforeHandoff(cursorContext(t)) == nil || lease.Complete(cursorContext(t)) == nil {
				t.Fatal("changed complete selected revision escaped retained lease")
			}
			if !bytes.Equal(body, cursorRead(t, f.cfg.StateFile)) {
				t.Fatal("selected revision observation repaired persisted corruption")
			}
		})
	}
}
