package summary

import (
	"fmt"
	"strings"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/pkg/jsonl"
)

// generateQuestionBody generates the body text for question status (no actions appended).
// Improved logic: extracts meaningful question text with markdown cleanup.
func generateQuestionBody(messages []jsonl.Message) string {
	// 1) Try to extract AskUserQuestion tool (with recency check)
	question, isRecent := extractAskUserQuestion(messages)
	if question != "" && isRecent {
		return truncateText(CleanMarkdown(question), 150)
	}

	// 2) Get recent messages from current response using helper
	recentMessages := getRecentAssistantMessages(messages, QuestionMessagesWindow)
	texts := jsonl.ExtractTextFromMessages(recentMessages)

	// Strategy A: Find texts with "?" and prioritize short ones
	var questionTexts []string
	for i := len(texts) - 1; i >= 0; i-- {
		if strings.Contains(texts[i], "?") {
			questionTexts = append(questionTexts, texts[i])
		}
	}

	// If we found questions, pick the shortest one (likely most direct)
	if len(questionTexts) > 0 {
		shortestQuestion := questionTexts[0]
		for _, q := range questionTexts {
			if len(q) < len(shortestQuestion) && len(q) > 10 {
				shortestQuestion = q
			}
		}
		return truncateText(CleanMarkdown(shortestQuestion), 150)
	}

	// Strategy B: No "?" found, take first sentence from last assistant message
	if len(texts) > 0 {
		lastText := texts[len(texts)-1]
		cleaned := CleanMarkdown(lastText)
		firstSentence := extractFirstSentence(cleaned)
		if len(firstSentence) > 10 {
			return truncateText(firstSentence, 150)
		}
	}

	// 3) Final fallback: generic prompt
	return "Claude needs your input to continue"
}

// generatePlanBody generates the body text for plan_ready status (no actions appended).
// Matches bash: lib/summarizer.sh lines 471-492.
func generatePlanBody(messages []jsonl.Message) string {
	plan := extractExitPlanModePlan(messages)
	if plan != "" {
		// Get first non-empty line, clean markdown
		for _, line := range strings.Split(plan, "\n") {
			cleaned := CleanMarkdown(line)
			if strings.TrimSpace(cleaned) != "" {
				return truncateText(cleaned, 150)
			}
		}
	}
	return "Plan is ready for review"
}

// generateReviewBody generates the body text for review_complete status (no actions appended).
// Matches bash: lib/summarizer.sh lines 494-521.
func generateReviewBody(messages []jsonl.Message) string {
	recentMessages := getRecentAssistantMessages(messages, ReviewMessagesWindow)
	texts := jsonl.ExtractTextFromMessages(recentMessages)
	combined := strings.Join(texts, " ")

	reviewKeywords := []string{"review", "анализ", "проверка", "analyzed", "analysis"}
	for _, keyword := range reviewKeywords {
		if strings.Contains(strings.ToLower(combined), keyword) {
			for _, text := range texts {
				if strings.Contains(strings.ToLower(text), keyword) {
					return truncateText(CleanMarkdown(text), 150)
				}
			}
		}
	}

	// Count Read tool usage
	readCount := 0
	for _, tool := range jsonl.ExtractTools(recentMessages) {
		if tool.Name == "Read" {
			readCount++
		}
	}
	if readCount > 0 {
		noun := "file"
		if readCount != 1 {
			noun = "files"
		}
		return fmt.Sprintf("Reviewed %d %s", readCount, noun)
	}

	return "Code review completed"
}

// generateTaskBody generates the body text for task_complete status (no actions appended).
// Matches bash: lib/summarizer.sh lines 523-653.
func generateTaskBody(messages []jsonl.Message, cfg *config.Config) string {
	recentMessages := getRecentAssistantMessages(messages, TaskMessagesWindow)
	if len(recentMessages) == 0 {
		return GetDefaultMessage(analyzer.StatusTaskComplete, cfg)
	}

	texts := jsonl.ExtractTextFromMessages(recentMessages)
	var lastMessage string
	if len(texts) > 0 {
		lastMessage = texts[len(texts)-1]
	}

	if lastMessage != "" {
		cleaned := CleanMarkdown(lastMessage)
		messageText := cleaned
		if len([]rune(cleaned)) >= 150 {
			messageText = extractFirstSentence(cleaned)
		}
		return truncateText(messageText, 150)
	}

	return "Task completed successfully"
}

// generateAPIErrorBody returns the body text for api_error (401 authentication) status.
func generateAPIErrorBody() string {
	return "Please run /login"
}

// generateAPIErrorOverloadedBody returns the body text for api_error_overloaded status.
// Extracts the actual error text from the API error message.
func generateAPIErrorOverloadedBody(messages []jsonl.Message) string {
	errorMessages := jsonl.GetLastApiErrorMessages(messages, 1)
	if len(errorMessages) > 0 {
		for _, text := range jsonl.ExtractTextFromMessages(errorMessages) {
			cleaned := strings.TrimSpace(CleanMarkdown(text))
			if cleaned != "" {
				return truncateText(cleaned, 150)
			}
		}
	}
	return "API error occurred"
}
