//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/testenv"
)

const nativeHookSession = "00000000-0000-0000-0000-000000000001"

type nativeHookFixture struct {
	binary, root, cwd, codexHome, capture string
	env                                   []string
}

// Only the OS launch boundary is fake. Every hook uses the compiled public CLI,
// current product SDK adapter, real Handler, metadata reader and Notifier.
func newNativeHookFixture(t *testing.T, showLabel bool) nativeHookFixture {
	t.Helper()
	f := nativeHookFixture{binary: buildCLIBinary(t), root: t.TempDir()}
	f.cwd = filepath.Join(f.root, "sandbox-project")
	f.capture = filepath.Join(f.root, "native-args")
	values := testenv.Values(filepath.Join(f.root, "home"))
	for _, path := range values {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	f.codexHome = values["CODEX_HOME"]
	if err := os.MkdirAll(f.cwd, 0700); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(f.root, "plugin")
	commands := filepath.Join(f.root, "commands")
	// This inert bundle is never launched. PATH resolves only our open fixture;
	// no inherited tools, model sessions or actual notification APIs can run.
	e2eWrite(t, filepath.Join(plugin, "bin", "ClaudeNotifier.app", "Contents", "MacOS", "terminal-notifier-modern"), []byte("inert helper fixture"))
	e2eWrite(t, filepath.Join(commands, "open"), []byte("#!/bin/sh\nprintf '%s\\0' \"$@\" > \"$NATIVE_HOOK_ARGS\"\n"))
	configPath := filepath.Join(f.root, "config.json")
	cfg := `{"notifications":{"desktop":{"enabled":true,"sound":false,"terminalBell":false,"clickToFocus":false,"showSessionLabel":` + map[bool]string{true: "true", false: "false"}[showLabel] + `,"terminalBundleId":"com.apple.Terminal"},"webhook":{"enabled":false},"respectDoNotDisturb":"off","respectDisplaySleep":false,"notifyOnlyWhenUnfocused":false,"notifyDelaySeconds":0,"suppressQuestionAfterAnyNotificationSeconds":0,"suppressQuestionAfterTaskCompleteSeconds":0}}`
	e2eWrite(t, configPath, []byte(cfg))
	values["PATH"] = commands
	values["PLUGIN_ROOT"] = plugin
	values["CLAUDE_PLUGIN_ROOT"] = plugin
	values["AGENT_NOTIFICATIONS_CONFIG"] = configPath
	values["NATIVE_HOOK_ARGS"] = f.capture
	for key, value := range values {
		f.env = append(f.env, key+"="+value)
	}
	return f
}

func (f nativeHookFixture) question(t *testing.T, product, turn, transcript string, title any) []string {
	t.Helper()
	payload := map[string]any{
		"session_id": nativeHookSession, "turn_id": turn, "cwd": f.cwd,
		"transcript_path": transcript, "hook_event_name": "PreToolUse",
		"tool_name": "AskUserQuestion", "tool_use_id": "call-" + turn,
		"tool_input": map[string]any{"questions": []any{map[string]any{
			"question": "Use the new installer?", "header": "Installer",
			"options": []any{map[string]any{"label": "SECRET_OPTION", "description": "SECRET_DESCRIPTION"}},
		}}},
	}
	if product == "codex" {
		payload["tool_name"] = "request_user_input"
	}
	if title != nil {
		payload["session_title"] = title
	}
	return f.runHook(t, product, "PreToolUse", payload)
}

func (f nativeHookFixture) runHook(t *testing.T, product, event string, payload map[string]any) []string {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.capture); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, f.binary, "handle-hook", event, "--product", product)
	cmd.Dir, cmd.Env, cmd.Stdin = f.cwd, f.env, strings.NewReader(string(raw))
	output, err := cmd.CombinedOutput()
	if err != nil || len(output) != 0 {
		t.Fatalf("hook wire contract: err=%v output=%q", err, output)
	}
	captured := string(e2eRead(t, f.capture))
	if !strings.HasSuffix(captured, "\x00") {
		t.Fatal("incomplete native launch capture")
	}
	return strings.Split(strings.TrimSuffix(captured, "\x00"), "\x00")
}

func assertNativeHookQuestion(t *testing.T, args []string, subtitle string) {
	t.Helper()
	for flag, want := range map[string]string{
		"-title":    "❓ Use the new installer?",
		"-subtitle": subtitle, "-message": "Use the new installer?", "-threadID": nativeHookSession,
	} {
		var got string
		count := 0
		for i := 0; i+1 < len(args); i++ {
			if args[i] == flag {
				got, count = args[i+1], count+1
			}
		}
		if count != 1 || got != want {
			t.Fatalf("%s: got %q (%d occurrences), want %q; args=%q", flag, got, count, want, args)
		}
	}
	if strings.Contains(strings.Join(args, " "), "SECRET_") || strings.Contains(strings.Join(args, " "), "OLD_QUESTION") {
		t.Fatalf("private options or stale transcript leaked: %q", args)
	}
}

