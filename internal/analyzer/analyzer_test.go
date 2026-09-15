package analyzer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/pkg/jsonl"
)

// === Test Helpers ===

// buildUserMessage creates a user message
func buildUserMessage(text string) jsonl.Message {
	return jsonl.Message{
		Type: "user",
		Message: jsonl.MessageContent{
			Role: "user",
			Content: []jsonl.Content{
				{Type: "text", Text: text},
			},
		},
		Timestamp: "2025-01-01T12:00:00Z",
	}
}

// buildAssistantWithTools creates an assistant message with tools and text
func buildAssistantWithTools(tools []string, text string) jsonl.Message {
	var content []jsonl.Content

	// Add tool uses
	for _, toolName := range tools {
		content = append(content, jsonl.Content{
			Type: "tool_use",
			Name: toolName,
			Input: map[string]interface{}{
				"file_path": "/test/file.go",
			},
		})
	}

	// Add text response
	content = append(content, jsonl.Content{
		Type: "text",
		Text: text,
	})

	return jsonl.Message{
		Type: "assistant",
		Message: jsonl.MessageContent{
			Role:    "assistant",
			Content: content,
		},
		Timestamp: "2025-01-01T12:00:01Z",
	}
}

// buildApiErrorMessage creates a synthetic API error message with isApiErrorMessage=true
// This matches the real Claude Code JSONL format for API errors
// errorCode: "unknown" for generic errors, "authentication_failed" for 401
func buildApiErrorMessage(errorText string, errorCode string) jsonl.Message {
	return jsonl.Message{
		Type:              "assistant",
		IsApiErrorMessage: true,
		Error:             errorCode,
		Message: jsonl.MessageContent{
			Role: "assistant",
			Content: []jsonl.Content{
				{Type: "text", Text: errorText},
			},
		},
		Timestamp: "2025-01-01T12:00:01Z",
	}
}

// buildTestMessages creates test messages from tool list and text length
func buildTestMessages(tools []string, textLength int) []jsonl.Message {
	// Generate text of specific length
	text := strings.Repeat("a", textLength)

	return []jsonl.Message{
		buildUserMessage("Test request"),
		buildAssistantWithTools(tools, text),
	}
}

// buildTranscriptFile creates a temporary JSONL file with test messages
func buildTranscriptFile(t *testing.T, messages []jsonl.Message) string {
	t.Helper()

	tmpDir := t.TempDir()
	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")

	f, err := os.Create(transcriptPath)
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer f.Close()

	// Write messages as JSONL
	encoder := json.NewEncoder(f)
	for _, msg := range messages {
		if err := encoder.Encode(msg); err != nil {
			t.Fatalf("failed to encode message: %v", err)
		}
	}

	return transcriptPath
}

// === Table-Driven Tests ===

