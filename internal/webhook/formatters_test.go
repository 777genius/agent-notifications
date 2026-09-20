package webhook

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
)

// makeCtx builds a SendContext for tests with the legacy fields populated.
// Tests that exercise structured fields populate them explicitly.
func makeCtx(status analyzer.Status, message, sessionID string) SendContext {
	return SendContext{
		Status:    status,
		Message:   message,
		SessionID: sessionID,
	}
}

func TestSlackFormatterFormat(t *testing.T) {
	formatter := &SlackFormatter{}
	statusInfo := config.StatusInfo{
		Title: "Task Complete",
	}

	result, err := formatter.Format(
		makeCtx(analyzer.StatusTaskComplete, "The task has been completed successfully", "session-123"),
		statusInfo,
	)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify structure
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("Result should be a map")
	}

	attachments, ok := resultMap["attachments"].([]map[string]interface{})
	if !ok || len(attachments) == 0 {
		t.Fatal("Should have attachments array")
	}

	attachment := attachments[0]

	// Check color
	color, ok := attachment["color"].(string)
	if !ok || color != "#28a745" {
		t.Errorf("Expected green color #28a745, got %v", color)
	}

	// Check title
	title, ok := attachment["title"].(string)
	if !ok || title != "Task Complete" {
		t.Errorf("Expected title 'Task Complete', got %v", title)
	}

	// Check text
	text, ok := attachment["text"].(string)
	if !ok || text != "The task has been completed successfully" {
		t.Errorf("Expected message text, got %v", text)
	}

	// Check footer contains session ID
	footer, ok := attachment["footer"].(string)
	if !ok || !strings.Contains(footer, "session-123") {
		t.Errorf("Footer should contain session ID, got %v", footer)
	}
	if got := attachment["footer_icon"]; got != agentNotificationsLogoURL {
		t.Errorf("footer_icon = %v, want %q", got, agentNotificationsLogoURL)
	}

	// Verify it's valid JSON
	data, err := json.Marshal(result)
	if err != nil {
		t.Errorf("Result should be JSON-serializable: %v", err)
	}
	if len(data) == 0 {
		t.Error("JSON data should not be empty")
	}
}

func TestSlackFormatterColors(t *testing.T) {
	formatter := &SlackFormatter{}
	statusInfo := config.StatusInfo{Title: "Test"}

	tests := []struct {
		status        analyzer.Status
		expectedColor string
	}{
		{analyzer.StatusTaskComplete, "#28a745"},
		{analyzer.StatusReviewComplete, "#17a2b8"},
		{analyzer.StatusQuestion, "#ffc107"},
		{analyzer.StatusPlanReady, "#007bff"},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			result, err := formatter.Format(makeCtx(tt.status, "test", "session-1"), statusInfo)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			resultMap := result.(map[string]interface{})
			attachments := resultMap["attachments"].([]map[string]interface{})
			color := attachments[0]["color"].(string)

			if color != tt.expectedColor {
				t.Errorf("Expected color %s for %s, got %s", tt.expectedColor, tt.status, color)
			}
		})
	}
}