// Regression: a CLI/source wiring omission can discard the native title or
// current tool_input even when Handler and Notifier unit tests pass separately.
func TestNativeHookE2EClaudeCurrentQuestion(t *testing.T) {
	f := newNativeHookFixture(t, true)
	transcript := filepath.Join(f.root, nativeHookSession+".jsonl")
	e2eWrite(t, transcript, []byte(`{"type":"custom-title","sessionId":"`+nativeHookSession+`","customTitle":"Old transcript title"}`+"\n"+
		`{"type":"assistant","sessionId":"`+nativeHookSession+`","message":{"role":"assistant","content":[{"type":"tool_use","name":"AskUserQuestion","input":{"questions":[{"question":"OLD_QUESTION"}]}}]}}`+"\n"))
	args := f.question(t, "claude", "turn-1", transcript, "Fix [SDK] | installer")
	assertNativeHookQuestion(t, args, "Fix [SDK] | installer · sandbox-project")
}

// Regression: fresh hooks must reread exact-thread index entries after a rename
// instead of retaining stale presentation or borrowing another session's title.
func TestNativeHookE2ECodexRenameOnDistinctTurn(t *testing.T) {
	f := newNativeHookFixture(t, true)
	index := filepath.Join(f.codexHome, "session_index.jsonl")
	e2eWrite(t, index, []byte(`{"id":"`+nativeHookSession+`","thread_name":"First [SDK] | name"}`+"\n"+
		`{"id":"foreign-thread","thread_name":"Foreign name"}`+"\n"))
	assertNativeHookQuestion(t, f.question(t, "codex", "turn-1", "", nil), "First [SDK] | name · sandbox-project")
	file, err := os.OpenFile(index, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString(`{"id":"` + nativeHookSession + `","thread_name":"Renamed [SDK] | name"}` + "\n")
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("append rename: %v %v", writeErr, closeErr)
	}
	assertNativeHookQuestion(t, f.question(t, "codex", "turn-2", "", nil), "Renamed [SDK] | name · sandbox-project")
}

// Regression: malformed optional metadata and a foreign rollout must not break
// a real question or accidentally attribute it to the local index title.
func TestNativeHookE2ECodexMetadataFallback(t *testing.T) {
	for _, kind := range []string{"malformed", "foreign rollout"} {
		t.Run(kind, func(t *testing.T) {
			f := newNativeHookFixture(t, true)
			index := filepath.Join(f.codexHome, "session_index.jsonl")
			var transcript string
			if kind == "malformed" {
				e2eWrite(t, index, []byte(`{"id":"foreign-thread","thread_name":"Foreign name"}`+"\n"+
					`{"id":"`+nativeHookSession+`","thread_name":42}`+"\n{oops\n"))
			} else {
				e2eWrite(t, index, []byte(`{"id":"`+nativeHookSession+`","thread_name":"Local index name"}`+"\n"))
				transcript = filepath.Join(f.root, "foreign", "rollout-"+nativeHookSession+".jsonl")
				e2eWrite(t, transcript, []byte("{}\n"))
			}
			assertNativeHookQuestion(t, f.question(t, "codex", "turn-1", transcript, nil), "bold 00000000 · sandbox-project")
		})
	}
}

// Regression: the public configuration must hide both native and generated
// identities across the full hook chain without hiding the current question.
func TestNativeHookE2EHiddenLabel(t *testing.T) {
	f := newNativeHookFixture(t, false)
	assertNativeHookQuestion(t, f.question(t, "claude", "turn-1", "", "Hidden [SDK] | name"), "sandbox-project")
}

// Regression: the public completion hook must deliver the concise native title
// while retaining the opaque thread identity and the final assistant message.
func TestNativeHookE2EClaudeCompletionTitle(t *testing.T) {
	f := newNativeHookFixture(t, true)
	args := f.runHook(t, "claude", "Stop", map[string]any{
		"session_id": nativeHookSession, "cwd": f.cwd, "hook_event_name": "Stop",
		"session_title": "Понятные уведомления", "last_assistant_message": "Completed the notification change.",
	})
	for flag, want := range map[string]string{
		"-title": "✅ [Понятные уведомления]", "-subtitle": "sandbox-project",
		"-message": "Completed the notification change.", "-threadID": nativeHookSession,
	} {
		var got string
		count := 0
		for i := 0; i+1 < len(args); i++ {
			if args[i] == flag {
				got, count = args[i+1], count+1
			}
		}
		if count != 1 || got != want {
			t.Fatalf("%s: got %q (%d occurrences), want %q; args=%q", flag, got, count, want, args)
		}
	}
}
