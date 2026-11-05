package summary

import (
	"regexp"
	"strings"
	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/pkg/jsonl"
)

const (
	// Message window sizes for different notification types
	// These determine how many recent assistant messages to analyze
	QuestionMessagesWindow = 8 // Based on bash version, good balance for question detection
	ReviewMessagesWindow   = 5 // Smaller window for focused review summaries
	TaskMessagesWindow     = 5 // Smaller window for task completion summaries
)

var (
	// Regex patterns for markdown cleanup
	headerPattern     = regexp.MustCompile(`^#+\s*`)
	bulletPattern     = regexp.MustCompile(`^[-*•]\s*`)
	backtickPattern   = regexp.MustCompile("`")
	multiSpacePattern = regexp.MustCompile(`\s+`)
	emojiPattern      = regexp.MustCompile(`^[\p{So}\p{Sk}]+\s*`)

	// Extended markdown patterns for full cleanup
	codeBlockPattern     = regexp.MustCompile("```[\\s\\S]*?```")        // Code blocks
	linkPattern          = regexp.MustCompile(`\[([^\]]+)\]\([^\)]+\)`)  // [text](url) -> text
	imagePattern         = regexp.MustCompile(`!\[([^\]]*)\]\([^\)]+\)`) // ![alt](url) -> alt
	boldPattern          = regexp.MustCompile(`(\*\*|__)(.+?)(\*\*|__)`) // **text** or __text__
	italicPattern        = regexp.MustCompile(`(\*|_)([^*_]+)(\*|_)`)    // *text* or _text_
	strikethroughPattern = regexp.MustCompile(`~~(.+?)~~`)               // ~~text~~
	blockquotePattern    = regexp.MustCompile(`^>\s*`)                   // > quote
)

// getRecentAssistantMessages safely extracts recent assistant messages from current response
// Filters by last user timestamp to ensure we only get messages from the CURRENT response,
// not from previous user requests. Falls back to last N messages if filtering fails.
func getRecentAssistantMessages(messages []jsonl.Message, limit int) []jsonl.Message {
	// Filter by user timestamp (current response only)
	userTS := jsonl.GetLastUserTimestamp(messages)
	filteredMessages := jsonl.FilterMessagesAfterTimestamp(messages, userTS)

	// If filtered result is not empty, use it (limited to window size)
	if len(filteredMessages) > 0 {
		if len(filteredMessages) > limit {
			return filteredMessages[len(filteredMessages)-limit:]
		}
		return filteredMessages
	}

	// Fallback: last N messages (for backward compatibility and edge cases)
	return jsonl.GetLastAssistantMessages(messages, limit)
}

// GenerateFromMessages generates a status-specific summary from already-parsed messages.
// This avoids re-reading the transcript file when messages are already available.
//
// Returns the joined "<body> <actions>" string for backward compatibility. New
// callers that want body and actions separately should use GenerateFromMessagesStructured.
func GenerateFromMessages(messages []jsonl.Message, status analyzer.Status, cfg *config.Config) string {
	body, actions := GenerateFromMessagesStructured(messages, status, cfg)
	return appendActions(body, actions)
}

// GenerateFromMessagesStructured returns the summary body and the action summary
// (e.g. "📝 1 new  ▶ 2 cmds  ⏱ 41s") as separate strings. Either may be empty.
//
// Webhook formatters that render structured layouts (Discord embed fields) use
// this to avoid re-parsing the joined output.
func GenerateFromMessagesStructured(messages []jsonl.Message, status analyzer.Status, cfg *config.Config) (body, actions string) {
	if len(messages) == 0 {
		return GetDefaultMessage(status, cfg), ""
	}

	switch status {
	case analyzer.StatusAPIError:
		return generateAPIErrorBody(), ""
	case analyzer.StatusAPIErrorOverloaded:
		return generateAPIErrorOverloadedBody(messages), ""
	}

	actions = getActionsString(messages)
	switch status {
	case analyzer.StatusQuestion:
		body = generateQuestionBody(messages)
	case analyzer.StatusPlanReady:
		body = generatePlanBody(messages)
	case analyzer.StatusReviewComplete:
		body = generateReviewBody(messages)
	case analyzer.StatusTaskComplete:
		body = generateTaskBody(messages, cfg)
	case analyzer.StatusSessionLimitReached:
		body = "Session limit reached. Please start a new conversation."
	default:
		body = generateTaskBody(messages, cfg)
	}
	return body, actions
}

// GenerateFromTranscript generates a status-specific summary from transcript
func GenerateFromTranscript(transcriptPath string, status analyzer.Status, cfg *config.Config) string {
	messages, err := jsonl.ParseFile(transcriptPath)
	if err != nil {
		return GetDefaultMessage(status, cfg)
	}
	return GenerateFromMessages(messages, status, cfg)
}

// GetDefaultMessage returns a default message for a status
func GetDefaultMessage(status analyzer.Status, cfg *config.Config) string {
	statusInfo, exists := cfg.GetStatusInfo(string(status))
	if !exists {
		return "Claude notification"
	}

	// Remove emoji from title for message
	title := statusInfo.Title
	title = strings.TrimSpace(emojiPattern.ReplaceAllString(title, ""))

	return title
}

// GenerateSimple generates a simple message based on status
func GenerateSimple(status analyzer.Status, cfg *config.Config) string {
	return GetDefaultMessage(status, cfg)
}
