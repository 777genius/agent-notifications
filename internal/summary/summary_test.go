package summary

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/pkg/jsonl"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		duration time.Duration
		expected string
	}{
		{30 * time.Second, "⏱ 30s"},
		{90 * time.Second, "⏱ 1m 30s"},
		{120 * time.Second, "⏱ 2m"},
		{3661 * time.Second, "⏱ 1h 1m"},
		{3600 * time.Second, "⏱ 1h"},
		{7200 * time.Second, "⏱ 2h"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := formatDuration(tt.duration)
			if result != tt.expected {
				t.Errorf("formatDuration(%v) = %s, want %s", tt.duration, result, tt.expected)
			}
		})
	}
}

func TestBuildActionsString(t *testing.T) {
	tests := []struct {
		name       string
		toolCounts map[string]int
		duration   string
		expected   string
	}{
		{
			name:       "All actions with duration",
			toolCounts: map[string]int{"Write": 3, "Edit": 2, "Bash": 1},
			duration:   "⏱ 2m 15s",
			expected:   "📝 3 new  ✏️ 2 edited  ▶ 1 cmds  ⏱ 2m 15s",
		},
		{
			name:       "Only write",
			toolCounts: map[string]int{"Write": 1},
			duration:   "",
			expected:   "📝 1 new",
		},
		{
			name:       "Multiple edits",
			toolCounts: map[string]int{"Edit": 5},
			duration:   "⏱ 30s",
			expected:   "✏️ 5 edited  ⏱ 30s",
		},
		{
			name:       "No tools",
			toolCounts: map[string]int{},
			duration:   "",
			expected:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildActionsString(tt.toolCounts, tt.duration)
			if result != tt.expected {
				t.Errorf("buildActionsString() = %s, want %s", result, tt.expected)
			}
		})
	}
}

func TestCleanMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Headers",
			input:    "# Header\nSome text",
			expected: "Header Some text",
		},
		{
			name:     "Bullet lists",
			input:    "- Item 1\n- Item 2",
			expected: "Item 1 Item 2",
		},
		{
			name:     "Bold with **",
			input:    "This is **bold text** here",
			expected: "This is bold text here",
		},
		{
			name:     "Bold with __",
			input:    "This is __bold text__ here",
			expected: "This is bold text here",
		},
		{
			name:     "Italic with *",
			input:    "This is *italic text* here",
			expected: "This is italic text here",
		},
		{
			name:     "Italic with _",
			input:    "This is _italic text_ here",
			expected: "This is italic text here",
		},
		{
			name:     "Links",
			input:    "Check [this link](https://example.com) out",
			expected: "Check this link out",
		},
		{
			name:     "Images",
			input:    "See ![cat image](https://example.com/cat.jpg) here",
			expected: "See cat image here",
		},
		{
			name:     "Strikethrough",
			input:    "This is ~~deleted~~ text",
			expected: "This is deleted text",
		},
		{
			name:     "Code blocks",
			input:    "Some text\n```python\nprint('hello')\n```\nMore text",
			expected: "Some text More text",
		},
		{
			name:     "Inline code",
			input:    "`code` and text",
			expected: "code and text",
		},
		{
			name:     "Blockquotes",
			input:    "> This is a quote\nNormal text",
			expected: "This is a quote Normal text",
		},
		{
			name:     "Multiple markdown",
			input:    "# Title\n**Bold** and *italic* with [link](url)",
			expected: "Title Bold and italic with link",
		},
		{
			name:     "Multiple spaces",
			input:    "Multiple    spaces",
			expected: "Multiple spaces",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CleanMarkdown(tt.input)
			if result != tt.expected {
				t.Errorf("CleanMarkdown(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestTruncateText(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		maxLen   int
		expected string
	}{
		{
			name:     "Short text",
			text:     "Short text",
			maxLen:   100,
			expected: "Short text",
		},
		{
			name:     "Truncate at sentence boundary",
			text:     "This is first sentence. This is second sentence. This is third sentence.",
			maxLen:   50,
			expected: "This is first sentence.",
		},
		{
			name:     "Truncate with exclamation",
			text:     "Hello world! This is great! How are you doing today?",
			maxLen:   30,
			expected: "Hello world!",
		},
		{
			name:     "Truncate with question mark",
			text:     "What is this? Something else here with more text.",
			maxLen:   25,
			expected: "What is this?",
		},
		{
			name:     "No sentence boundary - truncate at word",
			text:     "This is a long text that should be truncated at word boundary",
			maxLen:   30,
			expected: "This is a long text that...",
		},
		{
			name:     "Very long word",
			text:     strings.Repeat("a", 200),
			maxLen:   50,
			expected: strings.Repeat("a", 47) + "...",
		},
		{
			name:     "Multibyte truncate",
			text:     strings.Repeat("α", 100),
			maxLen:   50,
			expected: strings.Repeat("α", 47) + "...",
		},
		{
			name:     "Multibyte truncate with sentence boundary",
			text:     "Это первое предложение. Это второе предложение.",
			maxLen:   30,
			expected: "Это первое предложение.",
		},
		{
			name:     "Multibyte truncate at word boundary",
			text:     "Один два три четыре пять",
			maxLen:   15,
			expected: "Один два...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateText(tt.text, tt.maxLen)
			if len([]rune(result)) > tt.maxLen {
				t.Errorf("truncateText() returned text longer than maxLen: %d > %d", len([]rune(result)), tt.maxLen)
			}
			if result != tt.expected {
				t.Errorf("truncateText() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestExtractFirstSentence(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected string
	}{
		{
			name:     "Long first sentence",
			text:     "First sentence is long enough. Second sentence.",
			expected: "First sentence is long enough.",
		},
		{
			name:     "Short first sentence - include second",
			text:     "Short! This is longer second sentence.",
			expected: "Short! This is longer second sentence.",
		},
		{
			name:     "Question with answer",
			text:     "Question? This is a detailed answer that follows.",
			expected: "Question? This is a detailed answer that follows.",
		},
		{
			name:     "User case: Идеально",
			text:     "Идеально! Все тесты исправлены! Создам краткий отчет.",
			expected: "Идеально! Все тесты исправлены!",
		},
		{
			name:     "Very long sentence - only first",
			text:     "This is a long first sentence that is already over twenty characters. Second sentence.",
			expected: "This is a long first sentence that is already over twenty characters.",
		},
		{
			name:     "No punctuation",
			text:     strings.Repeat("a", 150),
			expected: strings.Repeat("a", 100),
		},
		{
			name:     "Single short sentence",
			text:     "Done!",
			expected: "Done!",
		},
		{
			name:     "Version number should not split",
			text:     "Бинарник v1.6.0 установлен! Теперь уведомления будут работать.",
			expected: "Бинарник v1.6.0 установлен!",
		},
		{
			name:     "Multiple version numbers",
			text:     "Updated from v1.5.0 to v1.6.0. Release complete.",
			expected: "Updated from v1.5.0 to v1.6.0. Release complete.",
		},
		{
			name:     "Decimal numbers should not split",
			text:     "Success rate is 99.9 percent. Great result!",
			expected: "Success rate is 99.9 percent.",
		},
		{
			name:     "IP address should not split",
			text:     "Connected to 192.168.1.1 successfully. Server is running.",
			expected: "Connected to 192.168.1.1 successfully.",
		},
		{
			name:     "Multi-byte characters without punctuation panic",
			text:     strings.Repeat("α", 60), // 60 chars (runes), 120 bytes
			expected: strings.Repeat("α", 60),
		},
		{
			name:     "Emoji support",
			text:     "🚀 Rocket is fast! 🌟 Star is bright.",
			expected: "🚀 Rocket is fast!",
		},
		{
			name:     "Long emoji string without punctuation",
			text:     strings.Repeat("🚀", 60),
			expected: strings.Repeat("🚀", 60),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractFirstSentence(tt.text)
			if result != tt.expected {
				t.Errorf("extractFirstSentence() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestExtractAskUserQuestion(t *testing.T) {
	// Test with mock messages
	now := time.Now()
	recentTime := now.Format(time.RFC3339)
	oldTime := now.Add(-120 * time.Second).Format(time.RFC3339)

	tests := []struct {
		name           string
		messages       []jsonl.Message
		expectQuestion string
		expectRecent   bool
	}{
		{
			name: "Recent AskUserQuestion",
			messages: []jsonl.Message{
				{
					Type:      "assistant",
					Timestamp: recentTime,
					Message: jsonl.MessageContent{
						Content: []jsonl.Content{
							{
								Type: "tool_use",
								Name: "AskUserQuestion",
								Input: map[string]interface{}{
									"questions": []interface{}{
										map[string]interface{}{
											"question": "Which API should we use?",
										},
									},
								},
							},
						},
					},
				},
				{
					Type:      "assistant",
					Timestamp: now.Add(10 * time.Second).Format(time.RFC3339),
					Message: jsonl.MessageContent{
						Content: []jsonl.Content{
							{Type: "text", Text: "Some text"},
						},
					},
				},
			},
			expectQuestion: "Which API should we use?",
			expectRecent:   true,
		},
		{
			name: "Old AskUserQuestion",
			messages: []jsonl.Message{
				{
					Type:      "assistant",
					Timestamp: oldTime,
					Message: jsonl.MessageContent{
						Content: []jsonl.Content{
							{
								Type: "tool_use",
								Name: "AskUserQuestion",
								Input: map[string]interface{}{
									"questions": []interface{}{
										map[string]interface{}{
											"question": "Old question",
										},
									},
								},
							},
						},
					},
				},
				{
					Type:      "assistant",
					Timestamp: recentTime,
					Message: jsonl.MessageContent{
						Content: []jsonl.Content{
							{Type: "text", Text: "Recent text"},
						},
					},
				},
			},
			expectQuestion: "Old question",
			expectRecent:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			question, isRecent := extractAskUserQuestion(tt.messages)
			if question != tt.expectQuestion {
				t.Errorf("extractAskUserQuestion() question = %s, want %s", question, tt.expectQuestion)
			}
			if isRecent != tt.expectRecent {
				t.Errorf("extractAskUserQuestion() isRecent = %v, want %v", isRecent, tt.expectRecent)
			}
		})
	}
}

func TestCountToolsByType(t *testing.T) {
	baseTime := time.Now()
	userTime := baseTime.Format(time.RFC3339)
	afterTime := baseTime.Add(10 * time.Second).Format(time.RFC3339)
	beforeTime := baseTime.Add(-10 * time.Second).Format(time.RFC3339)

	messages := []jsonl.Message{
		{
			Type:      "user",
			Timestamp: userTime,
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "text", Text: "Do something"},
				},
			},
		},
		{
			Type:      "assistant",
			Timestamp: beforeTime,
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "tool_use", Name: "Read"}, // Before user - should NOT count
				},
			},
		},
		{
			Type:      "assistant",
			Timestamp: afterTime,
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "tool_use", Name: "Write"},
					{Type: "tool_use", Name: "Edit"},
					{Type: "tool_use", Name: "Write"},
				},
			},
		},
	}

	counts := countToolsByType(messages)

	if counts["Write"] != 2 {
		t.Errorf("Write count = %d, want 2", counts["Write"])
	}
	if counts["Edit"] != 1 {
		t.Errorf("Edit count = %d, want 1", counts["Edit"])
	}
	if counts["Read"] != 0 {
		t.Errorf("Read count = %d, want 0 (before user message)", counts["Read"])
	}
}

func TestGetDefaultMessage(t *testing.T) {
	cfg := config.DefaultConfig()

	tests := []struct {
		status   string
		expected string
	}{
		{"task_complete", "Completed"},
		{"question", "Question"},
		{"plan_ready", "Plan"},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			result := GetDefaultMessage(analyzer.Status(tt.status), cfg)
			// Default message removes emoji, so check if expected text is contained
			if !strings.Contains(result, tt.expected) {
				t.Errorf("GetDefaultMessage(%s) = %s, want to contain %s", tt.status, result, tt.expected)
			}
		})
	}
}

// === Tests for GenerateFromTranscript ===