func TestAnalyzeTranscript_ReviewComplete(t *testing.T) {
	tests := []struct {
		name        string
		tools       []string // Tool sequence
		textLength  int      // Response text length
		wantStatus  Status
		description string // Why this test exists
	}{
		// === Positive Cases: SHOULD be review_complete ===
		{
			name:        "single_read_with_long_analysis",
			tools:       []string{"Read"},
			textLength:  250,
			wantStatus:  StatusReviewComplete,
			description: "Single file review with detailed analysis",
		},
		{
			name:        "multiple_reads_with_analysis",
			tools:       []string{"Read", "Read", "Read"},
			textLength:  300,
			wantStatus:  StatusReviewComplete,
			description: "Multi-file code review",
		},
		{
			name:        "grep_search_with_findings",
			tools:       []string{"Grep", "Grep"},
			textLength:  220,
			wantStatus:  StatusReviewComplete,
			description: "Pattern search with analysis",
		},
		{
			name:        "glob_pattern_search",
			tools:       []string{"Glob", "Read"},
			textLength:  201,
			wantStatus:  StatusReviewComplete,
			description: "File discovery + review",
		},
		{
			name:        "mixed_read_like_tools",
			tools:       []string{"Read", "Grep", "Glob", "Read"},
			textLength:  500,
			wantStatus:  StatusReviewComplete,
			description: "Complex analysis with multiple tool types",
		},
		{
			name:        "boundary_exactly_201_chars",
			tools:       []string{"Read"},
			textLength:  201,
			wantStatus:  StatusReviewComplete,
			description: "Just above threshold (boundary test)",
		},
		{
			name:        "grep_only_with_long_text",
			tools:       []string{"Grep"},
			textLength:  300,
			wantStatus:  StatusReviewComplete,
			description: "Grep search with analysis",
		},
		{
			name:        "glob_only_with_long_text",
			tools:       []string{"Glob"},
			textLength:  250,
			wantStatus:  StatusReviewComplete,
			description: "Glob pattern with analysis",
		},

		// === Negative Cases: should NOT be review_complete ===
		{
			name:        "read_with_short_text",
			tools:       []string{"Read"},
			textLength:  199,
			wantStatus:  StatusTaskComplete,
			description: "Below 200 char threshold",
		},
		{
			name:        "read_with_exactly_200_chars",
			tools:       []string{"Read"},
			textLength:  200,
			wantStatus:  StatusTaskComplete,
			description: "Exactly at threshold (> not >=)",
		},
		{
			name:        "read_plus_write",
			tools:       []string{"Read", "Write"},
			textLength:  300,
			wantStatus:  StatusTaskComplete,
			description: "Has active tool - not review",
		},
		{
			name:        "read_plus_edit",
			tools:       []string{"Read", "Read", "Edit"},
			textLength:  250,
			wantStatus:  StatusTaskComplete,
			description: "Editing after reading - task complete",
		},
		{
			name:        "read_plus_bash",
			tools:       []string{"Read", "Bash"},
			textLength:  400,
			wantStatus:  StatusTaskComplete,
			description: "Running commands - not pure review",
		},
		{
			name:        "only_active_tools",
			tools:       []string{"Write", "Edit"},
			textLength:  300,
			wantStatus:  StatusTaskComplete,
			description: "No read-like tools",
		},
		{
			name:        "no_tools",
			tools:       []string{},
			textLength:  300,
			wantStatus:  StatusTaskComplete,
			description: "No tools used - text response (notifyOnTextResponse=true by default)",
		},
		{
			name:        "passive_tools_without_read_like",
			tools:       []string{"WebFetch", "WebSearch"},
			textLength:  250,
			wantStatus:  StatusTaskComplete,
			description: "Passive but not read-like tools",
		},
		{
			name:        "write_before_read",
			tools:       []string{"Write", "Read"},
			textLength:  300,
			wantStatus:  StatusTaskComplete,
			description: "Active tool present - not review",
		},
		{
			name:        "notebook_edit_with_read",
			tools:       []string{"Read", "NotebookEdit"},
			textLength:  250,
			wantStatus:  StatusTaskComplete,
			description: "NotebookEdit is active tool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build test transcript
			messages := buildTestMessages(tt.tools, tt.textLength)
			transcriptPath := buildTranscriptFile(t, messages)

			// Analyze
			cfg := &config.Config{}
			status, err := AnalyzeTranscript(transcriptPath, cfg)

			// Assert
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if status != tt.wantStatus {
				t.Errorf("got status %v, want %v (reason: %s)",
					status, tt.wantStatus, tt.description)
			}
		})
	}
}

// === Priority Tests (State Machine) ===

