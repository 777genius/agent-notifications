package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
)

func TestStatusNavigationEligibility(t *testing.T) {
	base := Status{ExplicitIntent: true, OfflineCapability: "eligible"}
	policy := agentnotify.Policy{Delivery: notification.PolicySnapshot{ExplicitEnabled: true, DesktopEnabled: true, ClickToFocus: true}, Route: origin.RoutePolicy{LocalRouting: true, ApplicationPath: "/private/Codex.app", TeamID: "A1B2C3D4E5"}}
	caller := origin.Context{Provider: "codex", Namespace: "mcp", SessionID: "private-thread", Provenance: origin.ClientMetadata, Locality: origin.Local, Interface: origin.Desktop}
	for _, tc := range []struct {
		name               string
		edit               func(*Status, *agentnotify.Policy, *origin.Context)
		capability, reason string
	}{
		{"configured", nil, "eligible", "configured_codex_desktop"},
		{"disabled", func(s *Status, _ *agentnotify.Policy, _ *origin.Context) { s.ExplicitIntent = false }, "disabled", "notifications_disabled"},
		{"desktop disabled", func(_ *Status, p *agentnotify.Policy, _ *origin.Context) { p.Delivery.DesktopEnabled = false }, "disabled", "desktop_disabled"},
		{"click disabled", func(_ *Status, p *agentnotify.Policy, _ *origin.Context) { p.Delivery.ClickToFocus = false }, "disabled", "click_to_focus_disabled"},
		{"installation unavailable", func(s *Status, _ *agentnotify.Policy, _ *origin.Context) { s.OfflineCapability = "unavailable" }, "unavailable", "installation_unavailable"},
		{"installation disabled", func(_ *Status, p *agentnotify.Policy, _ *origin.Context) { p.Delivery.ExplicitEnabled = false }, "unavailable", "installation_unavailable"},
		{"route disabled", func(_ *Status, p *agentnotify.Policy, _ *origin.Context) { p.Route.LocalRouting = false }, "unavailable", "local_routing_disabled"},
		{"application missing", func(_ *Status, p *agentnotify.Policy, _ *origin.Context) { p.Route.ApplicationPath = "" }, "unavailable", "application_unavailable"},
		{"provider unsupported", func(_ *Status, _ *agentnotify.Policy, o *origin.Context) { o.Provider = "claude" }, "unavailable", "provider_unsupported"},
		{"thread missing", func(_ *Status, _ *agentnotify.Policy, o *origin.Context) { o.SessionID = "" }, "unavailable", "invalid_origin"},
		{"thread dot", func(_ *Status, _ *agentnotify.Policy, o *origin.Context) { o.SessionID = "." }, "unavailable", "invalid_target"},
		{"thread parent", func(_ *Status, _ *agentnotify.Policy, o *origin.Context) { o.SessionID = ".." }, "unavailable", "invalid_target"},
		{"thread line separator", func(_ *Status, _ *agentnotify.Policy, o *origin.Context) { o.SessionID = "thread\u2028id" }, "unavailable", "invalid_target"},
		{"thread paragraph separator", func(_ *Status, _ *agentnotify.Policy, o *origin.Context) { o.SessionID = "thread\u2029id" }, "unavailable", "invalid_target"},
		{"relative app", func(_ *Status, p *agentnotify.Policy, _ *origin.Context) { p.Route.ApplicationPath = "Codex.app" }, "unavailable", "invalid_target"},
		{"unclean app", func(_ *Status, p *agentnotify.Policy, _ *origin.Context) {
			p.Route.ApplicationPath = "/private/../Codex.app"
		}, "unavailable", "invalid_target"},
		{"wrong app suffix", func(_ *Status, p *agentnotify.Policy, _ *origin.Context) { p.Route.ApplicationPath = "/private/Codex" }, "unavailable", "invalid_target"},
		{"app separator", func(_ *Status, p *agentnotify.Policy, _ *origin.Context) {
			p.Route.ApplicationPath = "/private/Codex\u2028.app"
		}, "unavailable", "invalid_target"},
		{"short team", func(_ *Status, p *agentnotify.Policy, _ *origin.Context) { p.Route.TeamID = "ABC" }, "unavailable", "invalid_target"},
		{"lowercase team", func(_ *Status, p *agentnotify.Policy, _ *origin.Context) { p.Route.TeamID = "a1B2C3D4E5" }, "unavailable", "invalid_target"},
		{"punctuation team", func(_ *Status, p *agentnotify.Policy, _ *origin.Context) { p.Route.TeamID = "A1B2C3D4E-" }, "unavailable", "invalid_target"},
		{"unicode and punctuation", func(_ *Status, p *agentnotify.Policy, o *origin.Context) {
			o.SessionID = "thread-_:/.café🐈"
			p.Route.ApplicationPath = "/Applications/Codex café.app"
		}, "eligible", "configured_codex_desktop"},
		{"hidden", func(_ *Status, _ *agentnotify.Policy, o *origin.Context) { o.Hidden = true }, "unavailable", "hidden_target"},
		{"remote", func(_ *Status, _ *agentnotify.Policy, o *origin.Context) { o.Locality = origin.Remote }, "unavailable", "local_gui_unavailable"},
		{"headless", func(_ *Status, _ *agentnotify.Policy, o *origin.Context) { o.Interface = origin.Headless }, "unavailable", "local_gui_unavailable"},
		{"unknown unconsented", func(_ *Status, _ *agentnotify.Policy, o *origin.Context) { o.Interface = origin.InterfaceUnknown }, "unavailable", "unknown_caller"},
		{"unknown consented", func(_ *Status, p *agentnotify.Policy, o *origin.Context) {
			o.Interface = origin.InterfaceUnknown
			o.Locality = origin.LocalityUnknown
			p.Route.AllowUnknownCaller = true
		}, "eligible", "configured_codex_desktop"},
		{"asserted unconsented", func(_ *Status, _ *agentnotify.Policy, o *origin.Context) { o.Provenance = origin.CallerAsserted }, "unavailable", "caller_asserted_disabled"},
		{"asserted consented", func(_ *Status, p *agentnotify.Policy, o *origin.Context) {
			o.Provenance = origin.CallerAsserted
			p.Route.AllowCallerAsserted = true
		}, "eligible", "configured_codex_desktop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, p, o := base, policy, caller
			if tc.edit != nil {
				tc.edit(&s, &p, &o)
			}
			want := agentnotify.NavigationStatus{Capability: tc.capability, Precision: "none", Reason: tc.reason}
			if tc.capability == "eligible" {
				want.Precision, want.Scope = "chat_id", "local_current_profile"
			}
			if got := navigationStatus(s, p, &o, "darwin"); got != want {
				t.Fatalf("navigation: %+v, want %+v", got, want)
			}
		})
	}
	if got := navigationStatus(base, policy, &caller, "linux"); got != (agentnotify.NavigationStatus{Capability: "unavailable", Precision: "none", Reason: "application_unavailable"}) {
		t.Fatalf("Linux without explicit binding: %+v", got)
	}
	for _, platform := range []string{"windows", "freebsd"} {
		if got := navigationStatus(base, policy, &caller, platform); got != (agentnotify.NavigationStatus{Capability: "unavailable", Precision: "none", Reason: "unsupported_platform"}) {
			t.Fatalf("%s navigation: %+v", platform, got)
		}
	}
}

