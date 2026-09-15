package analyzer

import (
	"strings"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/pkg/jsonl"
)

// Tool categories for state machine classification
//
// TODO: Future improvement - detect passive Bash commands
// Currently all Bash commands are treated as "active" (code-changing).
// Could be improved by parsing command strings to differentiate:
//   - Passive: ls, cd, pwd, git status, git log, git diff, find, grep
//   - Active: mkdir, rm, mv, cp, git commit, npm install, etc.
//
// This requires:
//  1. Storing tool Input in ToolUse struct (pkg/jsonl)
//  2. Parsing command string from Input["command"]
//  3. Handling complex cases: pipes (|), redirects (>), chains (&&)
//
// Complexity: Medium-High. Edge cases are tricky (e.g. "cat file > output").
var (
	ActiveTools   = []string{"Write", "Edit", "Bash", "NotebookEdit", "SlashCommand", "KillShell"}
	QuestionTools = []string{"AskUserQuestion"}
	PlanningTools = []string{"ExitPlanMode", "TodoWrite"}
	PassiveTools  = []string{"Read", "Grep", "Glob", "WebFetch", "WebSearch", "Search", "Fetch", "Task"}
)

// Status represents the current task status
type Status string

const (
	StatusTaskComplete        Status = "task_complete"
	StatusReviewComplete      Status = "review_complete"
	StatusQuestion            Status = "question"
	StatusPlanReady           Status = "plan_ready"
	StatusSessionLimitReached Status = "session_limit_reached"
	StatusAPIError            Status = "api_error"
	StatusAPIErrorOverloaded  Status = "api_error_overloaded"
	StatusPermissionRequest   Status = "permission_request"
	StatusUnknown             Status = "unknown"
)

// AnalyzeTranscript analyzes a transcript file and determines the current status
func AnalyzeTranscript(transcriptPath string, cfg *config.Config) (Status, error) {
	status, _, err := AnalyzeTranscriptWithMessages(transcriptPath, cfg)
	return status, err
}

// AnalyzeTranscriptWithMessages analyzes a transcript and also returns the parsed messages.
// This allows callers to reuse the messages (e.g., for summary generation) without re-reading the file.
func AnalyzeTranscriptWithMessages(transcriptPath string, cfg *config.Config) (Status, []jsonl.Message, error) {
	// Parse JSONL file
	messages, err := jsonl.ParseFile(transcriptPath)
	if err != nil {
		return StatusUnknown, nil, err
	}

	// PRIORITY CHECK 1: Session limit reached
	// This takes precedence over all other status detection
	if detectSessionLimitReached(messages) {
		return StatusSessionLimitReached, messages, nil
	}

	// PRIORITY CHECK 2: API errors (uses isApiErrorMessage flag from JSONL)
	if apiStatus := detectAPIErrors(messages); apiStatus != StatusUnknown {
		return apiStatus, messages, nil
	}

	// Find last user message timestamp
	// This ensures we only analyze tools from the CURRENT response,
	// not from previous user requests (avoids "ghost" ExitPlanMode problem)
	userTS := jsonl.GetLastUserTimestamp(messages)

	// Filter assistant messages AFTER last user message
	filteredMessages := jsonl.FilterMessagesAfterTimestamp(messages, userTS)

	if len(filteredMessages) == 0 {
		return StatusUnknown, messages, nil
	}

	// Take last 15 messages (temporal window) from filtered set
	recentMessages := filteredMessages
	if len(filteredMessages) > 15 {
		recentMessages = filteredMessages[len(filteredMessages)-15:]
	}

	// Extract tools with positions
	tools := jsonl.ExtractTools(recentMessages)

	// STATE MACHINE LOGIC - tool-based detection only

	// 1. If we have tools, analyze them
	if len(tools) > 0 {
		lastTool := jsonl.GetLastTool(tools)

		// 1a. Last tool is ExitPlanMode → plan just created
		if lastTool == "ExitPlanMode" {
			return StatusPlanReady, messages, nil
		}

		// 1b. Last tool is AskUserQuestion → waiting for user
		if lastTool == "AskUserQuestion" {
			return StatusQuestion, messages, nil
		}

		// 1c. ExitPlanMode exists AND tools after it → plan executed
		exitPlanPos := jsonl.FindToolPosition(tools, "ExitPlanMode")
		if exitPlanPos >= 0 {
			toolsAfter := jsonl.CountToolsAfterPosition(tools, exitPlanPos)
			if toolsAfter > 0 {
				return StatusTaskComplete, messages, nil
			}
		}

		// 1d. Review detection: only read-like tools + long text response
		// Read-like tools: Read, Grep, Glob (searching/analyzing code)
		// No active tools: no Write, Edit, Bash, etc.
		// Long text: >200 chars (indicates substantial analysis/review)
		readLikeTools := []string{"Read", "Grep", "Glob"}
		readLikeCount := jsonl.CountToolsByNames(tools, readLikeTools)
		hasActiveTools := jsonl.HasAnyActiveTool(tools, ActiveTools)

		if readLikeCount >= 1 && !hasActiveTools {
			// Extract recent text to check length
			recentText := jsonl.ExtractRecentText(recentMessages, 5)

			if len(recentText) > 200 {
				return StatusReviewComplete, messages, nil
			}
		}

		// 1e. Last tool is active (Write/Edit/Bash) → work completed
		if contains(ActiveTools, lastTool) {
			return StatusTaskComplete, messages, nil
		}

		// 1f. Any tool usage at all → likely task completed
		// (matches bash version: toolCount >= 1 → task_complete)
		return StatusTaskComplete, messages, nil
	}

	// 2. No tools found
	// If notifyOnTextResponse is enabled (default: true), treat as task_complete
	// This handles cases like extended thinking where Claude responds with text only
	if cfg.ShouldNotifyOnTextResponse() {
		return StatusTaskComplete, messages, nil
	}

	return StatusUnknown, messages, nil
}

