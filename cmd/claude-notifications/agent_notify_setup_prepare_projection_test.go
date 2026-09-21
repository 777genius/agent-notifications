//go:build linux || darwin

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func setupPrepareIsolation(t *testing.T) {
	root := setupCommandRoot(t)
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "CODEX_HOME", "TMPDIR"} {
		t.Setenv(key, root)
	}
}

func TestSetupPrepareComposition(t *testing.T) {
	for _, kind := range []string{"fresh", "defaults", "partial", "false", "noop", "malformed", "stale"} {
		t.Run(kind, func(t *testing.T) {
			setupPrepareIsolation(t)
			f := newSetupCommandFixture(t)
			ctx := setupCommandContext(t)
			legacy, defaults := filepath.Join(f.root, "legacy.json"), filepath.Join(f.root, "defaults.json")
			setupCommandWrite(t, defaults, `{}`, 0600)
			setupCommandWrite(t, legacy, `{"secret":{"raw":1e+03},"notifications":{"desktop":{"enabled":false}}}`, 0600)
			switch kind {
			case "defaults":
				if err := os.Remove(legacy); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(f.global); err != nil {
					t.Fatal(err)
				}
			case "fresh":
				f.global = filepath.Join(f.root, "new", "nested", "config.json")
				f.composition.globalConfigPath = func() (string, error) { return f.global, nil }
			case "partial":
				setupCommandWrite(t, f.global, `{"secret":{"raw":1e+03}}`, 0600)
			case "malformed":
				setupCommandWrite(t, f.global, `{"secret":`, 0600)
			}
			args := f.args(t, "prepare", "--global-config", f.global, "--legacy-config", legacy, "--defaults-config", defaults)
			if kind == "stale" {
				args[5] = "999"
			}
			before := setupCommandTree(t, f.root)
			control := setupCommandTree(t, f.control)
			generation := f.generation(t)
			want := 0
			if kind == "malformed" || kind == "stale" {
				want = 1
			}
			r := setupCommandRun(t, ctx, args, f.composition, want)
			if !reflect.DeepEqual(control, setupCommandTree(t, f.control)) || generation != f.generation(t) || *f.verifies != 0 {
				t.Fatal("prepare changed installation or verified app")
			}
			if want == 1 {
				after := setupCommandTree(t, f.root)
				if kind == "malformed" {
					delete(after, f.global+".lock")
				}
				if !reflect.DeepEqual(before, after) {
					t.Fatal("failed preparation mutated tree")
				}
				return
			}
			if !r.Ready || r.ExplicitIntent || r.RuntimeEligible || r.Permission != "not_checked" {
				t.Fatal(r)
			}
			raw := setupCommandRead(t, f.global)
			if (kind == "fresh" || kind == "partial") && !strings.Contains(raw, "1e+03") {
				t.Fatal("unknown raw lost", raw)
			}
			if kind != "partial" && kind != "defaults" && !strings.Contains(raw, `"enabled":false`) && !strings.Contains(raw, `"enabled": false`) {
				t.Fatal("false lost", raw)
			}
			before = setupCommandTree(t, f.root)
			r = setupCommandRun(t, ctx, args, f.composition, 0)
			if r.Changed || r.Source != "canonical" || !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
				t.Fatal("not a no-op", r)
			}
		})
	}
}