type statusBoot struct{}

func (statusBoot) Now() (string, float64, error) { return "test-status", 100, nil }

func TestContextualStatusReadsOnceWithoutEffectsOrIdentityDisclosure(t *testing.T) {
	root := t.TempDir()
	reads, globals := 0, 0
	b, err := New(Options{
		ControlRoot: filepath.Join(root, "control"), JournalRoot: filepath.Join(root, "journal"), SpoolRoot: filepath.Join(root, "spool"), GlobalConfig: filepath.Join(root, "global"), BootClock: statusBoot{},
		ReadSnapshot: func(context.Context, string) (installruntime.PolicySnapshot, error) {
			reads++
			return installruntime.PolicySnapshot{Installation: installruntime.InstalledSnapshot{Enabled: true, Ledger: installruntime.Ledger{Native: &installruntime.NativeRecord{DecoderFloor: 1}}}, Fields: map[string]json.RawMessage{"schemaVersion": json.RawMessage(`1`), "enabled": json.RawMessage(`true`), "route": json.RawMessage(`{"localRouting":true,"allowUnknownCaller":true,"applicationPath":"/private/Codex.app","teamID":"A1B2C3D4E5"}`)}}, nil
		},
		ReadGlobal: func(string) ([]byte, error) {
			globals++
			return []byte(`{"notifications":{"desktop":{"enabled":true,"sound":true,"clickToFocus":true}}}`), nil
		},
		OpenJournal: func(context.Context, journal.Options) (*journal.Store, error) {
			t.Error("status opened journal")
			return nil, nil
		},
		DeliveryFactory: func(notifier.ManagedInstallation, string, notifier.BootClock) Delivery {
			t.Error("status created native delivery")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	o := origin.Context{Provider: "codex", Namespace: "mcp", SessionID: "private-thread", Provenance: origin.ClientMetadata, Locality: origin.LocalityUnknown, Interface: origin.InterfaceUnknown}
	got := b.StatusForOrigin(context.Background(), o)
	wantNavigation := agentnotify.NavigationStatus{Capability: "eligible", Precision: "chat_id", Scope: "local_current_profile", Reason: "configured_codex_desktop"}
	if runtime.GOOS != "darwin" {
		wantNavigation = agentnotify.NavigationStatus{Capability: "unavailable", Precision: "none", Reason: "unsupported_platform"}
	}
	want := Status{Configuration: "configured", ExplicitIntent: true, DesktopEnabled: true, OfflineCapability: "eligible", Permission: "not_checked", Navigation: wantNavigation}
	raw, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(raw) != string(wantJSON) {
		t.Fatalf("bounded read-only status: %s", raw)
	}
	o.SessionID = ""
	missing := b.StatusForOrigin(context.Background(), o)
	generic := b.Status(context.Background())
	if runtime.GOOS == "darwin" && (missing.Navigation.Reason != "invalid_origin" || generic.Navigation.Reason != "context_unavailable") {
		t.Fatalf("missing context: %+v %+v", missing, generic)
	}
	if reads != 3 || globals != 3 {
		t.Fatalf("policy reads=%d global reads=%d", reads, globals)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("status wrote state: %v %v", entries, err)
	}
}