func TestDiscordFormatterFormat(t *testing.T) {
	formatter := &DiscordFormatter{}
	statusInfo := config.StatusInfo{
		Title: "Question",
	}

	result, err := formatter.Format(
		makeCtx(analyzer.StatusQuestion, "What should we do next?", "session-456"),
		statusInfo,
	)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify structure
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("Result should be a map")
	}

	// Check username
	username, ok := resultMap["username"].(string)
	if !ok || username != "Claude Code" {
		t.Errorf("Expected username 'Claude Code', got %v", username)
	}
	if got := resultMap["avatar_url"]; got != agentNotificationsLogoURL {
		t.Errorf("avatar_url = %v, want %q", got, agentNotificationsLogoURL)
	}

	// Check embeds
	embeds, ok := resultMap["embeds"].([]map[string]interface{})
	if !ok || len(embeds) == 0 {
		t.Fatal("Should have embeds array")
	}

	embed := embeds[0]

	// Check title
	title, ok := embed["title"].(string)
	if !ok || title != "Question" {
		t.Errorf("Expected title 'Question', got %v", title)
	}

	// Check description
	desc, ok := embed["description"].(string)
	if !ok || desc != "What should we do next?" {
		t.Errorf("Expected description text, got %v", desc)
	}

	// Check color is integer
	color, ok := embed["color"].(int)
	if !ok {
		t.Errorf("Color should be integer, got %T", embed["color"])
	}
	if color != 0xffc107 {
		t.Errorf("Expected yellow color 0xffc107, got 0x%x", color)
	}

	// Check footer
	footer, ok := embed["footer"].(map[string]interface{})
	if !ok {
		t.Fatal("Should have footer map")
	}

	footerText, ok := footer["text"].(string)
	if !ok {
		t.Fatalf("Footer text missing or wrong type: %T", footer["text"])
	}
	// Footer carries the raw session ID so it doesn't duplicate the friendly
	// label that already appears in the author line.
	if !strings.Contains(footerText, "session-456") {
		t.Errorf("Footer text should contain raw session ID, got %q", footerText)
	}

	// Verify JSON serializable
	data, err := json.Marshal(result)
	if err != nil {
		t.Errorf("Result should be JSON-serializable: %v", err)
	}
	if len(data) == 0 {
		t.Error("JSON data should not be empty")
	}
}

func TestDiscordFormatterColors(t *testing.T) {
	formatter := &DiscordFormatter{}
	statusInfo := config.StatusInfo{Title: "Test"}

	tests := []struct {
		status        analyzer.Status
		expectedColor int
	}{
		{analyzer.StatusTaskComplete, 0x28a745},
		{analyzer.StatusReviewComplete, 0x17a2b8},
		{analyzer.StatusQuestion, 0xffc107},
		{analyzer.StatusPlanReady, 0x007bff},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			result, err := formatter.Format(makeCtx(tt.status, "test", "session-1"), statusInfo)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			resultMap := result.(map[string]interface{})
			embeds := resultMap["embeds"].([]map[string]interface{})
			color := embeds[0]["color"].(int)

			if color != tt.expectedColor {
				t.Errorf("Expected color 0x%x for %s, got 0x%x", tt.expectedColor, tt.status, color)
			}
		})
	}
}

func TestTelegramFormatterFormat(t *testing.T) {
	formatter := &TelegramFormatter{ChatID: "123456789"}
	statusInfo := config.StatusInfo{
		Title: "Review Complete",
	}

	result, err := formatter.Format(
		makeCtx(analyzer.StatusReviewComplete, "Code review finished", "session-789"),
		statusInfo,
	)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify structure
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("Result should be a map")
	}

	// Check chat_id
	chatID, ok := resultMap["chat_id"].(string)
	if !ok || chatID != "123456789" {
		t.Errorf("Expected chat_id '123456789', got %v", chatID)
	}

	// Check parse_mode
	parseMode, ok := resultMap["parse_mode"].(string)
	if !ok || parseMode != "HTML" {
		t.Errorf("Expected parse_mode 'HTML', got %v", parseMode)
	}

	// Check text contains HTML formatting
	text, ok := resultMap["text"].(string)
	if !ok {
		t.Fatal("Should have text field")
	}

	if !strings.Contains(text, "<b>") {
		t.Error("Text should contain HTML bold tags")
	}

	if !strings.Contains(text, "Review Complete") {
		t.Error("Text should contain title")
	}

	if !strings.Contains(text, "Code review finished") {
		t.Error("Text should contain message")
	}

	if !strings.Contains(text, "session-789") {
		t.Error("Text should contain session ID")
	}

	if !strings.Contains(text, "<i>") {
		t.Error("Text should contain HTML italic tags for session")
	}

	// Verify JSON serializable
	data, err := json.Marshal(result)
	if err != nil {
		t.Errorf("Result should be JSON-serializable: %v", err)
	}
	if len(data) == 0 {
		t.Error("JSON data should not be empty")
	}
}

