package setupwizard

import (
	"path/filepath"
	"testing"
)

func TestApplyEnvDefaultsExplicitWins(t *testing.T) {
	explicitCodex := filepath.Join(t.TempDir(), "explicit-codex")
	explicitClaude := filepath.Join(t.TempDir(), "explicit-claude")
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "env-codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "env-claude"))
	t.Setenv("HOME", filepath.Join(t.TempDir(), "home"))
	got := ApplyEnvDefaults(Request{CodexHome: explicitCodex, ClaudeConfig: explicitClaude})
	if got.CodexHome != explicitCodex || got.ClaudeConfig != explicitClaude {
		t.Fatalf("explicit lost: %+v", got)
	}
}

func TestApplyEnvDefaultsUsesNamedEnvNotHOME(t *testing.T) {
	envCodex := filepath.Join(t.TempDir(), "env-codex")
	envClaude := filepath.Join(t.TempDir(), "env-claude")
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("CODEX_HOME", envCodex)
	t.Setenv("CLAUDE_CONFIG_DIR", envClaude)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	got := ApplyEnvDefaults(Request{})
	if got.CodexHome != envCodex || got.ClaudeConfig != envClaude {
		t.Fatalf("env defaults: %+v", got)
	}
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	empty := ApplyEnvDefaults(Request{})
	if empty.CodexHome != "" || empty.ClaudeConfig != "" {
		t.Fatalf("HOME used as profile fallback: %+v", empty)
	}
}
