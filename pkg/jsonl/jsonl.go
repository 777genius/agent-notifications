package jsonl

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"time"
)

// Message represents a Claude Code transcript message
type Message struct {
	ParentUUID        string         `json:"parentUuid"`
	Type              string         `json:"type"`
	Message           MessageContent `json:"message"`
	Timestamp         string         `json:"timestamp"`
	IsApiErrorMessage bool           `json:"isApiErrorMessage,omitempty"`
	Error             string         `json:"error,omitempty"`
	// Origin says who produced a user record. Claude Code writes it on user
	// input records (for example {"kind":"human"} for typed input); older
	// versions omit it.
	Origin *Origin `json:"origin,omitempty"`
	// IsCompactSummary marks the summary record written when a conversation
	// is compacted; it continues the turn that was running, not a new one.
	IsCompactSummary bool `json:"isCompactSummary,omitempty"`
}

// Origin identifies the producer of a user record.
type Origin struct {
	Kind string `json:"kind,omitempty"`
}

// OriginKindHuman is the origin kind Claude Code writes for typed user input.
const OriginKindHuman = "human"

// MessageContent represents the content of a message
// Content can be either a string (user text messages) or an array (tool results, assistant messages)
type MessageContent struct {
	Role          string    `json:"role"`
	Content       []Content `json:"-"` // Array content (tool_result, assistant messages)
	ContentString string    `json:"-"` // String content (user text messages)
}

// Content represents a content block in a message
type Content struct {
	Type  string                 `json:"type"`
	Name  string                 `json:"name,omitempty"`
	Text  string                 `json:"text,omitempty"`
	Input map[string]interface{} `json:"input,omitempty"`
}

// UnmarshalJSON implements custom JSON unmarshaling for MessageContent
// Handles both string content (user text messages) and array content (tool results, assistant messages)
func (m *MessageContent) UnmarshalJSON(data []byte) error {
	// Create an alias to avoid recursion
	type Alias MessageContent
	aux := &struct {
		Content json.RawMessage `json:"content"`
		*Alias
	}{
		Alias: (*Alias)(m),
	}

	// Unmarshal everything except content
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Try to unmarshal content as a string (user text messages)
	var str string
	if err := json.Unmarshal(aux.Content, &str); err == nil {
		m.ContentString = str
		return nil
	}

	// Try to unmarshal content as an array (tool results, assistant messages)
	var arr []Content
	if err := json.Unmarshal(aux.Content, &arr); err == nil {
		m.Content = arr
		return nil
	}

	// Content is neither string nor array (or is null/empty), that's okay
	return nil
}

// MarshalJSON implements custom JSON marshaling for MessageContent
// Outputs content as string if ContentString is set, otherwise as array
func (m MessageContent) MarshalJSON() ([]byte, error) {
	// Create auxiliary struct with content as interface{}
	aux := &struct {
		Role    string      `json:"role"`
		Content interface{} `json:"content,omitempty"`
	}{
		Role: m.Role,
	}

	// Choose content format based on which field is set
	if m.ContentString != "" {
		aux.Content = m.ContentString
	} else if len(m.Content) > 0 {
		aux.Content = m.Content
	}

	return json.Marshal(aux)
}

// ParseFile parses a JSONL file and returns all messages
func ParseFile(path string) ([]Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	return Parse(f)
}

// Parse parses JSONL from a reader and returns all messages.
// Uses bufio.Reader instead of bufio.Scanner to handle arbitrarily long lines
// (e.g. base64-encoded images, large code diffs in Claude Code transcripts).
func Parse(r io.Reader) ([]Message, error) {
	var messages []Message
	reader := bufio.NewReaderSize(r, 64*1024) // 64KB initial buffer

	for {
		line, err := reader.ReadBytes('\n')
		// Process line even if err != nil (last line may lack trailing newline)
		line = bytes.TrimRight(line, "\r\n")
		if len(line) > 0 {
			var msg Message
			if jsonErr := json.Unmarshal(line, &msg); jsonErr == nil {
				messages = append(messages, msg)
			}
			// Skip invalid JSON lines instead of failing
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
	}

	return messages, nil
}

// GetLastApiErrorMessages returns the last N messages with isApiErrorMessage=true
func GetLastApiErrorMessages(messages []Message, count int) []Message {
	var errorMessages []Message
	for _, msg := range messages {
		if msg.IsApiErrorMessage {
			errorMessages = append(errorMessages, msg)
		}
	}

	if len(errorMessages) <= count {
		return errorMessages
	}
	return errorMessages[len(errorMessages)-count:]
}

// HasRecentApiError checks if there are API error messages after the last user message
func HasRecentApiError(messages []Message) bool {
	lastUserTS := GetLastUserTimestamp(messages)

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.IsApiErrorMessage {
			// If no user timestamp, any API error counts
			if lastUserTS == "" {
				return true
			}
			// Check if error is after last user message
			if msg.Timestamp >= lastUserTS {
				return true
			}
		}
	}
	return false
}