func TestTelegramFormatterEmojis(t *testing.T) {
	formatter := &TelegramFormatter{ChatID: "123"}
	statusInfo := config.StatusInfo{Title: "Test"}

	tests := []struct {
		status        analyzer.Status
		expectedEmoji string
	}{
		{analyzer.StatusTaskComplete, "✅"},
		{analyzer.StatusReviewComplete, "🔍"},
		{analyzer.StatusQuestion, "❓"},
		{analyzer.StatusPlanReady, "📋"},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			result, err := formatter.Format(makeCtx(tt.status, "test", "session-1"), statusInfo)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			resultMap := result.(map[string]interface{})
			text := resultMap["text"].(string)

			if !strings.Contains(text, tt.expectedEmoji) {
				t.Errorf("Expected emoji %s for %s in text: %s", tt.expectedEmoji, tt.status, text)
			}
		})
	}
}

func TestGetColorForStatus(t *testing.T) {
	tests := []struct {
		status   analyzer.Status
		expected string
	}{
		{analyzer.StatusTaskComplete, "#28a745"},
		{analyzer.StatusReviewComplete, "#17a2b8"},
		{analyzer.StatusQuestion, "#ffc107"},
		{analyzer.StatusPlanReady, "#007bff"},
		{analyzer.Status("unknown"), "#6c757d"},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			result := getColorForStatus(tt.status)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestGetDiscordColorInt(t *testing.T) {
	tests := []struct {
		status   analyzer.Status
		expected int
	}{
		{analyzer.StatusTaskComplete, 0x28a745},
		{analyzer.StatusReviewComplete, 0x17a2b8},
		{analyzer.StatusQuestion, 0xffc107},
		{analyzer.StatusPlanReady, 0x007bff},
		{analyzer.Status("unknown"), 0x6c757d},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			result := getDiscordColorInt(tt.status)
			if result != tt.expected {
				t.Errorf("Expected 0x%x, got 0x%x", tt.expected, result)
			}
		})
	}
}