func TestAnalyzeTranscript_PriorityOrder(t *testing.T) {
	// Test that review_complete has correct priority in state machine
	tests := []struct {
		name       string
		tools      []string
		textLength int
		wantStatus Status
		reason     string
	}{
		{
			name:       "exit_plan_mode_beats_review",
			tools:      []string{"Read", "ExitPlanMode"},
			textLength: 300,
			wantStatus: StatusPlanReady,
			reason:     "ExitPlanMode (last tool) has higher priority",
		},
		{
			name:       "ask_user_question_beats_review",
			tools:      []string{"Read", "AskUserQuestion"},
			textLength: 300,
			wantStatus: StatusQuestion,
			reason:     "AskUserQuestion (last tool) has higher priority",
		},
		{
			name:       "exit_plan_with_concurrent_reads",
			tools:      []string{"Read", "ExitPlanMode", "Read"},
			textLength: 300,
			wantStatus: StatusReviewComplete,
			reason:     "All tools in same message (concurrent) - review takes precedence",
		},
		{
			name:       "review_beats_passive_fallback",
			tools:      []string{"Read", "Grep"},
			textLength: 250,
			wantStatus: StatusReviewComplete,
			reason:     "review_complete before general task_complete",
		},
		{
			name:       "active_tool_last_beats_review",
			tools:      []string{"Read", "Edit"},
			textLength: 300,
			wantStatus: StatusTaskComplete,
			reason:     "Active tool present - not review",
		},
		{
			name:       "read_then_slash_command",
			tools:      []string{"Read", "SlashCommand"},
			textLength: 300,
			wantStatus: StatusTaskComplete,
			reason:     "SlashCommand is active tool",
		},
		{
			name:       "read_then_kill_shell",
			tools:      []string{"Read", "KillShell"},
			textLength: 300,
			wantStatus: StatusTaskComplete,
			reason:     "KillShell is active tool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := buildTestMessages(tt.tools, tt.textLength)
			transcriptPath := buildTranscriptFile(t, messages)

			cfg := &config.Config{}
			status, err := AnalyzeTranscript(transcriptPath, cfg)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if status != tt.wantStatus {
				t.Errorf("got %v, want %v\nreason: %s",
					status, tt.wantStatus, tt.reason)
			}
		})
	}
}

// === Real-World Scenario Tests ===

func TestAnalyzeTranscript_RealWorldScenarios(t *testing.T) {
	tests := []struct {
		name        string
		scenario    func() []jsonl.Message
		wantStatus  Status
		description string
	}{
		{
			name: "code_review_session",
			scenario: func() []jsonl.Message {
				return []jsonl.Message{
					buildUserMessage("Review my auth module"),
					buildAssistantWithTools(
						[]string{"Read", "Read", "Grep"},
						"I've analyzed your authentication module. Here are 5 security issues I found:\n"+
							"1. Password hashing uses SHA256 instead of bcrypt\n"+
							"2. No rate limiting on login attempts\n"+
							"3. Session tokens don't expire\n"+
							"4. SQL injection vulnerability in username field\n"+
							"5. Missing CSRF protection",
					),
				}
			},
			wantStatus:  StatusReviewComplete,
			description: "Typical code review with multiple files",
		},
		{
			name: "quick_file_check",
			scenario: func() []jsonl.Message {
				return []jsonl.Message{
					buildUserMessage("Is this file correct?"),
					buildAssistantWithTools(
						[]string{"Read"},
						"Looks good!",
					),
				}
			},
			wantStatus:  StatusTaskComplete,
			description: "Short response - not a review",
		},
		{
			name: "security_audit",
			scenario: func() []jsonl.Message {
				return []jsonl.Message{
					buildUserMessage("Check for security issues"),
					buildAssistantWithTools(
						[]string{"Grep", "Grep", "Glob", "Read"},
						"I performed a security audit of your codebase. "+
							"Searched for common vulnerabilities across 50 files. "+
							"Found potential SQL injection in 3 locations, "+
							"XSS vulnerabilities in 2 components, and "+
							"hardcoded credentials in config files. "+
							"I recommend immediate fixes for the SQL injection issues.",
					),
				}
			},
			wantStatus:  StatusReviewComplete,
			description: "Security audit with grep patterns",
		},
		{
			name: "fix_after_review",
			scenario: func() []jsonl.Message {
				return []jsonl.Message{
					buildUserMessage("Fix the issues you found"),
					buildAssistantWithTools(
						[]string{"Read", "Edit", "Write", "Bash"},
						"I've fixed all 5 security issues. "+
							"Updated password hashing to bcrypt, "+
							"added rate limiting, implemented token expiration, "+
							"fixed SQL injection, and added CSRF protection. "+
							"All tests passing.",
					),
				}
			},
			wantStatus:  StatusTaskComplete,
			description: "Fixing code - has active tools",
		},
		{
			name: "architecture_review",
			scenario: func() []jsonl.Message {
				return []jsonl.Message{
					buildUserMessage("Review the overall architecture"),
					buildAssistantWithTools(
						[]string{"Glob", "Read", "Read", "Read"},
						"I've reviewed your microservices architecture across 15 services. "+
							"The overall design follows SOLID principles well. "+
							"Each service has clear boundaries and responsibilities. "+
							"However, I noticed some areas for improvement: "+
							"1) Service A and B share too much coupling through the message queue "+
							"2) Missing circuit breakers in external API calls "+
							"3) Database connection pooling could be optimized. "+
							"Overall, it's a solid architecture with room for minor improvements.",
					),
				}
			},
			wantStatus:  StatusReviewComplete,
			description: "Architecture review with multiple files",
		},
		{
			name: "grep_pattern_analysis",
			scenario: func() []jsonl.Message {
				return []jsonl.Message{
					buildUserMessage("Find all TODO comments"),
					buildAssistantWithTools(
						[]string{"Grep"},
						"I found 23 TODO comments across your codebase. "+
							"Most are in the authentication module (8 TODOs), "+
							"followed by the payment service (6 TODOs). "+
							"The oldest TODO is from 6 months ago about database migration. "+
							"I recommend prioritizing the security-related TODOs in auth first.",
					),
				}
			},
			wantStatus:  StatusReviewComplete,
			description: "Grep analysis with long report",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := tt.scenario()
			transcriptPath := buildTranscriptFile(t, messages)

			cfg := &config.Config{}
			status, err := AnalyzeTranscript(transcriptPath, cfg)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if status != tt.wantStatus {
				t.Errorf("%s: got %v, want %v",
					tt.description, status, tt.wantStatus)
			}
		})
	}
}

