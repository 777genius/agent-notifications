//go:build linux || darwin

package setup

import (
	"context"
	"encoding/json"
	"fmt"
	transport "github.com/777genius/agent-notifications/internal/agentnotify/mcp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	policyruntime "github.com/777genius/agent-notifications/internal/agentnotify/runtime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
)

func assertNone(t *testing.T, o Options) {
	t.Helper()
	s := snapshot(t, o)
	var route map[string]any
	if e := json.Unmarshal(s.Fields["route"], &route); e != nil {
		t.Fatal(e)
	}
	want := map[string]any{"localRouting": false, "allowUnknownCaller": false, "allowCallerAsserted": false, "applicationPath": "", "teamID": ""}
	if !reflect.DeepEqual(route, want) {
		t.Fatalf("route: %s", s.Fields["route"])
	}
	p, e := policyruntime.ValidateSetupPolicy(s, o.GlobalConfig)
	if e != nil || p.Route != (origin.RoutePolicy{}) {
		t.Fatalf("runtime policy: %+v %v", p, e)
	}
}

func TestNoNavigationPreserveAndSwitch(t *testing.T) {
	for _, appFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "replace-app"}[appFirst], func(t *testing.T) {
			o, r := fixture(t)
			if appFirst {
				r.Route.AllowUnknownCaller = true
				r.Route.AllowCallerAsserted = true
				result, e := Apply(contextFor(t), o, r)
				if e != nil {
					t.Fatal(e)
				}
				r.ExpectedGeneration = result.Generation
			}
			o.VerifyApplication = func(context.Context, Application) error { t.Fatal("none called verifier"); return nil }
			r.Route = &Route{}
			result, e := Apply(contextFor(t), o, r)
			if e != nil || !result.Enabled {
				t.Fatal(result, e)
			}
			assertNone(t, o)
			for _, enabled := range []*bool{nil, ptr(false), nil, ptr(true)} {
				result, e = Apply(contextFor(t), o, Request{ExpectedGeneration: result.Generation, Enabled: enabled})
				if e != nil {
					t.Fatal(e)
				}
				assertNone(t, o)
			}
		})
	}
}

func TestNoNavigationRejectsWithoutMutation(t *testing.T) {
	for _, kind := range []string{"missing", "app", "team", "stale", "canceled", "global", "native", "journal"} {
		t.Run(kind, func(t *testing.T) {
			o, r := fixture(t)
			r.Route = &Route{}
			ctx := contextFor(t)
			switch kind {
			case "missing":
				r.Route = nil
			case "app":
				r.Route.ApplicationPath = "/Applications/Chosen.app"
			case "team":
				r.Route.TeamID = "TEAM123456"
			case "unknown":
				r.Route.AllowUnknownCaller = true
			case "asserted":
				r.Route.AllowCallerAsserted = true
			case "stale":
				r.ExpectedGeneration++
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "global":
				if e := os.Remove(o.GlobalConfig); e != nil {
					t.Fatal(e)
				}
			case "native":
				s := snapshot(t, o)
				if e := os.Rename(s.Installation.Ledger.Native.Path, s.Installation.Ledger.Native.Path+"-hidden"); e != nil {
					t.Fatal(e)
				}
			case "journal":
				result, e := Apply(ctx, o, r)
				if e != nil {
					t.Fatal(e)
				}
				r.ExpectedGeneration = result.Generation
				if e = os.Rename(filepath.Join(o.ControlRoot, "state", "journal"), filepath.Join(o.ControlRoot, "state", "journal-hidden")); e != nil {
					t.Fatal(e)
				}
			}
			before := tree(t, filepath.Dir(o.ControlRoot))
			_, e := Apply(ctx, o, r)
			if e == nil {
				t.Fatal("invalid setup accepted")
			}
			if kind == "missing" {
				wantReason(t, e, "route_required")
			}
			if !reflect.DeepEqual(before, tree(t, filepath.Dir(o.ControlRoot))) {
				t.Fatal("failure mutated state")
			}
		})
	}
}

type noneBoot struct{}

func (noneBoot) Now() (string, float64, error) { return "fixture", 100, nil }

type noneNative struct {
	t       *testing.T
	effects int
}

