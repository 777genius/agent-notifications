package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeHookCLIAllEventsKeepWireContract(t *testing.T) {
	env := claudeCLIEnv(t)
	cases := []struct {
		event   string
		payload string
	}{
		{
			event:   "PreToolUse",
			payload: `{"session_id":"claude-pretool","cwd":"/tmp/project","transcript_path":"/tmp/transcript.jsonl","hook_event_name":"PreToolUse","tool_name":"Read"}`,
		},
		{
			event:   "Notification",
			payload: `{"session_id":"claude-notification","cwd":"/tmp/project","transcript_path":"/tmp/transcript.jsonl","hook_event_name":"Notification"}`,
		},
		{
			event:   "Stop",
			payload: `{"session_id":"claude-stop","cwd":"/tmp/project","transcript_path":"/tmp/transcript.jsonl","hook_event_name":"Stop","last_assistant_message":"done"}`,
		},
		{
			event:   "SubagentStop",
			payload: `{"session_id":"claude-subagent","cwd":"/tmp/project","transcript_path":"/tmp/transcript.jsonl","hook_event_name":"SubagentStop","last_assistant_message":"subagent done"}`,
		},
		{
			event:   "TeammateIdle",
			payload: `{"session_id":"claude-team","cwd":"/tmp/project","transcript_path":"/tmp/transcript.jsonl","hook_event_name":"TeammateIdle","team_name":"team","teammate_name":"peer"}`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.event, func(t *testing.T) {
			result := runCLI(t, env, testCase.payload, "handle-hook", testCase.event)
			if result.exitCode != 0 {
				t.Fatalf("exit = %d, want 0; stderr = %q", result.exitCode, result.stderr)
			}
			if result.stdout != "" {
				t.Fatalf("stdout = %q, want empty", result.stdout)
			}
			assertClaudeFixtureStderr(t, result.stderr)
		})
	}
}

func TestClaudeHookCLIIgnoresLegacyUnknownTypedField(t *testing.T) {
	env := claudeCLIEnv(t)
	payload := `{"session_id":"claude-invalid-typed-field","cwd":"/tmp/project","transcript_path":"/tmp/transcript.jsonl","hook_event_name":"Stop","stop_hook_active":"false","last_assistant_message":"done"}`
	routes := [][]string{
		{"handle-hook", "Stop"},
		{"handle-hook", "Stop", "--product", "claude"},
		{"handle-hook", "Stop", "--product=claude"},
	}

	for _, args := range routes {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			result := runCLI(t, env, payload, args...)
			if result.exitCode != 0 {
				t.Fatalf("exit = %d, want legacy success; stderr = %q", result.exitCode, result.stderr)
			}
			if result.stdout != "" {
				t.Fatalf("stdout = %q, want empty", result.stdout)
			}
			assertClaudeFixtureStderr(t, result.stderr)
		})
	}
}

func TestClaudeHookCLIExplicitProductKeepsLoudErrors(t *testing.T) {
	env := claudeCLIEnv(t)
	routes := [][]string{
		{"handle-hook", "Stop"},
		{"handle-hook", "Stop", "--product", "claude"},
		{"handle-hook", "Stop", "--product=claude"},
	}

	for _, args := range routes {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			result := runCLI(t, env, `{oops`, args...)
			if result.exitCode == 0 {
				t.Fatal("malformed Claude payload must exit non-zero")
			}
			if result.stdout != "" {
				t.Fatalf("stdout = %q, want empty", result.stdout)
			}
			if !strings.Contains(result.stderr, "failed to parse hook data") {
				t.Fatalf("stderr = %q, want parse failure", result.stderr)
			}
		})
	}
}

func TestClaudeHookCLIConfigWarningsStayOnStderr(t *testing.T) {
	home := t.TempDir()
	pluginRoot := t.TempDir()
	configDir := filepath.Join(home, ".claude", "claude-notifications-go")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	validConfig := `{"notifications":{"desktop":{"enabled":false},"webhook":{"enabled":false}}}`
	configPath := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configPath, []byte(validConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(configPath, 0o644); err != nil {
		t.Fatal(err)
	}

	env := []string{
		"HOME=" + home,
		"USERPROFILE=" + home,
		"PLUGIN_ROOT=" + pluginRoot,
		"CLAUDE_PLUGIN_ROOT=" + pluginRoot,
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + t.TempDir(),
	}
	payload := `{"session_id":"claude-warning","cwd":"/tmp/project","transcript_path":"/tmp/transcript.jsonl","hook_event_name":"Stop","last_assistant_message":"done"}`
	routes := [][]string{
		{"handle-hook", "Stop"},
		{"handle-hook", "Stop", "--product", "claude"},
	}

	for _, args := range routes {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			result := runCLI(t, env, payload, args...)
			if result.exitCode != 0 {
				t.Fatalf("exit = %d, want 0; stderr = %q", result.exitCode, result.stderr)
			}
			if result.stdout != "" {
				t.Fatalf("stdout = %q, want empty", result.stdout)
			}
			if !strings.Contains(result.stderr, "ConfigPublicReadable") {
				t.Fatalf("stderr = %q, want config diagnostic", result.stderr)
			}
		})
	}
}

func TestClaudeHookCLIJudgeModeSuppressesBeforeDecode(t *testing.T) {
	env := append(claudeCLIEnv(t), "CLAUDE_HOOK_JUDGE_MODE=true")
	routes := [][]string{
		{"handle-hook", "Stop"},
		{"handle-hook", "Stop", "--product", "claude"},
	}

	for _, args := range routes {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			result := runCLI(t, env, `{oops`, args...)
			if result.exitCode != 0 {
				t.Fatalf("exit = %d, want 0; stderr = %q", result.exitCode, result.stderr)
			}
			if result.stdout != "" {
				t.Fatalf("stdout = %q, want empty", result.stdout)
			}
			assertClaudeFixtureStderr(t, result.stderr)
		})
	}
}

func claudeCLIEnv(t *testing.T) []string {
	t.Helper()
	env := codexCLIEnv(t)
	for _, entry := range env {
		if strings.HasPrefix(entry, "HOME=") {
			configPath := filepath.Join(strings.TrimPrefix(entry, "HOME="), ".claude", "claude-notifications-go", "config.json")
			if err := os.Chmod(configPath, 0o600); err != nil {
				t.Fatal(err)
			}
			return env
		}
	}
	t.Fatal("test HOME missing")
	return nil
}

func assertClaudeFixtureStderr(t *testing.T, stderr string) {
	t.Helper()
	// Windows ACLs can make the isolated fixture config publicly readable even
	// after Chmod. The warning is part of Claude CLI's existing contract.
	if stderr != "" && stderr != "ConfigPublicReadable\n" {
		t.Fatalf("stderr = %q, want empty or fixture permission warning", stderr)
	}
}
