package origin

import (
	"github.com/777genius/agent-notifications/internal/notification"
	"testing"
)

func TestRoutingEvidenceAndExplicitPolicy(t *testing.T) {
	base := Context{Provider: "codex", Namespace: "local", SessionID: "exact-session", Provenance: ClientMetadata, Locality: Local, Interface: InterfaceUnknown}
	policy := RoutePolicy{LocalRouting: true, AllowUnknownCaller: true, AllowCallerAsserted: true, ApplicationPath: "/test/Codex.app", TeamID: "team"}
	for _, tt := range []struct {
		name      string
		change    func(*Context, *RoutePolicy)
		available bool
	}{
		{"unknown_optin", func(*Context, *RoutePolicy) {}, true},
		{"unknown_no_optin", func(_ *Context, p *RoutePolicy) { p.AllowUnknownCaller = false }, false},
		{"desktop", func(o *Context, p *RoutePolicy) { o.Interface = Desktop; p.AllowUnknownCaller = false }, true},
		{"remote", func(o *Context, _ *RoutePolicy) { o.Locality = Remote }, false},
		{"headless", func(o *Context, _ *RoutePolicy) { o.Interface = Headless }, false},
		{"hidden", func(o *Context, _ *RoutePolicy) { o.Hidden = true }, false},
		{"asserted", func(o *Context, _ *RoutePolicy) { o.Provenance = CallerAsserted }, true},
		{"asserted_denied", func(o *Context, p *RoutePolicy) { o.Provenance = CallerAsserted; p.AllowCallerAsserted = false }, false},
		{"unknown_locality", func(o *Context, _ *RoutePolicy) { o.Locality = LocalityUnknown }, true},
		{"no_route", func(_ *Context, p *RoutePolicy) { p.LocalRouting = false }, false},
		{"no_session", func(o *Context, _ *RoutePolicy) { o.SessionID = "" }, false},
		{"claude", func(o *Context, _ *RoutePolicy) { o.Provider = "claude" }, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			o, p := base, policy
			tt.change(&o, &p)
			before := o
			r := ResolveCodex(o, p)
			if (r.Navigation.Capability == "available") != tt.available {
				t.Fatal(r)
			}
			if o != before {
				t.Fatal("invented origin")
			}
			if tt.available && (r.Desktop.ThreadID != o.SessionID || r.Navigation.Scope != "local_current_profile") {
				t.Fatal(r)
			}
		})
	}
}
func TestAnonymousAndSourceScope(t *testing.T) {
	o := Context{Provider: "codex", Namespace: "local", AnonymousCaller: "caller-A", Provenance: CallerAsserted, Locality: Local, Interface: InterfaceUnknown}
	if e := o.Validate(notification.None); e != nil {
		t.Fatal(e)
	}
	if e := o.Validate(notification.Required); e == nil {
		t.Fatal("fake chat")
	}
	a, b := o.Scope()
	o.SessionID = o.AnonymousCaller
	c, d := o.Scope()
	if a != c || b == d {
		t.Fatal("scope collision")
	}
	o.Provider = "codex/"
	o.Namespace = "local"
	a, _ = o.Scope()
	o.Provider = "codex"
	o.Namespace = "/local"
	c, _ = o.Scope()
	if a == c {
		t.Fatal("source framing collision")
	}
}
