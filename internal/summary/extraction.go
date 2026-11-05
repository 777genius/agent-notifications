package summary

import (
	"time"
	"github.com/777genius/agent-notifications/pkg/jsonl"
)

// extractAskUserQuestion extracts the last AskUserQuestion with recency check
// Returns (question, isRecent)
func extractAskUserQuestion(messages []jsonl.Message) (string, bool) {
	// Find last AskUserQuestion tool
	var questionText string
	var questionTimestamp string

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Type != "assistant" {
			continue
		}

		for _, content := range msg.Message.Content {
			if content.Type == "tool_use" && content.Name == "AskUserQuestion" {
				// Extract question from input.questions[0].question
				if questions, ok := content.Input["questions"].([]interface{}); ok && len(questions) > 0 {
					if q, ok := questions[0].(map[string]interface{}); ok {
						if qtext, ok := q["question"].(string); ok {
							questionText = qtext
							questionTimestamp = msg.Timestamp
							break
						}
					}
				}
			}
		}
		if questionText != "" {
			break
		}
	}

	if questionText == "" {
		return "", false
	}

	// Check recency (60s window)
	lastAssistantTS := jsonl.GetLastAssistantTimestamp(messages)
	if lastAssistantTS == "" || questionTimestamp == "" {
		return questionText, false
	}

	questionTime, err1 := time.Parse(time.RFC3339, questionTimestamp)
	lastTime, err2 := time.Parse(time.RFC3339, lastAssistantTS)

	if err1 != nil || err2 != nil {
		return questionText, false
	}

	// Check if question is within 60s of last assistant message
	age := lastTime.Sub(questionTime)
	isRecent := age >= 0 && age <= 60*time.Second

	return questionText, isRecent
}

// extractExitPlanModePlan extracts the plan text from ExitPlanMode tool
func extractExitPlanModePlan(messages []jsonl.Message) string {
	input := jsonl.ExtractToolInput(messages, "ExitPlanMode")
	if plan, ok := input["plan"].(string); ok {
		return plan
	}
	return ""
}