// === Edge Cases ===

func TestAnalyzeTranscript_EdgeCases(t *testing.T) {
	t.Run("empty_transcript", func(t *testing.T) {
		messages := []jsonl.Message{}
		transcriptPath := buildTranscriptFile(t, messages)

		cfg := &config.Config{}
		status, err := AnalyzeTranscript(transcriptPath, cfg)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != StatusUnknown {
			t.Errorf("got %v, want StatusUnknown for empty transcript", status)
		}
	})

	t.Run("only_user_messages", func(t *testing.T) {
		messages := []jsonl.Message{
			buildUserMessage("Hello"),
			buildUserMessage("Are you there?"),
		}
		transcriptPath := buildTranscriptFile(t, messages)

		cfg := &config.Config{}
		status, err := AnalyzeTranscript(transcriptPath, cfg)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != StatusUnknown {
			t.Errorf("got %v, want StatusUnknown for user-only messages", status)
		}
	})

	t.Run("assistant_message_without_tools_default", func(t *testing.T) {
		messages := []jsonl.Message{
			buildUserMessage("Hello"),
			buildAssistantWithTools([]string{}, "I understand your request."),
		}
		transcriptPath := buildTranscriptFile(t, messages)

		cfg := &config.Config{} // Default: notifyOnTextResponse=true
		status, err := AnalyzeTranscript(transcriptPath, cfg)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != StatusTaskComplete {
			t.Errorf("got %v, want StatusTaskComplete for text-only response (default)", status)
		}
	})

	t.Run("assistant_message_without_tools_disabled", func(t *testing.T) {
		messages := []jsonl.Message{
			buildUserMessage("Hello"),
			buildAssistantWithTools([]string{}, "I understand your request."),
		}
		transcriptPath := buildTranscriptFile(t, messages)

		notifyOnText := false
		cfg := &config.Config{
			Notifications: config.NotificationsConfig{
				NotifyOnTextResponse: &notifyOnText,
			},
		}
		status, err := AnalyzeTranscript(transcriptPath, cfg)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != StatusUnknown {
			t.Errorf("got %v, want StatusUnknown when notifyOnTextResponse=false", status)
		}
	})

	t.Run("exactly_threshold_boundary", func(t *testing.T) {
		// Test both 200 (not review) and 201 (review)
		for _, tc := range []struct {
			length int
			want   Status
		}{
			{200, StatusTaskComplete},   // Not review
			{201, StatusReviewComplete}, // Review
		} {
			messages := buildTestMessages([]string{"Read"}, tc.length)
			transcriptPath := buildTranscriptFile(t, messages)

			cfg := &config.Config{}
			status, err := AnalyzeTranscript(transcriptPath, cfg)

			if err != nil {
				t.Fatalf("textLength=%d: unexpected error: %v", tc.length, err)
			}
			if status != tc.want {
				t.Errorf("textLength=%d: got %v, want %v", tc.length, status, tc.want)
			}
		}
	})
}