// GetLastAssistantMessages returns the last N assistant messages
func GetLastAssistantMessages(messages []Message, count int) []Message {
	var assistantMessages []Message
	for _, msg := range messages {
		if msg.Type == "assistant" {
			assistantMessages = append(assistantMessages, msg)
		}
	}

	// Return last N messages
	if len(assistantMessages) <= count {
		return assistantMessages
	}
	return assistantMessages[len(assistantMessages)-count:]
}

// ExtractTools extracts all tools from messages with their positions
func ExtractTools(messages []Message) []ToolUse {
	var tools []ToolUse

	for pos, msg := range messages {
		for _, content := range msg.Message.Content {
			if content.Type == "tool_use" {
				tools = append(tools, ToolUse{
					Position: pos,
					Name:     content.Name,
				})
			}
		}
	}

	return tools
}

// ToolUse represents a tool use with its position
type ToolUse struct {
	Position int
	Name     string
}

// GetLastTool returns the last tool used, or empty string if none
func GetLastTool(tools []ToolUse) string {
	if len(tools) == 0 {
		return ""
	}
	return tools[len(tools)-1].Name
}

// CountToolsAfterPosition counts how many tools were used after a given position
func CountToolsAfterPosition(tools []ToolUse, position int) int {
	count := 0
	for _, tool := range tools {
		if tool.Position > position {
			count++
		}
	}
	return count
}

// FindToolPosition finds the position of a tool by name (last occurrence)
// Returns -1 if not found
func FindToolPosition(tools []ToolUse, name string) int {
	position := -1
	for _, tool := range tools {
		if tool.Name == name {
			position = tool.Position
		}
	}
	return position
}

// ExtractTextFromMessages extracts all text content from messages
func ExtractTextFromMessages(messages []Message) []string {
	var texts []string

	for _, msg := range messages {
		for _, content := range msg.Message.Content {
			if content.Type == "text" && content.Text != "" {
				texts = append(texts, content.Text)
			}
		}
	}

	return texts
}

// FindLastToolUse finds the last occurrence of a specific tool use in messages
// Returns nil if not found
func FindLastToolUse(messages []Message, toolName string) *Content {
	var lastTool *Content

	for _, msg := range messages {
		if msg.Type != "assistant" {
			continue
		}
		for i := range msg.Message.Content {
			if msg.Message.Content[i].Type == "tool_use" && msg.Message.Content[i].Name == toolName {
				lastTool = &msg.Message.Content[i]
			}
		}
	}

	return lastTool
}

// ExtractToolInput extracts the input parameters from a specific tool use
// Returns empty map if tool not found
func ExtractToolInput(messages []Message, toolName string) map[string]interface{} {
	tool := FindLastToolUse(messages, toolName)
	if tool == nil {
		return make(map[string]interface{})
	}
	return tool.Input
}

// GetLastUserTimestamp returns the timestamp of the last user message with text content
// Includes both string content (normal user messages) and array content with type="text" (interrupted tool use)
// Excludes tool_result messages
func GetLastUserTimestamp(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Type == "user" {
			// Check for string content (normal user text messages)
			if msg.Message.ContentString != "" {
				return msg.Timestamp
			}
			// Check for array content with type="text" (interrupted tool use: "[Request interrupted by user for tool use]")
			if len(msg.Message.Content) > 0 && msg.Message.Content[0].Type == "text" {
				return msg.Timestamp
			}
		}
	}
	return ""
}