func TestSetupDisabledGlobalInspection(t *testing.T) {
	for _, kind := range []string{"missing", "invalid", "false", "true"} {
		t.Run(kind, func(t *testing.T) {
			setupPrepareIsolation(t)
			f := newSetupCommandFixture(t)
			expected, code := "configured", 0
			switch kind {
			case "missing":
				if err := os.Remove(f.global); err != nil {
					t.Fatal(err)
				}
				expected, code = "configuration_required", 1
			case "invalid":
				setupCommandWrite(t, f.global, `{"private":true,"notifications":{"desktop":{"enabled":null}}}`, 0600)
				expected, code = "configuration_invalid", 1
			case "true":
				setupCommandWrite(t, f.global, `{"notifications":{"desktop":{"enabled":true,"sound":false,"clickToFocus":false}}}`, 0600)
			}
			before := setupCommandTree(t, f.root)
			r := setupCommandRun(t, setupCommandContext(t), f.args(t, "status", "--global-config", f.global), f.composition, code)
			if r.Configuration != "disabled" || r.GlobalConfiguration != expected || r.ExplicitIntent || r.Permission != "not_checked" {
				t.Fatal(r)
			}
			if code == 1 && r.DesktopEnabled != nil {
				t.Fatal("invented boolean", r)
			}
			if code == 0 && (r.DesktopEnabled == nil || *r.DesktopEnabled != (kind == "true")) {
				t.Fatal(r)
			}
			if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
				t.Fatal("status mutated tree")
			}
		})
	}
}

func TestSetupSkillProjectionComposition(t *testing.T) {
	setupPrepareIsolation(t)
	f := newSetupCommandFixture(t)
	ctx := setupCommandContext(t)
	source := filepath.Join(f.runtime, "skills", "agent-notify", "SKILL.md")
	stage := func(data string) {
		g := f.generation(t)
		before, err := installruntime.Fingerprint(source)
		if err != nil {
			t.Fatal(err)
		}
		_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: f.control, RuntimeRoot: f.runtime, Owner: "existing-installer", ConsumerID: "hooks", RefreshOnly: true, ExpectedGeneration: &g, Files: []installruntime.File{{Path: source, Before: before, Data: []byte(data), Mode: 0600}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	stage("first skill")
	destination := filepath.Join(f.root, "user-skills", "agent-notify", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		t.Fatal(err)
	}
	client := []string{"--provider", "codex", "--config", f.config, "--command", f.command}
	selected := append(append([]string{}, client...), "--skill-destination", destination)
	run := func(args []string, want int) {
		setupCommandRun(t, ctx, f.args(t, "register", args...), f.composition, want)
	}
	run(selected, 0)
	if setupCommandRead(t, destination) != "first skill" {
		t.Fatal("projection missing")
	}
	stage("second skill")
	run(client, 0)
	if setupCommandRead(t, destination) != "first skill" {
		t.Fatal("implicit refresh")
	}
	run(selected, 0)
	if setupCommandRead(t, destination) != "second skill" {
		t.Fatal("refresh missing")
	}
	before := setupCommandTree(t, f.root)
	run(selected, 0)
	if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
		t.Fatal("samebyte mutation")
	}
	setupCommandWrite(t, destination, "tampered", 0600)
	before = setupCommandTree(t, f.root)
	run(selected, 1)
	setupCommandRun(t, ctx, f.args(t, "remove", client...), f.composition, 1)
	if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
		t.Fatal("overwrote tampering")
	}
	setupCommandWrite(t, destination, "second skill", 0600)
	setupCommandRun(t, ctx, f.args(t, "remove", client...), f.composition, 0)
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatal("projection retained", err)
	}
	snapshot, err := installruntime.ReadInstalledSnapshot(f.control)
	if err != nil || len(snapshot.Ledger.Consumers) != 1 || snapshot.Ledger.Consumers["hooks"].RuntimeRoot != f.runtime {
		t.Fatal("lost hooks", err)
	}
	setupCommandWrite(t, destination, "foreign", 0600)
	before = setupCommandTree(t, f.root)
	run(selected, 1)
	if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
		t.Fatal("foreign overwrite")
	}
}

func TestSetupPrepareProjectionParser(t *testing.T) {
	for _, args := range [][]string{
		{"prepare", "--expected-generation", "1"},
		{"prepare", "--expected-generation", "1", "--legacy-config", "relative", "--defaults-config", "/defaults"},
		{"register", "--expected-generation", "1", "--provider", "claude", "--command", "/command", "--config", "/config", "--skill-destination", "/user/agent-notify/SKILL.md"},
		{"remove", "--expected-generation", "1", "--provider", "codex", "--command", "/command", "--config", "/config", "--skill-destination", "/user/agent-notify/SKILL.md"},
	} {
		if _, _, err := parseAgentNotifySetup(args); err == nil {
			t.Fatal(args)
		}
	}
}