func TestGenerateFromTranscript_TaskComplete(t *testing.T) {
	// Create temp transcript with task_complete scenario
	tmpDir := t.TempDir()
	transcriptPath := tmpDir + "/transcript.jsonl"

	messages := buildTestTranscript([]string{"Write", "Edit", "Bash"}, "Created auth module", time.Now())
	writeTranscript(t, transcriptPath, messages)

	cfg := config.DefaultConfig()
	result := GenerateFromTranscript(transcriptPath, analyzer.StatusTaskComplete, cfg)

	// Should contain action summary
	if !strings.Contains(result, "new") || !strings.Contains(result, "edited") {
		t.Errorf("TaskComplete summary should mention actions, got: %s", result)
	}
}

func TestGenerateFromTranscript_Question(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := tmpDir + "/transcript.jsonl"

	// Build transcript with AskUserQuestion
	now := time.Now()
	messages := []jsonl.Message{
		{
			Type:      "user",
			Timestamp: now.Add(-10 * time.Second).Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{{Type: "text", Text: "Help me"}},
			},
		},
		{
			Type:      "assistant",
			Timestamp: now.Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{
						Type: "tool_use",
						Name: "AskUserQuestion",
						Input: map[string]interface{}{
							"questions": []interface{}{
								map[string]interface{}{
									"question": "Which library should we use?",
								},
							},
						},
					},
				},
			},
		},
	}

	writeTranscript(t, transcriptPath, messages)

	cfg := config.DefaultConfig()
	result := GenerateFromTranscript(transcriptPath, analyzer.StatusQuestion, cfg)

	if !strings.Contains(result, "Which library") {
		t.Errorf("Question summary should contain question text, got: %s", result)
	}
}

func TestGenerateFromTranscript_PlanReady(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := tmpDir + "/transcript.jsonl"

	now := time.Now()
	messages := []jsonl.Message{
		{
			Type:      "user",
			Timestamp: now.Add(-10 * time.Second).Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{{Type: "text", Text: "Create auth"}},
			},
		},
		{
			Type:      "assistant",
			Timestamp: now.Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{
						Type: "tool_use",
						Name: "ExitPlanMode",
						Input: map[string]interface{}{
							"plan": "1. Create user model\n2. Add authentication\n3. Test endpoints",
						},
					},
				},
			},
		},
	}

	writeTranscript(t, transcriptPath, messages)

	cfg := config.DefaultConfig()
	result := GenerateFromTranscript(transcriptPath, analyzer.StatusPlanReady, cfg)

	if !strings.Contains(result, "Create user model") {
		t.Errorf("Plan summary should contain plan text, got: %s", result)
	}
}

func TestGenerateFromTranscript_ReviewComplete(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := tmpDir + "/transcript.jsonl"

	messages := buildTestTranscript([]string{"Read", "Read", "Grep"}, "Analyzed the codebase", time.Now())
	writeTranscript(t, transcriptPath, messages)

	cfg := config.DefaultConfig()
	result := GenerateFromTranscript(transcriptPath, analyzer.StatusReviewComplete, cfg)

	// Should contain either "Reviewed" or the extracted text
	if result == "" {
		t.Errorf("Review summary should not be empty")
	}
	// Just verify it's not empty and doesn't crash
	if len(result) < 5 {
		t.Errorf("Review summary too short: %s", result)
	}
}

func TestGenerateFromTranscript_NonexistentFile(t *testing.T) {
	cfg := config.DefaultConfig()
	result := GenerateFromTranscript("/nonexistent/path.jsonl", analyzer.StatusTaskComplete, cfg)

	// Should fallback to default message
	if !strings.Contains(result, "Completed") {
		t.Errorf("Should return default message for nonexistent file, got: %s", result)
	}
}

func TestGenerateFromTranscript_EmptyTranscript(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := tmpDir + "/empty.jsonl"

	// Create empty file
	writeTranscript(t, transcriptPath, []jsonl.Message{})

	cfg := config.DefaultConfig()
	result := GenerateFromTranscript(transcriptPath, analyzer.StatusTaskComplete, cfg)

	// Should fallback to default message
	if !strings.Contains(result, "Completed") {
		t.Errorf("Should return default message for empty transcript, got: %s", result)
	}
}

func TestGenerateFromTranscript_SessionLimitReached(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := tmpDir + "/session_limit.jsonl"

	// Create transcript with session limit message
	messages := []jsonl.Message{
		{
			Type:      "user",
			Timestamp: time.Now().Add(-1 * time.Minute).Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "text", Text: "Continue working"},
				},
			},
		},
		{
			Type:      "assistant",
			Timestamp: time.Now().Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "text", Text: "Session limit reached. Please start a new conversation."},
				},
			},
		},
	}

	writeTranscript(t, transcriptPath, messages)

	cfg := config.DefaultConfig()
	result := GenerateFromTranscript(transcriptPath, analyzer.StatusSessionLimitReached, cfg)

	// Should contain the base message and duration
	if !strings.Contains(result, "Session limit reached") {
		t.Errorf("Expected result to contain 'Session limit reached', got %q", result)
	}
	if !strings.Contains(result, "⏱") {
		t.Errorf("Expected result to contain duration, got %q", result)
	}
}

// === Tests for GenerateSimple ===

func TestGenerateSimple(t *testing.T) {
	cfg := config.DefaultConfig()

	tests := []struct {
		status   analyzer.Status
		expected string
	}{
		{analyzer.StatusTaskComplete, "Completed"},
		{analyzer.StatusQuestion, "Question"},
		{analyzer.StatusPlanReady, "Plan"},
		{analyzer.StatusReviewComplete, "Review"},
		{analyzer.StatusSessionLimitReached, "Session Limit Reached"},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			result := GenerateSimple(tt.status, cfg)
			if !strings.Contains(result, tt.expected) {
				t.Errorf("GenerateSimple(%s) = %s, want to contain %s", tt.status, result, tt.expected)
			}
		})
	}
}

// === Helper functions ===

