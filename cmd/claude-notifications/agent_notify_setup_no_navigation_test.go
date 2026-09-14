//go:build linux || darwin

package main

import (
	"context"
	"encoding/json"
	notifysetup "github.com/777genius/agent-notifications/internal/agentnotify/setup"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"reflect"
	"testing"
)

func TestSetupNoNavigationParser(t *testing.T) {
	base := []string{"enable", "--expected-generation", "1"}
	consent := []string{"--allow-unknown-caller", "true", "--allow-caller-asserted", "false"}
	for _, suffix := range [][]string{append([]string{"--navigation", "none"}, consent...), append([]string{"--navigation=none"}, consent...)} {
		a, help, e := parseAgentNotifySetup(append(append([]string{}, base...), suffix...))
		if e != nil || help || a.route == nil || *a.route != (notifysetup.Route{AllowUnknownCaller: true}) {
			t.Fatal(a, help, e)
		}
	}
	a, _, e := parseAgentNotifySetup(base)
	if e != nil || a.route != nil {
		t.Fatal("default changed")
	}
	if _, _, e := parseAgentNotifySetup(append(append([]string{}, base...), "--navigation", "none")); e == nil {
		t.Fatal("navigation none without consent")
	}
	invalid := [][]string{{"--navigation"}, {"--navigation="}, {"--navigation", "required"}, {"--navigation", "NONE"}, {"--navigation", "none", "--navigation=none"}}
	for _, flag := range []string{"app", "team-id", "allow-unknown-caller", "allow-caller-asserted"} {
		value := "false"
		if flag == "app" {
			value = "/A.app"
		}
		if flag == "team-id" {
			value = "TEAM123456"
		}
		invalid = append(invalid, []string{"--navigation", "none", "--" + flag, value})
	}
	invalid = append(invalid, []string{"--navigation", "none", "--app", "/A.app", "--team-id", "TEAM123456", "--allow-unknown-caller", "false", "--allow-caller-asserted", "false"})
	for _, suffix := range invalid {
		if _, _, e := parseAgentNotifySetup(append(append([]string{}, base...), suffix...)); e == nil {
			t.Fatalf("accepted %q", suffix)
		}
	}
	for _, op := range []string{"status", "register", "remove", "disable", "permission-status", "request-permission"} {
		if _, _, e := parseAgentNotifySetup([]string{op, "--navigation", "none", "--expected-generation", "1"}); e == nil {
			t.Fatal(op)
		}
	}
}
func TestSetupNoNavigationCommand(t *testing.T) {
	f := newSetupCommandFixture(t)
	ctx := setupCommandContext(t)
	before := setupCommandTree(t, f.root)
	r := setupCommandRun(t, ctx, f.args(t, "enable", "--global-config", f.global), f.composition, 1)
	if r.Reason != "route_required" || !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
		t.Fatal(r)
	}
	args := f.args(t, "enable", "--global-config", f.global, "--navigation", "none", "--allow-unknown-caller", "true", "--allow-caller-asserted", "false")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	setupCommandRun(t, canceled, args, f.composition, 1)
	if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
		t.Fatal("canceled mutation")
	}
	r = setupCommandRun(t, ctx, args, f.composition, 0)
	if !r.ExplicitIntent || !r.RuntimeEligible || *f.verifies != 0 {
		t.Fatal(r)
	}
	s, e := installruntime.ReadPolicySnapshot(ctx, f.control)
	if e != nil {
		t.Fatal(e)
	}
	var route notifysetup.Route
	if e = json.Unmarshal(s.Fields["route"], &route); e != nil || route != (notifysetup.Route{AllowUnknownCaller: true}) {
		t.Fatal(route, e)
	}
	before = setupCommandTree(t, f.root)
	r = setupCommandRun(t, ctx, args, f.composition, 1)
	if r.Reason != "generation_changed" || !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
		t.Fatal(r)
	}
	r = setupCommandRun(t, ctx, f.args(t, "status", "--global-config", f.global), f.composition, 0)
	if r.Configuration != "configured" || !r.ExplicitIntent || *f.verifies != 0 || !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
		t.Fatal(r)
	}
}

func TestSetupNoneConsentPairs(t *testing.T) {
	for _, unknown := range []string{"true", "false"} {
		for _, asserted := range []string{"true", "false"} {
			a, _, e := parseAgentNotifySetup([]string{"enable", "--expected-generation", "1", "--navigation", "none", "--allow-unknown-caller", unknown, "--allow-caller-asserted", asserted})
			if e != nil || a.route == nil || a.route.LocalRouting || a.route.ApplicationPath != "" || a.route.TeamID != "" || a.route.AllowUnknownCaller != (unknown == "true") || a.route.AllowCallerAsserted != (asserted == "true") {
				t.Fatal(a, e)
			}
		}
	}
}

func TestSetupNoneConsentCommand(t *testing.T) {
	f := newSetupCommandFixture(t)
	ctx := setupCommandContext(t)
	setupCommandRun(t, ctx, f.args(t, "enable", "--global-config", f.global, "--navigation", "none", "--allow-unknown-caller", "true", "--allow-caller-asserted", "false"), f.composition, 0)
	s, e := installruntime.ReadPolicySnapshot(ctx, f.control)
	if e != nil {
		t.Fatal(e)
	}
	var route notifysetup.Route
	if e = json.Unmarshal(s.Fields["route"], &route); e != nil || route != (notifysetup.Route{AllowUnknownCaller: true}) || *f.verifies != 0 {
		t.Fatal(route, e)
	}
}