func TestGetEmojiForStatus(t *testing.T) {
	tests := []struct {
		status   analyzer.Status
		expected string
	}{
		{analyzer.StatusTaskComplete, "✅"},
		{analyzer.StatusReviewComplete, "🔍"},
		{analyzer.StatusQuestion, "❓"},
		{analyzer.StatusPlanReady, "📋"},
		{analyzer.Status("unknown"), "ℹ️"},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			result := getEmojiForStatus(tt.status)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestLarkFormatterFormat(t *testing.T) {
	formatter := &LarkFormatter{}
	statusInfo := config.StatusInfo{
		Title: "Task Complete",
	}

	result, err := formatter.Format(
		makeCtx(analyzer.StatusTaskComplete, "The task has been completed successfully", "session-123"),
		statusInfo,
	)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify structure
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("Result should be a map")
	}

	// Check msg_type
	msgType, ok := resultMap["msg_type"].(string)
	if !ok || msgType != "interactive" {
		t.Errorf("Expected msg_type 'interactive', got %v", msgType)
	}

	// Check card
	card, ok := resultMap["card"].(map[string]interface{})
	if !ok {
		t.Fatal("Should have card map")
	}

	// Check config
	config, ok := card["config"].(map[string]interface{})
	if !ok {
		t.Fatal("Should have config map")
	}

	wideScreen, ok := config["wide_screen_mode"].(bool)
	if !ok || !wideScreen {
		t.Errorf("Expected wide_screen_mode true, got %v", wideScreen)
	}

	// Check header
	header, ok := card["header"].(map[string]interface{})
	if !ok {
		t.Fatal("Should have header map")
	}

	title, ok := header["title"].(map[string]interface{})
	if !ok {
		t.Fatal("Header should have title map")
	}

	titleTag, ok := title["tag"].(string)
	if !ok || titleTag != "plain_text" {
		t.Errorf("Expected title tag 'plain_text', got %v", titleTag)
	}

	titleContent, ok := title["content"].(string)
	if !ok || titleContent != "Task Complete" {
		t.Errorf("Expected title 'Task Complete', got %v", titleContent)
	}

	// Check template color
	template, ok := header["template"].(string)
	if !ok || template != "green" {
		t.Errorf("Expected template 'green' for task_complete, got %v", template)
	}

	// Check elements
	elements, ok := card["elements"].([]map[string]interface{})
	if !ok || len(elements) != 3 {
		t.Fatalf("Expected 3 elements, got %d", len(elements))
	}

	// Check first element (message div)
	msgDiv := elements[0]
	if msgDiv["tag"] != "div" {
		t.Errorf("Expected first element tag 'div', got %v", msgDiv["tag"])
	}

	msgText, ok := msgDiv["text"].(map[string]interface{})
	if !ok {
		t.Fatal("Message div should have text map")
	}

	msgContent, ok := msgText["content"].(string)
	if !ok || msgContent != "The task has been completed successfully" {
		t.Errorf("Expected message content, got %v", msgContent)
	}

	// Verify it's valid JSON
	data, err := json.Marshal(result)
	if err != nil {
		t.Errorf("Result should be JSON-serializable: %v", err)
	}
	if len(data) == 0 {
		t.Error("JSON data should not be empty")
	}
}

func TestLarkFormatterColors(t *testing.T) {
	formatter := &LarkFormatter{}
	statusInfo := config.StatusInfo{Title: "Test"}

	tests := []struct {
		status           analyzer.Status
		expectedTemplate string
	}{
		{analyzer.StatusTaskComplete, "green"},
		{analyzer.StatusReviewComplete, "yellow"},
		{analyzer.StatusQuestion, "red"},
		{analyzer.StatusPlanReady, "blue"},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			result, err := formatter.Format(makeCtx(tt.status, "test", "session-1"), statusInfo)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			resultMap := result.(map[string]interface{})
			card := resultMap["card"].(map[string]interface{})
			header := card["header"].(map[string]interface{})
			template := header["template"].(string)

			if template != tt.expectedTemplate {
				t.Errorf("Expected template %s for %s, got %s", tt.expectedTemplate, tt.status, template)
			}
		})
	}
}

func TestLarkFormatterUnknownStatus(t *testing.T) {
	formatter := &LarkFormatter{}
	statusInfo := config.StatusInfo{Title: "Unknown"}

	result, err := formatter.Format(
		makeCtx(analyzer.Status("unknown"), "Unknown status", "session-999"),
		statusInfo,
	)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	resultMap := result.(map[string]interface{})
	card := resultMap["card"].(map[string]interface{})
	header := card["header"].(map[string]interface{})
	template := header["template"].(string)

	if template != "grey" {
		t.Errorf("Expected template 'grey' for unknown status, got %s", template)
	}
}

