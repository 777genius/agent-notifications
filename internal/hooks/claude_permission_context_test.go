package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeClaudePermissionFixture(t *testing.T, session string, rows ...map[string]any) SessionContext {
	t.Helper()
	path := filepath.Join(t.TempDir(), session+".jsonl")
	var out []byte
	for _, row := range rows {
		raw, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, raw...)
		out = append(out, '\n')
	}
	if err := os.WriteFile(path, out, 0600); err != nil {
		t.Fatal(err)
	}
	return SessionContext{SessionID: session, TranscriptPath: path}
}

func claudeToolRecord(session, id, tool string, at time.Time) map[string]any {
	return map[string]any{
		"type": "assistant", "sessionId": session, "timestamp": at.Format(time.RFC3339Nano),
		"message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": id, "name": tool,
				"input": map[string]any{"command": "echo password=private-secret", "description": "secret note"}},
		}},
	}
}

func claudeToolResultRecord(session, id string, at time.Time) map[string]any {
	return map[string]any{
		"type": "user", "sessionId": session, "timestamp": at.Format(time.RFC3339Nano),
		"message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": id, "content": "private-secret"},
		}},
	}
}

func TestClaudePermissionRecentToolRequiresUniqueFreshUnresolvedRecord(t *testing.T) {
	now := time.Now().UTC()
	t.Run("one pending known tool yields only its name", func(t *testing.T) {
		s := writeClaudePermissionFixture(t, "test-one", claudeToolRecord("test-one", "call-1", "Bash", now))
		body := claudePermissionBody("Claude needs your permission", s)
		if body != "Claude needs your permission (recent tool: Bash)" ||
			strings.Contains(body, "private-secret") || strings.Contains(body, "echo") {
			t.Fatalf("unexpected permission body: %q", body)
		}
	})
	t.Run("ordinary Claude user text does not break tool matching", func(t *testing.T) {
		s := writeClaudePermissionFixture(t, "test-text",
			map[string]any{"type": "user", "sessionId": "test-text",
				"message": map[string]any{"role": "user", "content": "Please continue"}},
			claudeToolRecord("test-text", "call-1", "Bash", now))
		if tool := claudePermissionRecentTool(s); tool != "Bash" {
			t.Fatalf("string-form Claude user message suppressed tool evidence: %q", tool)
		}
	})
	for _, unsafe := range []struct{ label, line string }{
		{"malformed JSON", "not json\n"},
		{"oversized line", strings.Repeat("x", claudePermissionMaxRecord+1)+"\n"},
	} {
		t.Run(unsafe.label+" cannot hide another operation", func(t *testing.T) {
			s := writeClaudePermissionFixture(t, "test-bad",
				claudeToolRecord("test-bad", "call-1", "Bash", now))
			file, err := os.OpenFile(s.TranscriptPath, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, writeErr := file.WriteString(unsafe.line)
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				t.Fatal(writeErr, closeErr)
			}
			if name := claudePermissionRecentTool(s); name != "" {
				t.Fatalf("partial evidence produced tool label: %q", name)
			}
		})
	}
	t.Run("completed call is not a permission candidate", func(t *testing.T) {
		s := writeClaudePermissionFixture(t, "test-done",
			claudeToolRecord("test-done", "call-1", "Bash", now),
			claudeToolResultRecord("test-done", "call-1", now.Add(time.Second)))
		if name := claudePermissionRecentTool(s); name != "" {
			t.Fatalf("completed tool incorrectly called pending: %q", name)
		}
	})
	t.Run("two concurrent calls are ambiguous", func(t *testing.T) {
		s := writeClaudePermissionFixture(t, "test-parallel",
			claudeToolRecord("test-parallel", "call-1", "Bash", now),
			claudeToolRecord("test-parallel", "call-2", "Edit", now))
		if name := claudePermissionRecentTool(s); name != "" {
			t.Fatalf("guessed among concurrent calls: %q", name)
		}
	})
	t.Run("unknown concurrent tool prevents a false single match", func(t *testing.T) {
		s := writeClaudePermissionFixture(t, "test-unknown",
			claudeToolRecord("test-unknown", "call-1", "Bash", now),
			claudeToolRecord("test-unknown", "call-2", "mcp__private_secret", now))
		if name := claudePermissionRecentTool(s); name != "" {
			t.Fatalf("ignored the other in-flight tool: %q", name)
		}
	})
	for _, malformed := range []struct{ kind, brokenType string }{
		{"tool use without id", "tool_use"},
		{"tool result without tool_use_id", "tool_result"},
	} {
		t.Run(malformed.kind+" cannot hide pending work", func(t *testing.T) {
			broken := map[string]any{"type": malformed.brokenType, "name": "Edit"}
			if malformed.brokenType == "tool_result" {
				broken["content"] = "tool result"
			}
			s := writeClaudePermissionFixture(t, "test-idless",
				claudeToolRecord("test-idless", "call-valid", "Bash", now),
				map[string]any{"type": "assistant", "sessionId": "test-idless",
					"timestamp": now.Format(time.RFC3339Nano), "message": map[string]any{
						"role": "assistant", "content": []any{broken}}})
			if name := claudePermissionRecentTool(s); name != "" {
				t.Fatalf("missing correlation ID leaked guessed tool: %q", name)
			}
		})
	}
	t.Run("misplaced tool result cannot falsely resolve another call", func(t *testing.T) {
		s := writeClaudePermissionFixture(t, "test-wrong-result-role",
			claudeToolRecord("test-wrong-result-role", "call-bash", "Bash", now),
			claudeToolRecord("test-wrong-result-role", "call-edit", "Edit", now),
			map[string]any{"type": "assistant", "sessionId": "test-wrong-result-role",
				"timestamp": now.Format(time.RFC3339Nano), "message": map[string]any{
					"role": "assistant", "content": []any{
						map[string]any{"type": "tool_result", "tool_use_id": "call-bash"},
					}}},
		)
		if name := claudePermissionRecentTool(s); name != "" {
			t.Fatalf("invalid assistant tool_result erased competing permission: %q", name)
		}
	})
	t.Run("misplaced tool use cannot be silently ignored", func(t *testing.T) {
		s := writeClaudePermissionFixture(t, "test-wrong-use-role",
			claudeToolRecord("test-wrong-use-role", "call-bash", "Bash", now),
			map[string]any{"type": "user", "sessionId": "test-wrong-use-role",
				"timestamp": now.Format(time.RFC3339Nano), "message": map[string]any{
					"role": "user", "content": []any{
						map[string]any{"type": "tool_use", "id": "call-edit", "name": "Edit"},
					}}},
		)
		if name := claudePermissionRecentTool(s); name != "" {
			t.Fatalf("invalid user tool_use concealed conflicting operation: %q", name)
		}
	})
	t.Run("stale unresolved plus fresh call is ambiguous", func(t *testing.T) {
		s := writeClaudePermissionFixture(t, "test-stale-parallel",
			claudeToolRecord("test-stale-parallel", "call-old", "Bash", now.Add(-50*time.Second)),
			claudeToolRecord("test-stale-parallel", "call-fresh", "Edit", now))
		if name := claudePermissionRecentTool(s); name != "" {
			t.Fatalf("dropped old unresolved call and guessed fresh permission: %q", name)
		}
	})
	t.Run("oversized historical transcript never proves uniqueness", func(t *testing.T) {
		s := writeClaudePermissionFixture(t, "test-truncated",
			claudeToolRecord("test-truncated", "call-old", "Bash", now.Add(-time.Minute)),
			map[string]any{"type": "user", "sessionId": "test-truncated", "message": map[string]any{
				"role": "user", "content": strings.Repeat("history", claudePermissionTailBytes/5)}},
			claudeToolRecord("test-truncated", "call-new", "Edit", now))
		if name := claudePermissionRecentTool(s); name != "" {
			t.Fatalf("truncated transcript falsely inferred uniqueness: %q", name)
		}
	})
	t.Run("incomplete append cannot lend an older tool identity", func(t *testing.T) {
		s := writeClaudePermissionFixture(t, "test-unflushed",
			claudeToolRecord("test-unflushed", "call-1", "Bash", now))
		file, err := os.OpenFile(s.TranscriptPath, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.WriteString(`{"type":"assistant","message":{"content":[`)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatal(writeErr, closeErr)
		}
		if name := claudePermissionRecentTool(s); name != "" {
			t.Fatalf("guessed from unstable transcript tail: %q", name)
		}
	})
	t.Run("old tool does not become current prompt", func(t *testing.T) {
		s := writeClaudePermissionFixture(t, "test-old",
			claudeToolRecord("test-old", "call-1", "Bash", now.Add(-2*time.Minute)))
		if name := claudePermissionRecentTool(s); name != "" {
			t.Fatalf("stale transcript matched: %q", name)
		}
	})
	t.Run("wrong session record refuses", func(t *testing.T) {
		s := writeClaudePermissionFixture(t, "test-right",
			claudeToolRecord("test-other", "call-1", "Bash", now))
		if name := claudePermissionRecentTool(s); name != "" {
			t.Fatalf("cross-session data matched: %q", name)
		}
	})
	t.Run("missing, mismatched, symlink and partial transcript fail closed", func(t *testing.T) {
		s := writeClaudePermissionFixture(t, "test-path",
			claudeToolRecord("test-path", "call-1", "Bash", now))
		if name := claudePermissionRecentTool(SessionContext{SessionID: "different", TranscriptPath: s.TranscriptPath}); name != "" {
			t.Fatalf("mismatched path was accepted: %q", name)
		}
		if name := claudePermissionRecentTool(SessionContext{SessionID: "test-path", TranscriptPath: s.TranscriptPath + "-missing"}); name != "" {
			t.Fatalf("missing path was accepted: %q", name)
		}
		link := filepath.Join(t.TempDir(), "test-path.jsonl")
		if err := os.Symlink(s.TranscriptPath, link); err == nil {
			if name := claudePermissionRecentTool(SessionContext{SessionID: "test-path", TranscriptPath: link}); name != "" {
				t.Fatalf("symlink path was accepted: %q", name)
			}
		}
	})
	t.Run("specific host text remains authoritative", func(t *testing.T) {
		s := writeClaudePermissionFixture(t, "test-host",
			claudeToolRecord("test-host", "call-1", "Bash", now))
		host := "Dangerous operation needs your permission"
		if body := claudePermissionBody(host, s); body != host {
			t.Fatalf("overrode host-specific text: %q", body)
		}
	})
	t.Run("no transcript does not prevent notification", func(t *testing.T) {
		if body := claudePermissionBody("Claude needs your permission", SessionContext{SessionID: "s"}); body != "Claude needs your permission" {
			t.Fatalf("changed host fallback: %q", body)
		}
	})
}

func TestClaudeGenericPermissionHookUsesSafeRecentContext(t *testing.T) {
	now := time.Now().UTC()
	session := "test-notif-context"
	s := writeClaudePermissionFixture(t, session,
		claudeToolRecord(session, "call-1", "Bash", now))
	h, notifier, _ := newTestHandler(t, codexTestConfig())
	wire := HookData{SessionID: session, HookEventName: "Notification",
		NotificationType: "permission_prompt", Message: "Claude needs your permission",
		TranscriptPath: s.TranscriptPath}
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.HandleHook("Notification", strings.NewReader(string(raw))); err != nil {
		t.Fatal(err)
	}
	if notifier.callCount() != 1 {
		t.Fatalf("expected one permission notification, got %d", notifier.callCount())
	}
	message := notifier.lastCall().message
	if !strings.Contains(message, "recent tool: Bash") || strings.Contains(message, "private-secret") {
		t.Fatalf("wrong notification body: %q", message)
	}
}