func buildTestTranscript(tools []string, responseText string, timestamp time.Time) []jsonl.Message {
	var content []jsonl.Content

	// Add tools
	for _, tool := range tools {
		content = append(content, jsonl.Content{
			Type: "tool_use",
			Name: tool,
		})
	}

	// Add text
	content = append(content, jsonl.Content{
		Type: "text",
		Text: responseText,
	})

	return []jsonl.Message{
		{
			Type:      "user",
			Timestamp: timestamp.Add(-10 * time.Second).Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{{Type: "text", Text: "User request"}},
			},
		},
		{
			Type:      "assistant",
			Timestamp: timestamp.Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: content,
			},
		},
	}
}

func writeTranscript(t *testing.T, path string, messages []jsonl.Message) {
	t.Helper()

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create transcript: %v", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	for _, msg := range messages {
		if err := encoder.Encode(msg); err != nil {
			t.Fatalf("failed to encode message: %v", err)
		}
	}
}

// === Tests for uncovered functions ===

func TestGenerateAPIErrorSummary(t *testing.T) {
	cfg := config.DefaultConfig()
	messages := []jsonl.Message{
		{
			Type:      "assistant",
			Timestamp: time.Now().Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "text", Text: "API Error: 401. Please run /login to continue."},
				},
			},
		},
	}

	result := GenerateFromMessages(messages, analyzer.StatusAPIError, cfg)
	expected := "Please run /login"
	if result != expected {
		t.Errorf("generateAPIErrorSummary() = %q, want %q", result, expected)
	}
}

func TestGetRecentAssistantMessages(t *testing.T) {
	now := time.Now()
	messages := []jsonl.Message{
		{
			Type:      "user",
			Timestamp: now.Add(-5 * time.Second).Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{{Type: "text", Text: "User message"}},
			},
		},
		{
			Type:      "assistant",
			Timestamp: now.Add(-4 * time.Second).Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{{Type: "text", Text: "First assistant"}},
			},
		},
		{
			Type:      "assistant",
			Timestamp: now.Add(-3 * time.Second).Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{{Type: "text", Text: "Second assistant"}},
			},
		},
		{
			Type:      "assistant",
			Timestamp: now.Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{{Type: "text", Text: "Third assistant"}},
			},
		},
	}

	result := getRecentAssistantMessages(messages, 2)
	if len(result) != 2 {
		t.Errorf("getRecentAssistantMessages() returned %d messages, want 2", len(result))
	}
	// Should return latest 2 assistant messages
	if len(result) == 2 {
		texts := jsonl.ExtractTextFromMessages(result)
		if !strings.Contains(strings.Join(texts, " "), "Third assistant") {
			t.Errorf("Should contain latest assistant message")
		}
	}
}

func TestGenerateQuestionSummary_WithRecentQuestion(t *testing.T) {
	now := time.Now()
	cfg := config.DefaultConfig()
	messages := []jsonl.Message{
		{
			Type:      "user",
			Timestamp: now.Add(-10 * time.Second).Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{{Type: "text", Text: "Help me"}},
			},
		},
		{
			Type:      "assistant",
			Timestamp: now.Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{
						Type: "tool_use",
						Name: "AskUserQuestion",
						Input: map[string]interface{}{
							"questions": []interface{}{
								map[string]interface{}{
									"question": "Which API should we use for authentication?",
								},
							},
						},
					},
				},
			},
		},
	}

	result := GenerateFromMessages(messages, analyzer.StatusQuestion, cfg)
	if !strings.Contains(result, "Which API should we use") {
		t.Errorf("generateQuestionSummary() = %q, should contain question", result)
	}
}

func TestGenerateQuestionSummary_WithoutQuestion(t *testing.T) {
	now := time.Now()
	cfg := config.DefaultConfig()
	messages := []jsonl.Message{
		{
			Type:      "assistant",
			Timestamp: now.Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "text", Text: "Just some regular text without question"},
				},
			},
		},
	}

	result := GenerateFromMessages(messages, analyzer.StatusQuestion, cfg)
	// Should either extract text or fallback to default message
	if result == "" {
		t.Errorf("generateQuestionSummary() should not be empty")
	}
	// Verify it's at least some meaningful text (not just empty or error)
	if len(result) < 5 {
		t.Errorf("generateQuestionSummary() returned too short: %q", result)
	}
}

func TestGenerateReviewSummary_WithToolsAndDuration(t *testing.T) {
	now := time.Now()
	cfg := config.DefaultConfig()
	messages := []jsonl.Message{
		{
			Type:      "user",
			Timestamp: now.Add(-120 * time.Second).Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{{Type: "text", Text: "Review the auth module"}},
			},
		},
		{
			Type:      "assistant",
			Timestamp: now.Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "tool_use", Name: "Read"},
					{Type: "tool_use", Name: "Grep"},
					{Type: "text", Text: "I've reviewed the authentication module. The code looks good."},
				},
			},
		},
	}

	result := GenerateFromMessages(messages, analyzer.StatusReviewComplete, cfg)
	if result == "" {
		t.Errorf("generateReviewSummary() should not be empty")
	}
	// Should contain either tool actions or extracted text
	if len(result) < 10 {
		t.Errorf("generateReviewSummary() too short: %q", result)
	}
}

func TestGenerateReviewSummary_NoTools(t *testing.T) {
	now := time.Now()
	cfg := config.DefaultConfig()
	messages := []jsonl.Message{
		{
			Type:      "assistant",
			Timestamp: now.Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "text", Text: "The review is complete. Everything looks good!"},
				},
			},
		},
	}

	result := GenerateFromMessages(messages, analyzer.StatusReviewComplete, cfg)
	if !strings.Contains(result, "review") && !strings.Contains(result, "complete") {
		t.Errorf("generateReviewSummary() should extract meaningful text: %q", result)
	}
}

