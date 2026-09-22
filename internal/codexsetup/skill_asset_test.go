package codexsetup

import (
	"context"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExplicitSkillAssetInstallAndRefresh(t *testing.T) {
	source, destination := fakeBundle(t), t.TempDir()
	control := filepath.Join(t.TempDir(), "control")
	write := func(rel, data string) {
		t.Helper()
		p := filepath.Join(source, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	const skill = "skills/agent-notify/SKILL.md"
	write(skill, "first skill")
	for _, rel := range []string{"skills/foreign/SKILL.md", "skills/agent-notify/private.txt", "skills/agent-notify/nested/SKILL.md"} {
		write(rel, "not an owned asset")
	}
	for _, content := range []string{"first skill", "updated skill"} {
		write(skill, content)
		files, err := stageRuntimeFiles(source, destination)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, RuntimeRoot: destination, Owner: "existing-installer", ConsumerID: "test", Files: files})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(destination, filepath.FromSlash(skill)))
		if err != nil || string(got) != content {
			t.Fatalf("skill = %q, %v", got, err)
		}
	}
	for _, rel := range []string{"skills/foreign", "skills/agent-notify/private.txt", "skills/agent-notify/nested"} {
		if _, err := os.Lstat(filepath.Join(destination, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("unowned asset copied: %s, %v", rel, err)
		}
	}
}

// TestScriptsAssetOnlyIncludesIterm2Helper guards the iTerm2 exact-tab focus
// path: getiTerm2PythonEnv (internal/notifier/tmux_iterm2.go) resolves
// scripts/iterm2-select-tab.py under CLAUDE_PLUGIN_ROOT, which for Codex is
// the setup-codex install root. If that file is missing there, Codex
// notifications in iTerm2 silently degrade to plain app-activate instead of
// focusing the originating tab. Dev/CI scripts must stay excluded.
func TestScriptsAssetOnlyIncludesIterm2Helper(t *testing.T) {
	source, destination := fakeBundle(t), t.TempDir()
	control := filepath.Join(t.TempDir(), "control")
	write := func(rel, data string) {
		t.Helper()
		p := filepath.Join(source, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write("scripts/iterm2-select-tab.py", "#!/usr/bin/env python3\n")
	write("scripts/dev-local-plugin.sh", "#!/bin/sh\nexit 0\n")

	files, err := stageRuntimeFiles(source, destination)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, RuntimeRoot: destination, Owner: "existing-installer", ConsumerID: "test", Files: files})
	cancel()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(destination, "scripts", "iterm2-select-tab.py")); err != nil {
		t.Fatalf("iterm2-select-tab.py not installed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(destination, "scripts", "dev-local-plugin.sh")); !os.IsNotExist(err) {
		t.Fatalf("dev/CI script must not be copied into the install dir: %v", err)
	}
}