// contains checks if a slice contains a string
func contains(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}

// GetStatusForPreToolUse determines status for PreToolUse hook
// This is called BEFORE tool execution, so we only have the tool name
func GetStatusForPreToolUse(toolName string) Status {
	if toolName == "ExitPlanMode" {
		return StatusPlanReady
	}
	if toolName == "AskUserQuestion" {
		return StatusQuestion
	}
	return StatusUnknown
}

// detectSessionLimitReached checks if the last assistant messages contain "Session limit reached"
func detectSessionLimitReached(messages []jsonl.Message) bool {
	// Check last 3 assistant messages for the session limit text
	recentMessages := jsonl.GetLastAssistantMessages(messages, 3)
	if len(recentMessages) == 0 {
		return false
	}

	// Extract text from recent messages
	texts := jsonl.ExtractTextFromMessages(recentMessages)

	// Check each text for the session limit phrase
	for _, text := range texts {
		if containsIgnoreCase(text, "Session limit reached") ||
			containsIgnoreCase(text, "session limit has been reached") {
			return true
		}
	}

	return false
}

// detectAPIErrors checks for API errors using the isApiErrorMessage flag in JSONL.
// Claude Code sets isApiErrorMessage=true on synthetic assistant messages
// when the API returns an error (400, 401, 429, 500, 529, etc).
// Returns the specific error status or StatusUnknown if no API error found.
func detectAPIErrors(messages []jsonl.Message) Status {
	// Check if there are any recent API error messages (after last user message)
	if !jsonl.HasRecentApiError(messages) {
		return StatusUnknown
	}

	// Get the last API error messages to determine the type
	errorMessages := jsonl.GetLastApiErrorMessages(messages, 3)
	if len(errorMessages) == 0 {
		return StatusUnknown
	}

	// Check the last error message to determine the type
	lastError := errorMessages[len(errorMessages)-1]

	// Primary check: use the structured "error" field from JSONL
	// Real values: "authentication_failed" for 401, "unknown" for others
	if lastError.Error == "authentication_failed" {
		return StatusAPIError
	}

	// Fallback: check text content for 401 indicators
	// (in case Claude Code changes the error field format)
	texts := jsonl.ExtractTextFromMessages([]jsonl.Message{lastError})
	for _, text := range texts {
		if containsIgnoreCase(text, "401") &&
			(containsIgnoreCase(text, "authentication_error") ||
				containsIgnoreCase(text, "run /login")) {
			return StatusAPIError
		}
	}

	// Any other API error (400, 429, 500, 529, connection error, etc.)
	return StatusAPIErrorOverloaded
}

// containsIgnoreCase checks if string contains substring (case insensitive)
func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
