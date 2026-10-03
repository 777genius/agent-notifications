package hooks

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/pkg/jsonl"
)

// Regression: a pending current tool call must never display a previous
// question from a transcript that has not been flushed yet.
func TestClaudeCurrentQuestionWinsOverStaleTranscript(t *testing.T) {
	for _, input := range []string{
		`{"questions":[{"header":"Choice","question":"Restart Codex after the update?","options":[{"label":"Yes","description":"private-option-detail"}]}]}`,
		`{"questions":[]}`,
		`{}`,
	} {
		t.Run(input, func(t *testing.T) {
			setTestHome(t, t.TempDir())
			handler, desktop, _ := newTestHandler(t, config.DefaultConfig())
			transcript := createTempTranscript(t, []jsonl.Message{{
				Type: "assistant", Message: jsonl.MessageContent{Role: "assistant", Content: []jsonl.Content{{
					Type: "text", Text: "OLD QUESTION: Delete the database?",
				}}},
			}})
			payload, err := json.Marshal(map[string]any{
				"session_id": "test-current-question", "cwd": t.TempDir(),
				"tool_name": "AskUserQuestion", "transcript_path": transcript,
				"tool_input": json.RawMessage(input),
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := handler.HandleHook("PreToolUse", strings.NewReader(string(payload))); err != nil {
				t.Fatal(err)
			}
			call := desktop.lastCall()
			if call == nil {
				t.Fatal("question notification was lost")
			}
			if strings.Contains(call.message, "OLD QUESTION") || strings.Contains(call.message, "private-option-detail") {
				t.Fatalf("notification contains stale or non-question data: %q", call.message)
			}
			if strings.Contains(input, "Restart Codex") && !strings.Contains(call.message, "Restart Codex after the update?") {
				t.Fatalf("current question was lost: %q", call.message)
			}
		})
	}
}