// === Unit Tests for Helper Functions ===

func TestGetStatusForPreToolUse(t *testing.T) {
	tests := []struct {
		toolName string
		expected Status
	}{
		{"ExitPlanMode", StatusPlanReady},
		{"AskUserQuestion", StatusQuestion},
		{"Write", StatusUnknown},
		{"", StatusUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			status := GetStatusForPreToolUse(tt.toolName)
			if status != tt.expected {
				t.Errorf("got %v, want %v", status, tt.expected)
			}
		})
	}
}

func TestAnalyzeTranscript_SessionLimitReached(t *testing.T) {
	cfg := &config.Config{}

	t.Run("session_limit_in_text", func(t *testing.T) {
		messages := []jsonl.Message{
			buildUserMessage("Continue working"),
			buildAssistantWithTools([]string{"Read"}, "Session limit reached. Please start a new conversation."),
		}
		transcriptPath := buildTranscriptFile(t, messages)

		status, err := AnalyzeTranscript(transcriptPath, cfg)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != StatusSessionLimitReached {
			t.Errorf("got %v, want StatusSessionLimitReached", status)
		}
	})

	t.Run("session_limit_case_insensitive", func(t *testing.T) {
		messages := []jsonl.Message{
			buildUserMessage("Continue working"),
			buildAssistantWithTools([]string{}, "SESSION LIMIT REACHED"),
		}
		transcriptPath := buildTranscriptFile(t, messages)

		status, err := AnalyzeTranscript(transcriptPath, cfg)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != StatusSessionLimitReached {
			t.Errorf("got %v, want StatusSessionLimitReached for case-insensitive match", status)
		}
	})

	t.Run("session_limit_has_been_reached", func(t *testing.T) {
		messages := []jsonl.Message{
			buildUserMessage("Continue working"),
			buildAssistantWithTools([]string{}, "The session limit has been reached. Please start a new conversation."),
		}
		transcriptPath := buildTranscriptFile(t, messages)

		status, err := AnalyzeTranscript(transcriptPath, cfg)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != StatusSessionLimitReached {
			t.Errorf("got %v, want StatusSessionLimitReached for alternate phrasing", status)
		}
	})

	t.Run("no_session_limit_text", func(t *testing.T) {
		messages := []jsonl.Message{
			buildUserMessage("Hello"),
			buildAssistantWithTools([]string{"Write"}, "Task completed successfully"),
		}
		transcriptPath := buildTranscriptFile(t, messages)

		status, err := AnalyzeTranscript(transcriptPath, cfg)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status == StatusSessionLimitReached {
			t.Errorf("got StatusSessionLimitReached, expected different status")
		}
	})
}