func TestGetLarkColorTemplate(t *testing.T) {
	tests := []struct {
		status   analyzer.Status
		expected string
	}{
		{analyzer.StatusTaskComplete, "green"},
		{analyzer.StatusReviewComplete, "yellow"},
		{analyzer.StatusQuestion, "red"},
		{analyzer.StatusPlanReady, "blue"},
		{analyzer.Status("unknown"), "grey"},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			result := getLarkColorTemplate(tt.status)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

// === Tests for Discord native embed layout ===

func TestDiscordFormatter_AuthorAndFields(t *testing.T) {
	formatter := &DiscordFormatter{}
	statusInfo := config.StatusInfo{Title: "✅ Completed"}

	ctx := SendContext{
		Status:        analyzer.StatusTaskComplete,
		Message:       "[phoenix 439d1884|main claude-utils] Done. 📝 1 new  ▶ 2 cmds  ⏱ 41s",
		SessionID:     "439d1884-b53d-42f2-922a-203d086a158d",
		CWD:           "/work/claude-utils",
		SessionName:   "phoenix 439d1884",
		GitBranch:     "main",
		Folder:        "claude-utils",
		RawBody:       "Done.",
		ActionSummary: "📝 1 new  ▶ 2 cmds  ⏱ 41s",
	}

	result, err := formatter.Format(ctx, statusInfo)
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}

	embed := result.(map[string]interface{})["embeds"].([]map[string]interface{})[0]

	author, ok := embed["author"].(map[string]interface{})
	if !ok {
		t.Fatalf("embed should have author map, got %T", embed["author"])
	}
	if name := author["name"].(string); name != "phoenix 439d1884 · claude-utils (main)" {
		t.Errorf("author.name = %q, want %q", name, "phoenix 439d1884 · claude-utils (main)")
	}

	if desc := embed["description"].(string); desc != "Done." {
		t.Errorf("description = %q, want %q", desc, "Done.")
	}

	fields, ok := embed["fields"].([]map[string]interface{})
	if !ok {
		t.Fatalf("embed should have fields slice, got %T", embed["fields"])
	}
	if len(fields) != 3 {
		t.Fatalf("fields count = %d, want 3 (📝/▶/⏱)", len(fields))
	}
	if fields[0]["name"] != "New" || fields[0]["value"] != "1 new" {
		t.Errorf("fields[0] = %v, want New/1 new", fields[0])
	}
	if fields[1]["name"] != "Commands" || fields[1]["value"] != "2 cmds" {
		t.Errorf("fields[1] = %v, want Commands/2 cmds", fields[1])
	}
	if fields[2]["name"] != "Duration" || fields[2]["value"] != "41s" {
		t.Errorf("fields[2] = %v, want Duration/41s", fields[2])
	}
	for i, f := range fields {
		if inline, _ := f["inline"].(bool); !inline {
			t.Errorf("fields[%d] should be inline", i)
		}
	}

	footer := embed["footer"].(map[string]interface{})
	wantFooter := "Session: 439d1884-b53d-42f2-922a-203d086a158d"
	if text := footer["text"].(string); text != wantFooter {
		t.Errorf("footer = %q, want %q", text, wantFooter)
	}

	if _, err := json.Marshal(result); err != nil {
		t.Errorf("payload not JSON-serializable: %v", err)
	}
}

func TestDiscordFormatter_NoGitBranch(t *testing.T) {
	formatter := &DiscordFormatter{}
	ctx := SendContext{
		Status:      analyzer.StatusTaskComplete,
		Message:     "Done.",
		SessionID:   "439d1884-b53d-42f2-922a-203d086a158d",
		SessionName: "phoenix 439d1884",
		Folder:      "claude-utils",
		RawBody:     "Done.",
	}

	result, err := formatter.Format(ctx, config.StatusInfo{Title: "✅"})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}

	embed := result.(map[string]interface{})["embeds"].([]map[string]interface{})[0]
	author := embed["author"].(map[string]interface{})
	if name := author["name"].(string); name != "phoenix 439d1884 · claude-utils" {
		t.Errorf("author.name = %q, want no branch suffix", name)
	}
}

func TestDiscordFormatter_AuthorRespectsDiscordLimit(t *testing.T) {
	formatter := &DiscordFormatter{}
	longFolder := strings.Repeat("folder", 30)
	longBranch := strings.Repeat("branch", 30)
	ctx := SendContext{
		Status:      analyzer.StatusTaskComplete,
		Message:     "Done.",
		SessionID:   "439d1884-b53d-42f2-922a-203d086a158d",
		SessionName: "phoenix 439d1884",
		Folder:      longFolder,
		GitBranch:   longBranch,
		RawBody:     "Done.",
	}

	result, err := formatter.Format(ctx, config.StatusInfo{Title: "✅"})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}

	embed := result.(map[string]interface{})["embeds"].([]map[string]interface{})[0]
	author := embed["author"].(map[string]interface{})
	name := author["name"].(string)

	if got := len([]rune(name)); got > discordEmbedAuthorLimit {
		t.Fatalf("author.name length = %d, want <= %d", got, discordEmbedAuthorLimit)
	}
	if !strings.HasPrefix(name, "phoenix 439d1884") {
		t.Errorf("author.name should preserve the prefix, got %q", name)
	}
	wantTail := (longBranch + ")")[len(longBranch+")")-20:]
	if !strings.HasSuffix(name, wantTail) {
		t.Errorf("author.name should preserve the tail, got %q", name)
	}
	if !strings.Contains(name, "...") {
		t.Errorf("author.name should contain ellipsis when truncated, got %q", name)
	}
}

