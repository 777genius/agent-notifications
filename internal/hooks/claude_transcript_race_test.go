package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/config"
)

// A transcript record appended after the first parse but before the settle
// size check must still be delivered. A post-parse size baseline misses it.
func TestClaudeStopAppendBetweenParseAndSettle(t *testing.T) {
	setTestHome(t, t.TempDir())
	path := filepath.Join(t.TempDir(), "session.jsonl")
	initial := `{"type":"user","timestamp":"2026-09-24T12:00:00Z","message":{"role":"user","content":"say ready"}}` + "\n"
	closing := `{"type":"assistant","timestamp":"2026-09-24T12:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"Ready after parse."}]}}` + "\n"
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	h, mock, _ := newTestHandler(t, &config.Config{
		Notifications: config.NotificationsConfig{Desktop: config.DesktopConfig{Enabled: true}},
	})
	parseCalls := 0
	h.afterClaudeTranscriptParse = func() {
		parseCalls++
		if parseCalls != 1 {
			return
		}
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString(closing); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	payload := buildHookDataJSON(HookData{
		SessionID: "parse-stat-race-609", TranscriptPath: path,
		HookEventName: "Stop", CWD: t.TempDir(),
	})
	if err := h.HandleHook("Stop", payload); err != nil {
		t.Fatal(err)
	}
	if parseCalls != 1 {
		t.Fatalf("test seam must append exactly once after the initial parse, got %d", parseCalls)
	}
	if mock.callCount() != 1 || !strings.Contains(mock.lastCall().message, "Ready after parse") {
		t.Fatalf("late transcript record omitted: calls=%d last=%+v", mock.callCount(), mock.lastCall())
	}
	// A replay must not emit the already delivered completion a second time.
	h.afterClaudeTranscriptParse = nil
	if err := h.HandleHook("Stop", buildHookDataJSON(HookData{
		SessionID: "parse-stat-race-609", TranscriptPath: path,
		HookEventName: "Stop", CWD: t.TempDir(),
	})); err != nil {
		t.Fatal(err)
	}
	if mock.callCount() != 1 {
		t.Fatalf("Stop replay generated duplicate desktop delivery: %d", mock.callCount())
	}
}