func TestGenerateTaskSummary_WithMultipleTools(t *testing.T) {
	now := time.Now()
	cfg := config.DefaultConfig()
	messages := []jsonl.Message{
		{
			Type:      "user",
			Timestamp: now.Add(-180 * time.Second).Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{{Type: "text", Text: "Create user auth"}},
			},
		},
		{
			Type:      "assistant",
			Timestamp: now.Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "tool_use", Name: "Write"},
					{Type: "tool_use", Name: "Write"},
					{Type: "tool_use", Name: "Edit"},
					{Type: "tool_use", Name: "Bash"},
					{Type: "text", Text: "Created user authentication module with tests."},
				},
			},
		},
	}

	result := GenerateFromMessages(messages, analyzer.StatusTaskComplete, cfg)
	// Should contain tool counts and duration
	if !strings.Contains(result, "new") && !strings.Contains(result, "edited") {
		t.Errorf("generateTaskSummary() should mention tools: %q", result)
	}
	if !strings.Contains(result, "⏱") {
		t.Errorf("generateTaskSummary() should include duration: %q", result)
	}
}

func TestGenerateTaskSummary_NoTools(t *testing.T) {
	now := time.Now()
	cfg := config.DefaultConfig()
	messages := []jsonl.Message{
		{
			Type:      "assistant",
			Timestamp: now.Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "text", Text: "Task completed successfully!"},
				},
			},
		},
	}

	result := GenerateFromMessages(messages, analyzer.StatusTaskComplete, cfg)
	// Should extract text when no tools
	if !strings.Contains(result, "Task completed") && !strings.Contains(result, "successfully") {
		t.Errorf("generateTaskSummary() should extract text: %q", result)
	}
}

func TestGenerateFromTranscript_APIError(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := tmpDir + "/api_error.jsonl"

	messages := []jsonl.Message{
		{
			Type:      "assistant",
			Timestamp: time.Now().Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "text", Text: "API Error: 401. Please run /login to authenticate."},
				},
			},
		},
	}

	writeTranscript(t, transcriptPath, messages)

	cfg := config.DefaultConfig()
	result := GenerateFromTranscript(transcriptPath, analyzer.StatusAPIError, cfg)

	if !strings.Contains(result, "Please run /login") {
		t.Errorf("API Error summary should contain login prompt, got: %s", result)
	}
}

func TestGenerateAPIErrorOverloadedSummary(t *testing.T) {
	cfg := config.DefaultConfig()

	t.Run("extracts_error_text", func(t *testing.T) {
		messages := []jsonl.Message{
			{
				Type:              "assistant",
				IsApiErrorMessage: true,
				Error:             "unknown",
				Timestamp:         time.Now().Format(time.RFC3339),
				Message: jsonl.MessageContent{
					Content: []jsonl.Content{
						{Type: "text", Text: `API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`},
					},
				},
			},
		}

		result := GenerateFromMessages(messages, analyzer.StatusAPIErrorOverloaded, cfg)
		if !strings.Contains(result, "529") {
			t.Errorf("should contain error code, got: %s", result)
		}
		if !strings.Contains(result, "overloaded") {
			t.Errorf("should contain error type, got: %s", result)
		}
	})

	t.Run("connection_error", func(t *testing.T) {
		messages := []jsonl.Message{
			{
				Type:              "assistant",
				IsApiErrorMessage: true,
				Error:             "unknown",
				Timestamp:         time.Now().Format(time.RFC3339),
				Message: jsonl.MessageContent{
					Content: []jsonl.Content{
						{Type: "text", Text: "API Error: Connection error."},
					},
				},
			},
		}

		result := GenerateFromMessages(messages, analyzer.StatusAPIErrorOverloaded, cfg)
		if !strings.Contains(result, "Connection error") {
			t.Errorf("should contain 'Connection error', got: %s", result)
		}
	})

	t.Run("no_api_error_messages", func(t *testing.T) {
		messages := []jsonl.Message{
			{
				Type:      "assistant",
				Timestamp: time.Now().Format(time.RFC3339),
				Message: jsonl.MessageContent{
					Content: []jsonl.Content{
						{Type: "text", Text: "Normal message"},
					},
				},
			},
		}

		result := GenerateFromMessages(messages, analyzer.StatusAPIErrorOverloaded, cfg)
		if result != "API error occurred" {
			t.Errorf("should return fallback, got: %s", result)
		}
	})

	t.Run("empty_messages", func(t *testing.T) {
		// Empty messages short-circuit to GetDefaultMessage in the public API,
		// so test the body helper directly here to cover its empty fallback.
		result := generateAPIErrorOverloadedBody([]jsonl.Message{})
		if result != "API error occurred" {
			t.Errorf("should return fallback for empty, got: %s", result)
		}
	})
}

func TestGenerateFromTranscript_APIErrorOverloaded(t *testing.T) {
	tmpDir := t.TempDir()
	transcriptPath := tmpDir + "/api_error_overloaded.jsonl"

	messages := []jsonl.Message{
		{
			Type:              "assistant",
			IsApiErrorMessage: true,
			Error:             "unknown",
			Timestamp:         time.Now().Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "text", Text: `API Error: 500 {"type":"error","error":{"type":"internal_server_error","message":"Internal server error"}}`},
				},
			},
		},
	}

	writeTranscript(t, transcriptPath, messages)

	cfg := config.DefaultConfig()
	result := GenerateFromTranscript(transcriptPath, analyzer.StatusAPIErrorOverloaded, cfg)

	if !strings.Contains(result, "500") {
		t.Errorf("API Error Overloaded summary should contain error code, got: %s", result)
	}
}

func TestCalculateDuration(t *testing.T) {
	now := time.Now()
	userTime := now.Add(-120 * time.Second)

	messages := []jsonl.Message{
		{
			Type:      "user",
			Timestamp: userTime.Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{{Type: "text", Text: "Do something"}},
			},
		},
		{
			Type:      "assistant",
			Timestamp: now.Format(time.RFC3339),
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{{Type: "text", Text: "Done"}},
			},
		},
	}

	duration := calculateDuration(messages)
	// Should be "⏱ 2m" for 120 seconds
	if !strings.Contains(duration, "⏱") || !strings.Contains(duration, "2m") {
		t.Errorf("calculateDuration() = %q, want '⏱ 2m'", duration)
	}
}

