package summary

import (
	"fmt"
	"regexp"
	"strings"
	"time"

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

// calculateDuration calculates duration between last user and last assistant messages
func calculateDuration(messages []jsonl.Message) string {
	userTS := jsonl.GetLastUserTimestamp(messages)
	assistantTS := jsonl.GetLastAssistantTimestamp(messages)

	if userTS == "" || assistantTS == "" {
		return ""
	}

	userTime, err1 := time.Parse(time.RFC3339, userTS)
	assistantTime, err2 := time.Parse(time.RFC3339, assistantTS)

	if err1 != nil || err2 != nil {
		return ""
	}

	duration := assistantTime.Sub(userTime)
	if duration < 0 {
		return ""
	}

	return formatDuration(duration)
}

// formatDuration formats duration into human-readable string
func formatDuration(d time.Duration) string {
	seconds := int(d.Seconds())

	if seconds < 60 {
		return fmt.Sprintf("⏱ %ds", seconds)
	}

	minutes := seconds / 60
	secs := seconds % 60

	if minutes < 60 {
		if secs > 0 {
			return fmt.Sprintf("⏱ %dm %ds", minutes, secs)
		}
		return fmt.Sprintf("⏱ %dm", minutes)
	}

	hours := minutes / 60
	mins := minutes % 60

	if mins > 0 {
		return fmt.Sprintf("⏱ %dh %dm", hours, mins)
	}
	return fmt.Sprintf("⏱ %dh", hours)
}

// countToolsByType counts tools since last user message
func countToolsByType(messages []jsonl.Message) map[string]int {
	counts := make(map[string]int)

	// Find last user timestamp
	userTS := jsonl.GetLastUserTimestamp(messages)
	var sinceTime time.Time
	if userTS != "" {
		if t, err := time.Parse(time.RFC3339, userTS); err == nil {
			sinceTime = t
		}
	}

	// Count tools after user message
	for _, msg := range messages {
		if msg.Type != "assistant" {
			continue
		}

		// Check if this message is after user message
		if !sinceTime.IsZero() && msg.Timestamp != "" {
			if msgTime, err := time.Parse(time.RFC3339, msg.Timestamp); err == nil {
				if msgTime.Before(sinceTime) {
					continue
				}
			}
		}

		for _, content := range msg.Message.Content {
			if content.Type == "tool_use" {
				counts[content.Name]++
			}
		}
	}

	return counts
}

// getActionsString calculates duration, counts tools, and returns formatted actions string
func getActionsString(messages []jsonl.Message) string {
	return buildActionsString(countToolsByType(messages), calculateDuration(messages))
}

// appendActions appends actions suffix to message if non-empty
func appendActions(message, actions string) string {
	if actions == "" {
		return message
	}
	return message + " " + actions
}

// buildActionsString builds actions summary with tool counts and duration
func buildActionsString(toolCounts map[string]int, duration string) string {
	var parts []string

	// Write
	if count := toolCounts["Write"]; count > 0 {
		parts = append(parts, fmt.Sprintf("📝 %d new", count))
	}

	// Edit
	if count := toolCounts["Edit"]; count > 0 {
		parts = append(parts, fmt.Sprintf("✏️ %d edited", count))
	}

	// Bash
	if count := toolCounts["Bash"]; count > 0 {
		parts = append(parts, fmt.Sprintf("▶ %d cmds", count))
	}

	// Add duration at the end
	if duration != "" {
		parts = append(parts, duration)
	}

	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, "  ")
}

// Helper functions

func extractFirstSentence(text string) string {
	// Find first sentence (ending with . ! or ?)
	// If first sentence is too short (< 20 chars), try to include second sentence too
	const minSentenceLength = 20
	const maxLength = 200

	var sentences []string
	var currentStart int
	runes := []rune(text)

	for i, char := range runes {
		if char == '.' || char == '!' || char == '?' {
			// For dots, check if this is really end of sentence:
			// - Must be followed by space + uppercase letter, or end of string
			// - Should not be preceded by a digit (to avoid "v1.6.0")
			if char == '.' {
				// Check if preceded by digit (version numbers like v1.6.0)
				if i > 0 && runes[i-1] >= '0' && runes[i-1] <= '9' {
					continue
				}
				// Check if followed by digit (decimal numbers like 1.5)
				if i+1 < len(runes) && runes[i+1] >= '0' && runes[i+1] <= '9' {
					continue
				}
				// Check if followed by letter without space (abbreviations, domains)
				if i+1 < len(runes) && runes[i+1] != ' ' && runes[i+1] != '\n' {
					continue
				}
			}

			// Include punctuation in the sentence
			sentence := strings.TrimSpace(string(runes[currentStart : i+1]))
			if sentence != "" {
				sentences = append(sentences, sentence)
				currentStart = i + 1

				// Calculate total length so far
				totalLength := len(strings.Join(sentences, " "))

				// If we have at least one sentence and either:
				// 1. Total length >= minSentenceLength, OR
				// 2. Total length >= maxLength
				// Then return what we have
				if len(sentences) == 1 && totalLength < minSentenceLength && totalLength < maxLength {
					// First sentence too short, continue to get second
					continue
				}

				if totalLength >= maxLength {
					// Too long, return what we had before last sentence
					if len(sentences) > 1 {
						return strings.Join(sentences[:len(sentences)-1], " ")
					}
				}

				// Good length, return
				return strings.Join(sentences, " ")
			}
		}
	}

	// No sentence ending found
	if len(sentences) > 0 {
		return strings.Join(sentences, " ")
	}

	// Return first 100 chars if no punctuation found
	if len(runes) > 100 {
		return string(runes[:100])
	}
	return text
}