func TestDiscordFormatter_NoActionSummaryOmitsFields(t *testing.T) {
	formatter := &DiscordFormatter{}
	ctx := SendContext{
		Status:      analyzer.StatusQuestion,
		Message:     "Pick one.",
		SessionID:   "439d1884-b53d-42f2-922a-203d086a158d",
		SessionName: "phoenix 439d1884",
		RawBody:     "Pick one.",
	}

	result, err := formatter.Format(ctx, config.StatusInfo{Title: "❓"})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}

	embed := result.(map[string]interface{})["embeds"].([]map[string]interface{})[0]
	if _, present := embed["fields"]; present {
		t.Errorf("embed should not contain fields key when ActionSummary is empty")
	}
}

func TestDiscordFormatter_FallsBackToMessageWhenRawBodyEmpty(t *testing.T) {
	formatter := &DiscordFormatter{}
	ctx := SendContext{
		Status:    analyzer.StatusTaskComplete,
		Message:   "legacy joined message",
		SessionID: "439d1884-b53d-42f2-922a-203d086a158d",
		// SessionName / RawBody intentionally empty (legacy callers).
	}

	result, err := formatter.Format(ctx, config.StatusInfo{Title: "✅"})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}

	embed := result.(map[string]interface{})["embeds"].([]map[string]interface{})[0]
	if desc := embed["description"].(string); desc != "legacy joined message" {
		t.Errorf("description = %q, want fallback to Message", desc)
	}
}

func TestParseActionSummary(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []map[string]interface{}
	}{
		{
			name: "empty",
			in:   "",
			want: nil,
		},
		{
			name: "all known",
			in:   "📝 1 new  ✏️ 2 edited  ▶ 3 cmds  ⏱ 12s",
			want: []map[string]interface{}{
				{"name": "New", "value": "1 new", "inline": true},
				{"name": "Edited", "value": "2 edited", "inline": true},
				{"name": "Commands", "value": "3 cmds", "inline": true},
				{"name": "Duration", "value": "12s", "inline": true},
			},
		},
		{
			name: "unknown emoji collected",
			in:   "📝 1 new  🐛 1 bug",
			want: []map[string]interface{}{
				{"name": "New", "value": "1 new", "inline": true},
				{"name": "Details", "value": "🐛 1 bug", "inline": true},
			},
		},
		{
			// Discord rejects fields with empty value (HTTP 400). Bare emoji
			// segments (no trailing data) must be skipped, not emitted with
			// an empty value.
			name: "bare emoji skipped",
			in:   "📝  ▶ 3 cmds",
			want: []map[string]interface{}{
				{"name": "Commands", "value": "3 cmds", "inline": true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseActionSummary(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d (got %v)", len(got), len(tt.want), got)
			}
			for i := range got {
				for k, v := range tt.want[i] {
					if got[i][k] != v {
						t.Errorf("field[%d].%s = %v, want %v", i, k, got[i][k], v)
					}
				}
			}
		})
	}
}

func TestAgentDisplayName(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"empty defaults to claude", "", "Claude Code"},
		{"claude", "claude", "Claude Code"},
		{"codex", "codex", "Codex"},
		{
			name:   "unknown future agent falls back to the raw source, not Claude Code",
			source: "gemini",
			want:   "gemini",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentDisplayName(tt.source); got != tt.want {
				t.Errorf("agentDisplayName(%q) = %q, want %q", tt.source, got, tt.want)
			}
		})
	}
}