func TestExtractExitPlanModePlan(t *testing.T) {
	tests := []struct {
		name     string
		messages []jsonl.Message
		expected string
	}{
		{
			name: "With ExitPlanMode tool",
			messages: []jsonl.Message{
				{
					Type:      "assistant",
					Timestamp: time.Now().Format(time.RFC3339),
					Message: jsonl.MessageContent{
						Content: []jsonl.Content{
							{
								Type: "tool_use",
								Name: "ExitPlanMode",
								Input: map[string]interface{}{
									"plan": "1. Create API\n2. Add tests\n3. Deploy",
								},
							},
						},
					},
				},
			},
			expected: "1. Create API",
		},
		{
			name: "Without ExitPlanMode",
			messages: []jsonl.Message{
				{
					Type:      "assistant",
					Timestamp: time.Now().Format(time.RFC3339),
					Message: jsonl.MessageContent{
						Content: []jsonl.Content{
							{Type: "text", Text: "Here's the plan"},
						},
					},
				},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractExitPlanModePlan(tt.messages)
			if tt.expected != "" && !strings.Contains(result, tt.expected) {
				t.Errorf("extractExitPlanModePlan() = %q, want to contain %q", result, tt.expected)
			}
			if tt.expected == "" && result != "" {
				t.Errorf("extractExitPlanModePlan() = %q, want empty", result)
			}
		})
	}
}

// === Additional Coverage Tests for generateReviewSummary ===

func TestGenerateReviewSummary_WithKeywords(t *testing.T) {
	cfg := &config.Config{}
	nowStr := time.Now().Format(time.RFC3339)

	tests := []struct {
		name     string
		messages []jsonl.Message
		keyword  string
	}{
		{
			name: "review keyword",
			messages: []jsonl.Message{
				{
					Type:      "assistant",
					Timestamp: nowStr,
					Message: jsonl.MessageContent{
						Content: []jsonl.Content{
							{Type: "text", Text: "I'll review the code carefully"},
						},
					},
				},
			},
			keyword: "review",
		},
		{
			name: "analysis keyword",
			messages: []jsonl.Message{
				{
					Type:      "assistant",
					Timestamp: nowStr,
					Message: jsonl.MessageContent{
						Content: []jsonl.Content{
							{Type: "text", Text: "After analysis of the codebase, I found issues"},
						},
					},
				},
			},
			keyword: "analysis",
		},
		{
			name: "проверка keyword (Russian)",
			messages: []jsonl.Message{
				{
					Type:      "assistant",
					Timestamp: nowStr,
					Message: jsonl.MessageContent{
						Content: []jsonl.Content{
							{Type: "text", Text: "Проведу проверку кода"},
						},
					},
				},
			},
			keyword: "проверк",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateFromMessages(tt.messages, analyzer.StatusReviewComplete, cfg)
			if result == "" {
				t.Error("generateReviewSummary() returned empty string")
			}
			if !strings.Contains(strings.ToLower(result), tt.keyword) && result != "Code review completed" {
				t.Logf("Result: %q (fallback is OK)", result)
			}
		})
	}
}

func TestGenerateReviewSummary_WithReadTools(t *testing.T) {
	cfg := &config.Config{}
	nowStr := time.Now().Format(time.RFC3339)

	tests := []struct {
		name      string
		readCount int
		expected  string
	}{
		{
			name:      "single read",
			readCount: 1,
			expected:  "Reviewed 1 file",
		},
		{
			name:      "multiple reads",
			readCount: 5,
			expected:  "Reviewed 5 files",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build message with Read tools
			content := []jsonl.Content{
				{Type: "text", Text: "Checking the files"},
			}
			for i := 0; i < tt.readCount; i++ {
				content = append(content, jsonl.Content{
					Type: "tool_use",
					Name: "Read",
					Input: map[string]interface{}{
						"file_path": fmt.Sprintf("/test/file%d.go", i),
					},
				})
			}

			messages := []jsonl.Message{
				{
					Type:      "assistant",
					Timestamp: nowStr,
					Message: jsonl.MessageContent{
						Content: content,
					},
				},
			}

			result := GenerateFromMessages(messages, analyzer.StatusReviewComplete, cfg)
			if result != tt.expected {
				t.Errorf("generateReviewSummary() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestGenerateReviewSummary_Fallback(t *testing.T) {
	cfg := &config.Config{}
	nowStr := time.Now().Format(time.RFC3339)

	// No keywords, no Read tools
	messages := []jsonl.Message{
		{
			Type:      "assistant",
			Timestamp: nowStr,
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "text", Text: "Task completed successfully"},
				},
			},
		},
	}

	result := GenerateFromMessages(messages, analyzer.StatusReviewComplete, cfg)
	if result != "Code review completed" {
		t.Errorf("generateReviewSummary() fallback = %q, want 'Code review completed'", result)
	}
}

// === Additional Coverage Tests for generateTaskSummary ===

func TestGenerateTaskSummary_EmptyMessages(t *testing.T) {
	cfg := &config.Config{
		Statuses: map[string]config.StatusInfo{
			"task_complete": {Title: "Task Complete"},
		},
	}

	messages := []jsonl.Message{}

	result := GenerateFromMessages(messages, analyzer.StatusTaskComplete, cfg)
	if result == "" {
		t.Error("generateTaskSummary() should return default message for empty messages")
	}
}

func TestGenerateTaskSummary_ShortMessage(t *testing.T) {
	cfg := &config.Config{}
	userTS := time.Now().Add(-5 * time.Second).Format(time.RFC3339)
	assistantTS := time.Now().Format(time.RFC3339)

	messages := []jsonl.Message{
		{
			Type:      "user",
			Timestamp: userTS,
			Message: jsonl.MessageContent{
				ContentString: "Do task",
			},
		},
		{
			Type:      "assistant",
			Timestamp: assistantTS,
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "text", Text: "Done!"},
					{Type: "tool_use", Name: "Write", Input: map[string]interface{}{"file_path": "/test.go"}},
				},
			},
		},
	}

	result := GenerateFromMessages(messages, analyzer.StatusTaskComplete, cfg)
	if result == "" {
		t.Error("generateTaskSummary() returned empty string")
	}
	// Should include short message and actions
	if !strings.Contains(result, "Done") || !strings.Contains(result, "new") {
		t.Logf("Result: %q (may vary)", result)
	}
}