func truncateText(text string, maxLen int) string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return text
	}

	// Step 1: Try to find sentence boundary (., !, ?) within maxLen
	// Look for the last sentence-ending punctuation in the allowed range
	// Use runes to avoid cutting in the middle of a multi-byte character
	searchText := string(runes[:maxLen])

	// Check for sentence enders: ". ", "! ", "? " (followed by space or newline)
	// Also check for end of string within maxLen
	lastSentenceEnd := -1

	// Try sentence endings with space/newline after
	// Find the FIRST suitable sentence ending (to avoid partial next sentences)
	sentenceEnders := []string{". ", "! ", "? ", ".\n", "!\n", "?\n"}
	for _, ender := range sentenceEnders {
		idx := 0
		for {
			pos := strings.Index(searchText[idx:], ender)
			if pos < 0 {
				break
			}
			actualPos := idx + pos
			// Check if this position is suitable: not too early
			if actualPos > maxLen/3 {
				// Found a suitable sentence ending
				// Only use it if we haven't found one yet, or this is a better one
				// (we want the FIRST suitable one, not the last)
				if lastSentenceEnd < 0 {
					lastSentenceEnd = actualPos
					break // Stop searching for this ender
				}
			}
			idx = actualPos + 1
		}
		if lastSentenceEnd >= 0 {
			break // Found a suitable sentence, no need to check other enders
		}
	}

	// Also try sentence ending at the very end of searchText (no space after)
	if lastSentenceEnd < 0 && len(searchText) > 0 {
		lastChar := searchText[len(searchText)-1]
		if lastChar == '.' || lastChar == '!' || lastChar == '?' {
			lastSentenceEnd = len(searchText) - 1
		}
	}

	if lastSentenceEnd >= 0 {
		// Found a sentence boundary, truncate there (including the punctuation)
		return strings.TrimSpace(searchText[:lastSentenceEnd+1])
	}

	// Step 2: No sentence boundary found, try word boundary
	// Still use runes to be safe
	truncatedRunes := runes[:maxLen-3]
	truncated := string(truncatedRunes)
	lastSpace := strings.LastIndex(truncated, " ")
	if lastSpace > maxLen/2 {
		truncated = truncated[:lastSpace]
	}

	return truncated + "..."
}

// CleanMarkdown cleans markdown formatting from text
// Removes all markdown syntax while preserving the actual text content
func CleanMarkdown(text string) string {
	// Step 1: Remove code blocks first (they can contain markdown-like syntax)
	text = codeBlockPattern.ReplaceAllString(text, "")

	// Step 2: Convert images to alt text (must be before links since images are ![](url))
	text = imagePattern.ReplaceAllString(text, "$1")

	// Step 3: Convert links to text only
	text = linkPattern.ReplaceAllString(text, "$1")

	// Step 4: Remove strikethrough
	text = strikethroughPattern.ReplaceAllString(text, "$1")

	// Step 5: Remove bold (both ** and __)
	text = boldPattern.ReplaceAllString(text, "$2")

	// Step 6: Remove italic (both * and _)
	// Need to be careful with edge cases
	text = italicPattern.ReplaceAllString(text, "$2")

	// Step 7: Remove backticks (inline code)
	text = backtickPattern.ReplaceAllString(text, "")

	// Step 8: Process line by line for line-based patterns
	lines := strings.Split(text, "\n")
	var cleaned []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Remove headers (# text)
		line = headerPattern.ReplaceAllString(line, "")

		// Remove blockquotes (> text)
		line = blockquotePattern.ReplaceAllString(line, "")

		// Remove bullet points (- text, * text, • text)
		line = bulletPattern.ReplaceAllString(line, "")

		// Trim again
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}

	// Step 9: Join lines and normalize whitespace
	result := strings.Join(cleaned, " ")
	result = multiSpacePattern.ReplaceAllString(result, " ")

	return strings.TrimSpace(result)
}

// GetDefaultMessage returns a default message for a status
func GetDefaultMessage(status analyzer.Status, cfg *config.Config) string {
	statusInfo, exists := cfg.GetStatusInfo(string(status))
	if !exists {
		return "Claude Code notification"
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
