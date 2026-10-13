package hooks

import "testing"

// Regression: the async schema exposes its actual question as title, while a
// synchronous header alone must not be misrepresented as the question text.
func TestQuestionInsightAsyncTitleAndHeaderFallback(t *testing.T) {
	async := questionInsight([]byte(`{"questions":[{"title":"Restart Codex?","options":["secret-option"]}]}`), true)
	if async.Question != "Restart Codex?" || async.Body != "Restart Codex?" {
		t.Fatalf("async question was lost: %+v", async)
	}
	header := questionInsight([]byte(`{"questions":[{"header":"Approach"}]}`), false)
	if header.Question != "" || header.Body != "Approach" {
		t.Fatalf("header was invented into a question: %+v", header)
	}
}
