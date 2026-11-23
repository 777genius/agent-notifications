package hooks

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	claudePermissionTailBytes = 256 * 1024
	claudePermissionMaxRecord = 64 * 1024
	claudePermissionMaxAge = 45 * time.Second
)

// claudePermissionRecentTool is only an optional, advisory label for Claude's
// generic delayed permission notification. A transcript does not identify the
// currently displayed permission dialog; never present this as the exact
// command being approved, and never render tool_input or tool output.
func claudePermissionRecentTool(session SessionContext) string {
	if session.SessionID == "" || session.TranscriptPath == "" ||
		!filepath.IsAbs(session.TranscriptPath) ||
		filepath.Clean(session.TranscriptPath) != session.TranscriptPath ||
		filepath.Base(session.TranscriptPath) != session.SessionID+".jsonl" {
		return ""
	}
	before, err := os.Lstat(session.TranscriptPath)
	if err != nil || !before.Mode().IsRegular() || before.Size() == 0 {
		return ""
	}
	file, err := os.Open(session.TranscriptPath)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() || !os.SameFile(before, stat) || stat.Size() != before.Size() {
		return ""
	}
	// A truncated transcript can hide a still-unresolved older call. Do
	// not claim uniqueness unless the entire session is within the read cap.
	if stat.Size() > claudePermissionTailBytes {
		return ""
	}
	tail := make([]byte, int(stat.Size()))
	n, err := file.ReadAt(tail, 0)
	if (err != nil && err != io.EOF) || n != len(tail) {
		return ""
	}
	// The last record may still be being written. An incomplete tail must
	// never make an older tool appear to be the only pending operation.
	if len(tail) == 0 || tail[len(tail)-1] != '\n' {
		return ""
	}
	// A concurrent append would invalidate the snapshot. Keep the original
	// host message rather than guessing from unstable evidence.
	if after, err := file.Stat(); err != nil || after.Size() != before.Size() {
		return ""
	}
	completed := make(map[string]bool)
	type pendingTool struct {
		name string
		at   time.Time
	}
	pending := make(map[string]pendingTool)
	now := time.Now()
	lines := bytes.Split(tail, []byte{'\n'})
	for i := len(lines)-1; i >= 0; i-- {
		if len(lines[i]) == 0 {
			continue
		}
		if len(lines[i]) > claudePermissionMaxRecord {
			return ""
		}
		var record struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(lines[i], &record) != nil {
			// Bad JSON may conceal a second in-flight call or its result.
			// Fall back to Claude's exact message rather than silently skip.
			return ""
		}
		if record.SessionID != session.SessionID {
			continue
		}
		content := bytes.TrimSpace(record.Message.Content)
		if len(content) == 0 || bytes.Equal(content, []byte("null")) ||
			content[0] == '"' {
			// Claude user prompts may use a string rather than block array.
			continue
		}
		if content[0] != '[' {
			return ""
		}
		var blocks []struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			ToolUseID string `json:"tool_use_id"`
			Name      string `json:"name"`
		}
		if json.Unmarshal(content, &blocks) != nil {
			return ""
		}
		// Walk newest to oldest, so a later tool_result suppresses its
		// earlier tool_use even when both occur in one model turn.
		for _, item := range blocks {
			if item.Type == "tool_result" && item.ToolUseID != "" {
				completed[item.ToolUseID] = true
				delete(pending, item.ToolUseID)
			}
		}
		if record.Type != "assistant" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, record.Timestamp)
		if err != nil || at.After(now.Add(5*time.Second)) {
			at = time.Time{}
		}
		for _, item := range blocks {
			if item.Type != "tool_use" || item.ID == "" || completed[item.ID] {
				continue
			}
			// All unresolved tool calls count, even stale or unknown ones. An
			// older pending Bash must not make a recent Edit look unique.
			pending[item.ID] = pendingTool{name: safeClaudeToolLabel(item.Name), at: at}
		}
	}
	if len(pending) != 1 {
		return ""
	}
	for _, candidate := range pending {
		if candidate.at.IsZero() || now.Sub(candidate.at) > claudePermissionMaxAge {
			return ""
		}
		return candidate.name
	}
	return ""
}

// Restrict names to fixed non-sensitive tool identities. Dynamic MCP names,
// descriptions, shell input, file paths and transcript text never reach a banner.
func safeClaudeToolLabel(name string) string {
	switch name {
	case "Bash", "PowerShell", "Read", "Edit", "Write", "MultiEdit",
		"NotebookEdit", "Glob", "Grep", "WebFetch", "WebSearch", "Agent", "Task":
		return name
	default:
		return ""
	}
}

func claudePermissionBody(host string, session SessionContext) string {
	host = strings.Join(strings.Fields(host), " ")
	if isGenericClaudePermissionPrompt(host) {
		if tool := claudePermissionRecentTool(session); tool != "" {
			return truncateRunes(host+" (recent tool: "+tool+")", 180)
		}
	}
	return truncateRunes(host, 180)
}