func TestSetupProjectionPhysicalConflicts(t *testing.T) {
	for _, kind := range []string{"relative", "symlink-parent", "symlink-file", "inside-runtime", "unowned-source"} {
		t.Run(kind, func(t *testing.T) {
			setupPrepareIsolation(t)
			f := newSetupCommandFixture(t)
			ctx := setupCommandContext(t)
			source := filepath.Join(f.runtime, "skills", "agent-notify", "SKILL.md")
			g := f.generation(t)
			if kind != "unowned-source" {
				_, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: f.control, RuntimeRoot: f.runtime, Owner: "existing-installer", ConsumerID: "hooks", RefreshOnly: true, ExpectedGeneration: &g, Files: []installruntime.File{{Path: source, Data: []byte("skill"), Mode: 0600}}})
				if err != nil {
					t.Fatal(err)
				}
			}
			destination := filepath.Join(f.root, "user", "agent-notify", "SKILL.md")
			if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
				t.Fatal(err)
			}
			code := 1
			switch kind {
			case "relative":
				destination = "relative/agent-notify/SKILL.md"
				code = 2
			case "inside-runtime":
				destination = source
			case "symlink-parent":
				alias := filepath.Join(f.root, "alias")
				if err := os.Symlink(filepath.Join(f.root, "user"), alias); err != nil {
					t.Fatal(err)
				}
				destination = filepath.Join(alias, "agent-notify", "SKILL.md")
			case "symlink-file":
				if err := os.Symlink(source, destination); err != nil {
					t.Fatal(err)
				}
			case "unowned-source":
				if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
					t.Fatal(err)
				}
				setupCommandWrite(t, source, "unowned skill", 0600)
			}
			before := setupCommandTree(t, f.root)
			args := f.args(t, "register", "--provider", "codex", "--command", f.command, "--config", f.config, "--skill-destination", destination)
			setupCommandRun(t, ctx, args, f.composition, code)
			if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
				t.Fatal("conflict changed tree")
			}
		})
	}
}

func TestSetupProjectionCreatesPhysicalParents(t *testing.T) {
	setupPrepareIsolation(t)
	f := newSetupCommandFixture(t)
	ctx := setupCommandContext(t)
	source := filepath.Join(f.runtime, "skills", "agent-notify", "SKILL.md")
	g := f.generation(t)
	_, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: f.control, RuntimeRoot: f.runtime, Owner: "existing-installer", ConsumerID: "hooks", RefreshOnly: true, ExpectedGeneration: &g, Files: []installruntime.File{{Path: source, Data: []byte("owned skill"), Mode: 0600}}})
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(f.root, "absent", "agent-notify", "SKILL.md")
	g = f.generation(t)
	args := f.args(t, "register", "--provider", "codex", "--command", f.command, "--config", f.config, "--skill-destination", destination)
	r := setupCommandRun(t, ctx, args, f.composition, 0)
	if setupCommandRead(t, destination) != "owned skill" || r.Generation != g+1 || f.generation(t) != r.Generation {
		t.Fatal("projection or generation mismatch", r)
	}
	for _, p := range []string{filepath.Dir(destination), filepath.Dir(filepath.Dir(destination))} {
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatal("nonphysical parent", p, err)
		}
	}
	before := setupCommandTree(t, f.root)
	setupCommandRun(t, ctx, f.args(t, "register", "--provider", "codex", "--command", f.command, "--config", f.config, "--skill-destination", destination), f.composition, 0)
	if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
		t.Fatal("owned projection retry mutated tree")
	}
	setupCommandRun(t, ctx, f.args(t, "remove", "--provider", "codex", "--command", f.command, "--config", f.config), f.composition, 0)
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatal("owned projection not removed", err)
	}
}