// TurnOrigin reports who started the current turn: the origin kind of the
// input that Claude was responding to. known is false when no user record in
// the transcript carries an origin kind, which is the case for transcripts
// written before Claude Code recorded origins.
//
// A turn's input is the batch of user records (not tool results) between the
// previous assistant record and the next one. Claude Code also writes user
// records inside a running turn: skill instructions and compaction summaries
// after a tool result, queued notifications. A batch that follows a tool
// result, or that holds only a compaction summary, continues an earlier turn,
// so the search moves on to the batch before it. Within the batch that
// started the turn, any human input makes the turn human; otherwise kind is
// the newest origin kind in it, or "" when none of its records carries one.
func TurnOrigin(messages []Message) (kind string, known bool) {
	for i := range messages {
		if isUserInput(messages[i]) && messages[i].Origin != nil && messages[i].Origin.Kind != "" {
			known = true
			break
		}
	}
	if !known {
		return "", false
	}

	i := len(messages) - 1
	for {
		// Find the newest user input at or before i.
		for i >= 0 && !isUserInput(messages[i]) {
			i--
		}
		if i < 0 {
			return "", true
		}

		// Collect the batch: user inputs back to the previous assistant
		// record or tool result. Other record types are skipped.
		kind = ""
		human, compactSummary := false, false
		for ; i >= 0; i-- {
			msg := messages[i]
			if msg.Type == "assistant" || isToolResult(msg) {
				break
			}
			if !isUserInput(msg) {
				continue
			}
			if msg.IsCompactSummary {
				compactSummary = true
			}
			if msg.Origin != nil && msg.Origin.Kind != "" {
				if msg.Origin.Kind == OriginKindHuman {
					human = true
				}
				if kind == "" {
					kind = msg.Origin.Kind
				}
			}
		}
		if human {
			return OriginKindHuman, true
		}
		midTurn := i >= 0 && isToolResult(messages[i])
		if !midTurn && (kind != "" || !compactSummary) {
			return kind, true
		}
		// The batch continues an earlier turn; inspect the batch before it.
	}
}

// isToolResult reports whether msg is a user record carrying tool results.
func isToolResult(msg Message) bool {
	return msg.Type == "user" && len(msg.Message.Content) > 0 && msg.Message.Content[0].Type == "tool_result"
}

// isUserInput reports whether msg is a user record that is not a tool result.
func isUserInput(msg Message) bool {
	if msg.Type != "user" || isToolResult(msg) {
		return false
	}
	return msg.Message.ContentString != "" || len(msg.Message.Content) > 0
}

// GetLastAssistantTimestamp returns the timestamp of the last assistant message
func GetLastAssistantTimestamp(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Type == "assistant" {
			return messages[i].Timestamp
		}
	}
	return ""
}

// FilterMessagesAfterTimestamp filters messages that occurred after given timestamp
// Returns only assistant messages after the timestamp
// This is used to filter messages to only those in the current response (after last user message)
func FilterMessagesAfterTimestamp(messages []Message, afterTimestamp string) []Message {
	if afterTimestamp == "" {
		// No user message - return all assistant messages
		return filterAssistantMessages(messages)
	}

	// Parse the timestamp
	afterTime, err := time.Parse(time.RFC3339, afterTimestamp)
	if err != nil {
		// Invalid timestamp - return all assistant messages
		return filterAssistantMessages(messages)
	}

	var filtered []Message
	for _, msg := range messages {
		if msg.Type != "assistant" {
			continue
		}

		if msg.Timestamp == "" {
			continue
		}

		msgTime, err := time.Parse(time.RFC3339, msg.Timestamp)
		if err != nil {
			continue
		}

		// Include only messages AFTER user message
		if msgTime.After(afterTime) {
			filtered = append(filtered, msg)
		}
	}

	return filtered
}

// filterAssistantMessages returns only assistant messages from the list
func filterAssistantMessages(messages []Message) []Message {
	var filtered []Message
	for _, msg := range messages {
		if msg.Type == "assistant" {
			filtered = append(filtered, msg)
		}
	}
	return filtered
}

// CountToolsByNames counts tools matching any of the given names
func CountToolsByNames(tools []ToolUse, names []string) int {
	count := 0
	for _, tool := range tools {
		for _, name := range names {
			if tool.Name == name {
				count++
			}
		}
	}
	return count
}

// HasAnyActiveTool checks if any active tool was used
func HasAnyActiveTool(tools []ToolUse, activeTools []string) bool {
	for _, tool := range tools {
		for _, active := range activeTools {
			if tool.Name == active {
				return true
			}
		}
	}
	return false
}

// ExtractRecentText extracts concatenated text from last N assistant messages
func ExtractRecentText(messages []Message, count int) string {
	recentMessages := GetLastAssistantMessages(messages, count)
	texts := ExtractTextFromMessages(recentMessages)

	// Join all texts with spaces
	var result string
	for i, text := range texts {
		if i > 0 {
			result += " "
		}
		result += text
	}

	return result
}
