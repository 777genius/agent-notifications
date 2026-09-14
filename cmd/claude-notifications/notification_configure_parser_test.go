package main

import "testing"

func TestNotificationConfigureParserAndSetupOptIn(t *testing.T) {
	for _, args := range [][]string{{}, {"--provider", "auto"}, {"--provider", "both", "--codex-home", "relative"}, {"--provider", "claude", "--navigation", "none", "--app", "/A.app"}} {
		if _, _, err := parseNotificationConfigure(args); err == nil {
			t.Fatal(args)
		}
	}
	for _, flag := range []string{"--remove", "--print", "--dry-run"} {
		if _, err := parseSetupCodexOptions([]string{"--agent-notify", flag}); err == nil {
			t.Fatal(flag)
		}
	}
	if _, err := parseSetupCodexOptions([]string{"--skip-agent-notify", "--navigation", "none"}); err == nil {
		t.Fatal("route without agent-notify")
	}
	if _, err := parseSetupCodexOptions([]string{"--agent-notify", "--skip-agent-notify"}); err == nil {
		t.Fatal("conflicting opt-in")
	}
	if opts, err := parseSetupCodexOptions(nil); err != nil || !opts.configure || len(opts.configureArgs) != 6 {
		t.Fatal("default agent-notify", opts, err)
	}
	if opts, err := parseSetupCodexOptions([]string{"--json"}); err != nil || !opts.configure || len(opts.configureArgs) != 7 || opts.configureArgs[6] != "--json" {
		t.Fatal("json must keep default none consent", opts, err)
	}
	if opts, err := parseSetupCodexOptions([]string{"--request-permission"}); err != nil || !opts.configure || len(opts.configureArgs) != 7 || opts.configureArgs[6] != "--request-permission" {
		t.Fatal("request-permission must keep default none consent", opts, err)
	}
	if _, err := parseSetupCodexOptions([]string{"--agent-notify", "--navigation", "none"}); err == nil {
		t.Fatal("navigation none without consent")
	}
	if _, err := parseSetupCodexOptions(append([]string{"--agent-notify"}, agentNotifyDefaultNoneArgs()...)); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	opts, err := parseSetupCodexOptions([]string{"--codex-home", home})
	if err != nil || opts.codexHome != home || len(opts.configureArgs) != 6 {
		t.Fatal("codex-home must inject default none consent", opts, err)
	}
	if _, err := parseSetupCodexOptions([]string{"--codex-home", home, "--navigation", "none"}); err == nil {
		t.Fatal("codex-home with navigation none still requires consent")
	}
	if r, _, err := parseNotificationConfigure(append([]string{"--provider", "codex"}, agentNotifyDefaultNoneArgs()...)); err != nil || r.Route == nil || !r.Route.AllowUnknownCaller || r.Route.AllowCallerAsserted {
		t.Fatal("default navigation none requires explicit unknown-caller consent", r, err)
	}
	if _, _, err := parseNotificationConfigure([]string{"--provider", "codex", "--navigation", "none"}); err == nil {
		t.Fatal("parser must not imply unknown-caller consent")
	}
	if r, _, err := parseNotificationConfigure(append([]string{"--provider", "codex", "--codex-home", home}, agentNotifyDefaultNoneArgs()...)); err != nil || r.CodexHome != home {
		t.Fatal("configure must keep --codex-home", r, err)
	}
	if _, err := parseSetupCodexOptions([]string{"--configure-notifications"}); err == nil {
		t.Fatal("retired alias accepted")
	}
	if opts, err := parseSetupCodexOptions([]string{"--print"}); err != nil || opts.configure {
		t.Fatal("print should skip agent-notify", opts, err)
	}
}