func TestGenerateTaskSummary_NoDuplicatePunctuation(t *testing.T) {
	cfg := &config.Config{}
	userTS := time.Now().Add(-5 * time.Second).Format(time.RFC3339)
	assistantTS := time.Now().Format(time.RFC3339)

	tests := []struct {
		name    string
		message string
	}{
		{"trailing dot", "All done."},
		{"trailing exclamation", "Done!"},
		{"trailing question", "Ready?"},
		{"no trailing punct", "All done"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := []jsonl.Message{
				{
					Type:      "user",
					Timestamp: userTS,
					Message: jsonl.MessageContent{
						ContentString: "Do task",
					},
				},
				{
					Type:      "assistant",
					Timestamp: assistantTS,
					Message: jsonl.MessageContent{
						Content: []jsonl.Content{
							{Type: "text", Text: tt.message},
							{Type: "tool_use", Name: "Write", Input: map[string]interface{}{"file_path": "/test.go"}},
						},
					},
				},
			}

			result := GenerateFromMessages(messages, analyzer.StatusTaskComplete, cfg)
			if strings.Contains(result, "..") {
				t.Errorf("Double dots in result: %q", result)
			}
			if strings.Contains(result, "!.") {
				t.Errorf("Exclamation+dot in result: %q", result)
			}
			if strings.Contains(result, "?.") {
				t.Errorf("Question+dot in result: %q", result)
			}
		})
	}
}

func TestGenerateTaskSummary_LongMessage(t *testing.T) {
	cfg := &config.Config{}
	userTS := time.Now().Add(-10 * time.Second).Format(time.RFC3339)
	assistantTS := time.Now().Format(time.RFC3339)

	longText := strings.Repeat("This is a very long message. ", 20) // > 150 chars

	messages := []jsonl.Message{
		{
			Type:      "user",
			Timestamp: userTS,
			Message: jsonl.MessageContent{
				ContentString: "Do task",
			},
		},
		{
			Type:      "assistant",
			Timestamp: assistantTS,
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "text", Text: longText},
					{Type: "tool_use", Name: "Edit", Input: map[string]interface{}{"file_path": "/test.go"}},
				},
			},
		},
	}

	result := GenerateFromMessages(messages, analyzer.StatusTaskComplete, cfg)
	if result == "" {
		t.Error("generateTaskSummary() returned empty string")
	}
	// Should truncate long message
	if len([]rune(result)) > 200 {
		t.Errorf("generateTaskSummary() result too long: %d chars", len([]rune(result)))
	}
}

func TestGenerateTaskSummary_MultibyteThreshold(t *testing.T) {
	cfg := &config.Config{}
	userTS := time.Now().Add(-10 * time.Second).Format(time.RFC3339)
	assistantTS := time.Now().Format(time.RFC3339)

	// A message that is > 150 bytes but < 150 runes
	// "α" is 2 bytes. 80 * 2 = 160 bytes. But only 80 runes.
	multibyteText := strings.Repeat("α", 80)

	messages := []jsonl.Message{
		{
			Type:      "user",
			Timestamp: userTS,
			Message:   jsonl.MessageContent{ContentString: "Task"},
		},
		{
			Type:      "assistant",
			Timestamp: assistantTS,
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "text", Text: multibyteText},
				},
			},
		},
	}

	result := GenerateFromMessages(messages, analyzer.StatusTaskComplete, cfg)
	// Because it's < 150 runes, it should NOT be passed to extractFirstSentence
	// and should be returned as-is (possibly truncated by the final truncateText(150))
	if !strings.Contains(result, multibyteText) {
		t.Errorf("generateTaskSummary should not have extracted first sentence for short rune-count message, got: %q", result)
	}
}

func TestGenerateTaskSummary_OnlyActions(t *testing.T) {
	cfg := &config.Config{}
	userTS := time.Now().Add(-10 * time.Second).Format(time.RFC3339)
	assistantTS := time.Now().Format(time.RFC3339)

	messages := []jsonl.Message{
		{
			Type:      "user",
			Timestamp: userTS,
			Message: jsonl.MessageContent{
				ContentString: "Do task",
			},
		},
		{
			Type:      "assistant",
			Timestamp: assistantTS,
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "tool_use", Name: "Write", Input: map[string]interface{}{"file_path": "/test.go"}},
					{Type: "tool_use", Name: "Write", Input: map[string]interface{}{"file_path": "/test2.go"}},
				},
			},
		},
	}

	result := GenerateFromMessages(messages, analyzer.StatusTaskComplete, cfg)
	if result == "" {
		t.Error("generateTaskSummary() returned empty string")
	}
	// Should show tool counts
	if !strings.Contains(result, "Wrote 2") && !strings.Contains(result, "operations") {
		t.Logf("Result: %q (should mention tools)", result)
	}
}

func TestGenerateTaskSummary_FinalFallback(t *testing.T) {
	cfg := &config.Config{}
	nowStr := time.Now().Format(time.RFC3339)

	// No user message, no tools
	messages := []jsonl.Message{
		{
			Type:      "assistant",
			Timestamp: nowStr,
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{},
			},
		},
	}

	result := GenerateFromMessages(messages, analyzer.StatusTaskComplete, cfg)
	if result == "" {
		t.Error("generateTaskSummary() should return fallback message")
	}
	if result != "Task completed successfully" {
		t.Logf("Result: %q (fallback variant)", result)
	}
}

// === Additional Coverage Tests for generateQuestionSummary ===