func (n *noneNative) CheckReadiness(_ context.Context, r notification.Request) notification.Readiness {
	if r.Target != (notification.DesktopTarget{}) || r.Navigation != notification.None {
		n.t.Fatalf("native action not none: %+v", r)
	}
	return notification.Readiness{CorrelationID: r.CorrelationID, Status: "ready", Reason: "ready", Navigation: notification.NavigationResult{Capability: "disabled", Precision: "none", Reason: "no_action"}}
}
func (n *noneNative) Deliver(ctx context.Context, r notification.Request) notification.Receipt {
	n.effects++
	ready := n.CheckReadiness(ctx, r)
	return notification.Receipt{CorrelationID: r.CorrelationID, Status: "submitted", Reason: "os_accepted", Backend: "fake", Navigation: ready.Navigation}
}
func TestNoNavigationPersistedRuntimeService(t *testing.T) {
	o, r := fixture(t)
	r.Route = &Route{AllowUnknownCaller: true}
	o.VerifyApplication = nil
	write(t, o.GlobalConfig, `{"notifications":{"desktop":{"enabled":true,"sound":false,"clickToFocus":true}}}`, 0600)
	if _, e := Apply(contextFor(t), o, r); e != nil {
		t.Fatal(e)
	}
	native := &noneNative{t: t}
	b, e := policyruntime.New(policyruntime.Options{ControlRoot: o.ControlRoot, GlobalConfig: o.GlobalConfig, JournalClock: o.JournalClock, BootClock: noneBoot{}, DeliveryFactory: func(notifier.ManagedInstallation, string, notifier.BootClock) policyruntime.Delivery { return native }})
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close(contextFor(t))
	p := agentnotify.Payload{Title: "Information", Body: "Finished", Category: "info", Navigation: notification.None}
	caller := origin.Context{Provider: "claude", Namespace: "mcp", AnonymousCaller: "shared_mcp", Provenance: origin.ClientMetadata, Locality: origin.LocalityUnknown, Interface: origin.InterfaceUnknown}
	deadline := notification.Deadline{BootID: "fixture", NotAfter: 115}
	receipt := b.Notify(contextFor(t), p, caller, deadline)
	if receipt.Status != "submitted" || receipt.Navigation.Precision != "none" || native.effects != 1 {
		t.Fatalf("none: %+v effects=%d", receipt, native.effects)
	}
	p.Navigation = ""
	receipt = b.Notify(contextFor(t), p, caller, deadline)
	if receipt.Status != "rejected" || receipt.Reason != "session_required" || native.effects != 1 {
		t.Fatalf("required: %+v", receipt)
	}
	write(t, o.GlobalConfig, globalConfig, 0600)
	p.Navigation = notification.None
	receipt = b.Notify(contextFor(t), p, caller, deadline)
	if receipt.Status != "suppressed" || native.effects != 1 {
		t.Fatalf("global opt out: %+v", receipt)
	}
}

func TestNoNavigationMalformedPersistedRoute(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `false`, `{"localRouting":null}`, `{"localRouting":"false"}`, `{"localRouting":false,"applicationPath":"/A.app"}`} {
		t.Run(raw, func(t *testing.T) {
			o, r := fixture(t)
			path := filepath.Join(o.ControlRoot, "agent-notifications.json")
			fields := map[string]json.RawMessage{"schemaVersion": json.RawMessage("1"), "enabled": json.RawMessage("false")}
			fields["route"] = json.RawMessage(raw)
			data, e := json.Marshal(fields)
			if e != nil {
				t.Fatal(e)
			}
			write(t, path, string(data), 0600)
			r.Route = nil
			before := tree(t, filepath.Dir(o.ControlRoot))
			if _, e = Apply(contextFor(t), o, r); e == nil {
				t.Fatal("malformed route accepted")
			}
			if !reflect.DeepEqual(before, tree(t, filepath.Dir(o.ControlRoot))) {
				t.Fatal("malformed route mutated state")
			}
		})
	}
}

