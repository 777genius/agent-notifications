package summary

import (
	"fmt"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/pkg/jsonl"
)

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