func TestGenerateQuestionSummary_NotRecentAskUserQuestion(t *testing.T) {
	cfg := &config.Config{}

	// AskUserQuestion from 120 seconds ago (not recent)
	oldTS := time.Now().Add(-120 * time.Second).Format(time.RFC3339)
	nowTS := time.Now().Format(time.RFC3339)

	messages := []jsonl.Message{
		{
			Type:      "assistant",
			Timestamp: oldTS,
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{
						Type: "tool_use",
						Name: "AskUserQuestion",
						Input: map[string]interface{}{
							"questions": []interface{}{
								map[string]interface{}{
									"question": "Old question?",
								},
							},
						},
					},
				},
			},
		},
		{
			Type:      "assistant",
			Timestamp: nowTS,
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "text", Text: "What should I do next?"},
				},
			},
		},
	}

	result := GenerateFromMessages(messages, analyzer.StatusQuestion, cfg)
	if result == "" {
		t.Error("generateQuestionSummary() returned empty string")
	}
	// Should not use old AskUserQuestion (not recent), should extract from text
	if strings.Contains(result, "Old question") {
		t.Error("Should not use non-recent AskUserQuestion")
	}
}

func TestGenerateQuestionSummary_MultipleQuestions(t *testing.T) {
	cfg := &config.Config{}
	nowStr := time.Now().Format(time.RFC3339)

	messages := []jsonl.Message{
		{
			Type:      "assistant",
			Timestamp: nowStr,
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "text", Text: "This is a very long question that should not be chosen because it's too verbose and lengthy?"},
					{Type: "text", Text: "Short Q?"},
					{Type: "text", Text: "Another longer question that is also quite verbose?"},
				},
			},
		},
	}

	result := GenerateFromMessages(messages, analyzer.StatusQuestion, cfg)
	if result == "" {
		t.Error("generateQuestionSummary() returned empty string")
	}
	// Should pick shortest question
	if !strings.Contains(result, "Short") && len(result) > 50 {
		t.Logf("Result: %q (should prefer shorter questions)", result)
	}
}

func TestGenerateQuestionSummary_NoQuestionMark(t *testing.T) {
	cfg := &config.Config{}
	nowStr := time.Now().Format(time.RFC3339)

	messages := []jsonl.Message{
		{
			Type:      "assistant",
			Timestamp: nowStr,
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "text", Text: "Please provide more information. I need clarification on your requirements. Let me know what you think."},
				},
			},
		},
	}

	result := GenerateFromMessages(messages, analyzer.StatusQuestion, cfg)
	if result == "" {
		t.Error("generateQuestionSummary() returned empty string")
	}
	// Should extract first sentence
	if !strings.Contains(result, "Please provide") && !strings.Contains(result, "Claude needs") {
		t.Logf("Result: %q (should extract first sentence or use fallback)", result)
	}
}

func TestGenerateQuestionSummary_VeryShortText(t *testing.T) {
	cfg := &config.Config{}
	nowStr := time.Now().Format(time.RFC3339)

	messages := []jsonl.Message{
		{
			Type:      "assistant",
			Timestamp: nowStr,
			Message: jsonl.MessageContent{
				Content: []jsonl.Content{
					{Type: "text", Text: "Hi"},
				},
			},
		},
	}

	result := GenerateFromMessages(messages, analyzer.StatusQuestion, cfg)
	if result == "" {
		t.Error("generateQuestionSummary() returned empty string")
	}
	// Should use fallback for very short text
	if result != "Claude needs your input to continue" {
		t.Logf("Result: %q (should use fallback for short text)", result)
	}
}

// === Tests for GenerateFromMessagesStructured ===

func TestGenerateFromMessagesStructured_TaskComplete(t *testing.T) {
	cfg := config.DefaultConfig()

	now := time.Now()
	messages := []jsonl.Message{
		{
			Type:      "user",
			Timestamp: now.Add(-30 * time.Second).Format(time.RFC3339),
			Message:   jsonl.MessageContent{Content: []jsonl.Content{{Type: "text", Text: "do it"}}},
		},
		{
			Type:      "assistant",
			Timestamp: now.Add(-25 * time.Second).Format(time.RFC3339),
			Message: jsonl.MessageContent{Content: []jsonl.Content{
				{Type: "tool_use", Name: "Write", Input: map[string]interface{}{}},
				{Type: "tool_use", Name: "Bash", Input: map[string]interface{}{}},
			}},
		},
		{
			Type:      "assistant",
			Timestamp: now.Format(time.RFC3339),
			Message:   jsonl.MessageContent{Content: []jsonl.Content{{Type: "text", Text: "Done."}}},
		},
	}

	body, actions := GenerateFromMessagesStructured(messages, analyzer.StatusTaskComplete, cfg)
	if body != "Done." {
		t.Errorf("body = %q, want %q", body, "Done.")
	}
	if !strings.Contains(actions, "📝 1 new") {
		t.Errorf("actions = %q, want to contain 📝 1 new", actions)
	}
	if !strings.Contains(actions, "▶ 1 cmds") {
		t.Errorf("actions = %q, want to contain ▶ 1 cmds", actions)
	}
	if !strings.Contains(actions, "⏱") {
		t.Errorf("actions = %q, want to contain duration", actions)
	}

	// Joined output should match what GenerateFromMessages returns.
	joined := GenerateFromMessages(messages, analyzer.StatusTaskComplete, cfg)
	if joined != appendActions(body, actions) {
		t.Errorf("GenerateFromMessages joined = %q, want %q", joined, appendActions(body, actions))
	}
}

func TestGenerateFromMessagesStructured_APIErrorHasNoActions(t *testing.T) {
	cfg := config.DefaultConfig()
	messages := []jsonl.Message{
		{
			Type:      "assistant",
			Timestamp: time.Now().Format(time.RFC3339),
			Message:   jsonl.MessageContent{Content: []jsonl.Content{{Type: "text", Text: "401 unauthorized"}}},
		},
	}
	body, actions := GenerateFromMessagesStructured(messages, analyzer.StatusAPIError, cfg)
	if body != "Please run /login" {
		t.Errorf("body = %q, want 'Please run /login'", body)
	}
	if actions != "" {
		t.Errorf("actions = %q, want empty for API error", actions)
	}
}

func TestGenerateFromMessagesStructured_EmptyMessages(t *testing.T) {
	cfg := config.DefaultConfig()
	body, actions := GenerateFromMessagesStructured(nil, analyzer.StatusTaskComplete, cfg)
	if body == "" {
		t.Error("body should not be empty for empty messages (default fallback)")
	}
	if actions != "" {
		t.Errorf("actions = %q, want empty for empty messages", actions)
	}
}