// Only the native effect is mocked: setup, runtime policy, journal, service and
// Claude MCP framing all use their production implementations.
func TestClaudeNoNavigationConsumerChain(t *testing.T) {
	for _, consent := range []bool{false, true} {
		t.Run(fmt.Sprint(consent), func(t *testing.T) {
			o, r := fixture(t)
			r.Route = &Route{AllowUnknownCaller: consent}
			o.VerifyApplication = nil
			write(t, o.GlobalConfig, `{"notifications":{"desktop":{"enabled":true,"sound":false,"clickToFocus":true}}}`, 0600)
			if _, e := Apply(contextFor(t), o, r); e != nil {
				t.Fatal(e)
			}
			native := &noneNative{t: t}
			b, e := policyruntime.New(policyruntime.Options{ControlRoot: o.ControlRoot, GlobalConfig: o.GlobalConfig, JournalClock: o.JournalClock, BootClock: noneBoot{}, DeliveryFactory: func(notifier.ManagedInstallation, string, notifier.BootClock) policyruntime.Delivery { return native }})
			if e != nil {
				t.Fatal(e)
			}
			defer b.Close(contextFor(t))
			ctx := contextFor(t)
			a, peer := net.Pipe()
			done := make(chan error, 1)
			go func() {
				done <- transport.Run(ctx, a, transport.Options{Backend: b, Status: noneStatus{}, AdapterKind: "claude", Clock: agentnotify.ClockFunc(func() notification.Deadline { return notification.Deadline{BootID: "fixture", NotAfter: 100} })})
			}()
			client := sdk.NewClient(&sdk.Implementation{Name: "claude-code", Version: "fixture"}, nil)
			session, e := client.Connect(ctx, &sdk.IOTransport{Reader: peer, Writer: peer}, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer func() {
				peer.Close()
				session.Close()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Error("transport did not stop")
				}
			}()
			call := func(id, nav string, meta sdk.Meta) agentnotify.Receipt {
				result, e := session.CallTool(ctx, &sdk.CallToolParams{Name: "notify", Meta: meta, Arguments: map[string]any{"title": "Information", "body": "Finished", "category": "info", "request_id": id, "navigation": nav}})
				if e != nil {
					t.Fatal(e)
				}
				var got agentnotify.Receipt
				if e = json.Unmarshal([]byte(result.Content[0].(*sdk.TextContent).Text), &got); e != nil {
					t.Fatal(e)
				}
				return got
			}
			first := call("same", "none", nil)
			want := "submitted"
			if !consent {
				want = "rejected"
			}
			if first.Status != want || (!consent && first.Reason != "local_routing_unavailable") || (consent && (first.Navigation.Capability != "disabled" || first.Navigation.Precision != "none")) {
				t.Fatalf("first: %+v", first)
			}
			replay := call("same", "none", sdk.Meta{"claudecode": map[string]any{"toolUseId": "toolu_1"}, "toolUseId": "toolu_2", "progressToken": "progress-1"})
			if replay.Status != want || replay.Replayed != consent {
				t.Fatalf("metadata/replay: %+v", replay)
			}
			required := call("required", "required", nil)
			if required.Status != "rejected" || required.Reason != "session_unavailable" {
				t.Fatalf("required: %+v", required)
			}
			if consent {
				if native.effects != 1 {
					t.Fatalf("replay/required effects=%d", native.effects)
				}
				// Different tool metadata must retain the same anonymous rate scope.
				for i := 0; i < 10; i++ {
					call(fmt.Sprint(i), "none", sdk.Meta{"toolUseId": fmt.Sprint(i), "progressToken": i})
				}
				limited := call("limited", "none", nil)
				if limited.Reason != "rate_limited" {
					t.Fatalf("rate: %+v", limited)
				}
				if native.effects > 6 || native.effects < 1 {
					t.Fatalf("effects=%d", native.effects)
				}
			} else if native.effects != 0 {
				t.Fatal("unconsented effect")
			}
		})
	}
}

type noneStatus struct{}

func (noneStatus) Status(context.Context) (transport.Status, error) { return transport.Status{}, nil }

func TestNoneConsentPersistence(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		for _, asserted := range []bool{false, true} {
			o, r := fixture(t)
			first, e := Apply(contextFor(t), o, r)
			if e != nil {
				t.Fatal(e)
			}
			r.ExpectedGeneration = first.Generation
			r.Route = &Route{AllowUnknownCaller: unknown, AllowCallerAsserted: asserted}
			o.VerifyApplication = nil
			result, e := Apply(contextFor(t), o, r)
			if e != nil {
				t.Fatal(e)
			}
			for i := 0; i < 2; i++ {
				s := snapshot(t, o)
				p, e := policyruntime.ValidateSetupPolicy(s, o.GlobalConfig)
				if e != nil || p.Route.LocalRouting || p.Route.ApplicationPath != "" || p.Route.TeamID != "" || p.Route.AllowUnknownCaller != unknown || p.Route.AllowCallerAsserted != asserted {
					t.Fatal(p, e)
				}
				result, e = Apply(contextFor(t), o, Request{ExpectedGeneration: result.Generation})
				if e != nil {
					t.Fatal(e)
				}
			}
		}
	}
}