func TestAnalyzeTranscript_APIError(t *testing.T) {
	cfg := &config.Config{}

	t.Run("api_error_401_via_error_field", func(t *testing.T) {
		// Real format: error="authentication_failed" — primary detection path
		messages := []jsonl.Message{
			buildUserMessage("Continue working"),
			buildApiErrorMessage(
				`API Error: 401 {"type":"error","error":{"type":"authentication_error","message":"OAuth token has expired"}} · Please run /login`,
				"authentication_failed",
			),
		}
		transcriptPath := buildTranscriptFile(t, messages)

		status, err := AnalyzeTranscript(transcriptPath, cfg)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != StatusAPIError {
			t.Errorf("got %v, want StatusAPIError", status)
		}
	})

	t.Run("api_error_401_via_text_fallback", func(t *testing.T) {
		// Fallback: error="unknown" but text contains 401 + authentication_error
		messages := []jsonl.Message{
			buildUserMessage("Continue working"),
			buildApiErrorMessage(
				`API Error: 401 {"type":"error","error":{"type":"authentication_error","message":"OAuth token has expired"}} · Please run /login`,
				"unknown",
			),
		}
		transcriptPath := buildTranscriptFile(t, messages)

		status, err := AnalyzeTranscript(transcriptPath, cfg)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != StatusAPIError {
			t.Errorf("got %v, want StatusAPIError (text fallback)", status)
		}
	})

	t.Run("api_error_connection_error", func(t *testing.T) {
		// Real format from logs: "API Error: Connection error."
		messages := []jsonl.Message{
			buildUserMessage("Do something"),
			buildApiErrorMessage("API Error: Connection error.", "unknown"),
		}
		transcriptPath := buildTranscriptFile(t, messages)

		status, err := AnalyzeTranscript(transcriptPath, cfg)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != StatusAPIErrorOverloaded {
			t.Errorf("got %v, want StatusAPIErrorOverloaded", status)
		}
	})

	t.Run("api_error_400_content_filter", func(t *testing.T) {
		messages := []jsonl.Message{
			buildUserMessage("Do something"),
			buildApiErrorMessage(
				`API Error: 400 {"type":"error","error":{"type":"invalid_request_error","message":"Output blocked by content filtering policy"}}`,
				"unknown",
			),
		}
		transcriptPath := buildTranscriptFile(t, messages)

		status, err := AnalyzeTranscript(transcriptPath, cfg)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != StatusAPIErrorOverloaded {
			t.Errorf("got %v, want StatusAPIErrorOverloaded", status)
		}
	})

	t.Run("api_error_529_overloaded", func(t *testing.T) {
		messages := []jsonl.Message{
			buildUserMessage("Do something"),
			buildApiErrorMessage(
				`API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
				"unknown",
			),
		}
		transcriptPath := buildTranscriptFile(t, messages)

		status, err := AnalyzeTranscript(transcriptPath, cfg)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != StatusAPIErrorOverloaded {
			t.Errorf("got %v, want StatusAPIErrorOverloaded", status)
		}
	})

	t.Run("api_error_500_server_error", func(t *testing.T) {
		messages := []jsonl.Message{
			buildUserMessage("Do something"),
			buildApiErrorMessage(
				`API Error: 500 {"type":"error","error":{"type":"internal_server_error","message":"Internal server error"}}`,
				"unknown",
			),
		}
		transcriptPath := buildTranscriptFile(t, messages)

		status, err := AnalyzeTranscript(transcriptPath, cfg)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != StatusAPIErrorOverloaded {
			t.Errorf("got %v, want StatusAPIErrorOverloaded", status)
		}
	})

	t.Run("api_error_429_rate_limit", func(t *testing.T) {
		messages := []jsonl.Message{
			buildUserMessage("Do something"),
			buildApiErrorMessage(
				`API Error: 429 {"type":"error","error":{"type":"rate_limit_error","message":"Rate limit exceeded"}}`,
				"unknown",
			),
		}
		transcriptPath := buildTranscriptFile(t, messages)

		status, err := AnalyzeTranscript(transcriptPath, cfg)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != StatusAPIErrorOverloaded {
			t.Errorf("got %v, want StatusAPIErrorOverloaded", status)
		}
	})

	t.Run("no_flag_no_detection", func(t *testing.T) {
		// Regular assistant message talking about API errors should NOT trigger
		messages := []jsonl.Message{
			buildUserMessage("Hello"),
			buildAssistantWithTools([]string{"Write"}, "I fixed the API Error 500 handling in your code"),
		}
		transcriptPath := buildTranscriptFile(t, messages)

		status, err := AnalyzeTranscript(transcriptPath, cfg)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status == StatusAPIError || status == StatusAPIErrorOverloaded {
			t.Errorf("got %v, should not detect API error without isApiErrorMessage flag", status)
		}
	})

	t.Run("no_api_error", func(t *testing.T) {
		messages := []jsonl.Message{
			buildUserMessage("Hello"),
			buildAssistantWithTools([]string{"Write"}, "Task completed successfully"),
		}
		transcriptPath := buildTranscriptFile(t, messages)

		status, err := AnalyzeTranscript(transcriptPath, cfg)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status == StatusAPIError || status == StatusAPIErrorOverloaded {
			t.Errorf("got %v, expected different status", status)
		}
	})
}

func TestContains(t *testing.T) {
	slice := []string{"apple", "banana", "cherry"}

	if !contains(slice, "banana") {
		t.Error("expected contains to find 'banana'")
	}
	if contains(slice, "orange") {
		t.Error("expected contains not to find 'orange'")
	}
	if contains([]string{}, "test") {
		t.Error("expected contains not to find anything in empty slice")
	}
}
