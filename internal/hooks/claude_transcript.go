package hooks

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/platform"
	"github.com/777genius/agent-notifications/internal/summary"
	"github.com/777genius/agent-notifications/pkg/jsonl"
)

// Claude Stop is skipped when the transcript has not been written for this
// long; a finished session resumed with no new turn must not re-notify.
const staleTranscriptSeconds = 10 * 60

const (
	transcriptSettleWait     = 500 * time.Millisecond
	transcriptSettleInterval = 25 * time.Millisecond
)

var settleSleepFunc = time.Sleep

// handleStopEvent handles Stop/SubagentStop hooks.
// Returns the parsed messages alongside the status so callers can reuse them
// (e.g., for summary generation) without re-reading the transcript file.
func (h *Handler) handleStopEvent(ev Event) (analyzer.Status, []jsonl.Message, error) {
	if ev.Session.TranscriptPath == "" {
		logging.Warn("Transcript path is empty, skipping notification")
		return analyzer.StatusUnknown, nil, nil
	}

	if !platform.FileExists(ev.Session.TranscriptPath) {
		logging.Warn("Transcript file not found: %s", ev.Session.TranscriptPath)
		return analyzer.StatusUnknown, nil, nil
	}

	status, messages, err := analyzer.AnalyzeTranscriptWithMessages(ev.Session.TranscriptPath, h.cfg)
	if err != nil {
		logging.Error("Failed to analyze transcript: %v", err)
		return analyzer.StatusUnknown, nil, nil
	}

	if h.afterClaudeTranscriptParse != nil {
		h.afterClaudeTranscriptParse()
	}
	logging.Debug("Analyzed status: %s", status)
	return status, messages, nil
}

// Claude Code supplies the final response in Stop even when the final JSONL
// record has not yet been written. Use that text for the body on every ordinary
// Stop, not only when transcript classification is unknown. The transcript
// remains authoritative for tool-driven status and action counts.
func (h *Handler) enrichClaudeStop(status analyzer.Status, message string, messages []jsonl.Message) (analyzer.Status, *TurnInsight) {
	message = strings.TrimSpace(message)
	if message == "" {
		return status, nil
	}
	if status == analyzer.StatusUnknown || status == analyzer.StatusTaskComplete || status == analyzer.StatusReviewComplete {
		if failure := classifyClaudeStopFailure(message); failure != analyzer.StatusUnknown {
			status = failure
		} else if status == analyzer.StatusUnknown {
			if !h.cfg.ShouldNotifyOnTextResponse() {
				return analyzer.StatusUnknown, nil
			}
			status = analyzer.StatusTaskComplete
		}
	}
	if status == analyzer.StatusTaskComplete && claudeStopIsReview(messages, message) {
		status = analyzer.StatusReviewComplete
	}
	if status != analyzer.StatusTaskComplete && status != analyzer.StatusReviewComplete {
		return status, nil
	}
	// The task-body formatter applies the same Markdown cleanup and truncation
	// regardless of whether the final text has reached the transcript.
	synthetic := []jsonl.Message{{
		Type: "assistant",
		Message: jsonl.MessageContent{Role: "assistant", Content: []jsonl.Content{{
			Type: "text", Text: message,
		}}},
	}}
	body, _ := summary.GenerateFromMessagesStructured(synthetic, analyzer.StatusTaskComplete, h.cfg)
	return status, &TurnInsight{Body: body}
}

// A Claude success summary can mention an error it fixed. Only a failure
// phrase standing alone or introducing details is treated as a host error;
// the Codex last-message heuristic is intentionally broader and not used here.
func classifyClaudeStopFailure(message string) analyzer.Status {
	// Long final responses are usually task reports, not host failure notices.
	if utf8.RuneCountInString(strings.TrimSpace(message)) > 300 {
		return analyzer.StatusUnknown
	}
	firstLine := strings.ToLower(strings.TrimSpace(strings.SplitN(message, "\n", 2)[0]))
	for _, pattern := range []struct {
		phrase string
		status analyzer.Status
	}{
		{"session limit reached", analyzer.StatusSessionLimitReached},
		{"usage limit reached", analyzer.StatusSessionLimitReached},
		{"rate limit reached", analyzer.StatusAPIErrorOverloaded},
		{"rate limit exceeded", analyzer.StatusAPIErrorOverloaded},
		{"too many requests", analyzer.StatusAPIErrorOverloaded},
		{"quota exceeded", analyzer.StatusAPIErrorOverloaded},
		{"currently overloaded", analyzer.StatusAPIErrorOverloaded},
		{"authentication failed", analyzer.StatusAPIError},
		{"invalid api key", analyzer.StatusAPIError},
		{"api error", analyzer.StatusAPIError},
		{"stream error", analyzer.StatusAPIError},
		{"context window exceeded", analyzer.StatusAPIError},
	} {
		if firstLine == pattern.phrase || firstLine == pattern.phrase+"." {
			return pattern.status
		}
		for _, separator := range []string{":", ". ", " - "} {
			if detail, ok := strings.CutPrefix(firstLine, pattern.phrase+separator); ok {
				detail = strings.TrimSpace(detail)
				for _, success := range []string{"fixed", "resolved", "handled", "corrected", "mitigated"} {
					if strings.HasPrefix(detail, success+" ") || detail == success {
						return analyzer.StatusUnknown
					}
				}
				return pattern.status
			}
		}
	}
	return analyzer.StatusUnknown
}

func claudeStopIsReview(messages []jsonl.Message, final string) bool {
	currentTurn := jsonl.FilterMessagesAfterTimestamp(messages, jsonl.GetLastUserTimestamp(messages))
	if len(currentTurn) > 15 {
		currentTurn = currentTurn[len(currentTurn)-15:]
	}
	tools := jsonl.ExtractTools(currentTurn)
	if jsonl.CountToolsByNames(tools, []string{"Read", "Grep", "Glob"}) == 0 ||
		jsonl.HasAnyActiveTool(tools, analyzer.ActiveTools) {
		return false
	}
	recentText := jsonl.ExtractRecentText(currentTurn, 5)
	lastTexts := []string(nil)
	if len(currentTurn) > 0 {
		lastTexts = jsonl.ExtractTextFromMessages(currentTurn[len(currentTurn)-1:])
	}
	if len(lastTexts) > 0 && strings.Join(strings.Fields(strings.Join(lastTexts, " ")), " ") ==
		strings.Join(strings.Fields(final), " ") {
		// The final line has already flushed; don't count it twice.
		return len(recentText) > 200
	}
	return len(recentText)+len(final) > 200
}

// A final reply missing from the transcript is a long generation whose flush
// has not landed yet, not a resume of an already finished turn.
func claudeStopHasUnflushedReply(messages []jsonl.Message, final string) bool {
	final = strings.Join(strings.Fields(final), " ")
	if final == "" {
		return false
	}
	currentTurn := jsonl.FilterMessagesAfterTimestamp(messages, jsonl.GetLastUserTimestamp(messages))
	last := jsonl.ExtractTextFromMessages(jsonl.GetLastAssistantMessages(currentTurn, 1))
	return strings.Join(strings.Fields(strings.Join(last, " ")), " ") != final
}

// transcriptSizeBeforeParse records a baseline before the initial JSONL read.
// An append during parsing must not become the settle loop's baseline.
func transcriptSizeBeforeParse(path string) int64 {
	if path == "" {
		return -1
	}
	info, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return info.Size()
}

// Older Claude versions may omit last_assistant_message. In that case only,
// wait briefly for an existing transcript to grow. A stat avoids reparsing an
// unchanged large transcript; elapsed time bounds both sleeps and reparses.
func (h *Handler) settleClaudeTranscript(ev Event, status analyzer.Status, messages []jsonl.Message, preParseSize int64) (analyzer.Status, []jsonl.Message) {
	path := ev.Session.TranscriptPath
	if path == "" || !platform.FileExists(path) || claudeStopStatusSettled(status) || claudeTranscriptHasClosingText(messages) {
		return status, messages
	}
	deadline := time.Now().Add(transcriptSettleWait)
	lastSize := preParseSize
	for time.Now().Before(deadline) {
		settleSleepFunc(transcriptSettleInterval)
		info, err := os.Stat(path)
		if err == nil && info.Size() == lastSize {
			continue
		}
		if err == nil {
			lastSize = info.Size()
		}
		updatedStatus, updatedMessages, parseErr := analyzer.AnalyzeTranscriptWithMessages(path, h.cfg)
		if parseErr != nil {
			continue
		}
		status, messages = updatedStatus, updatedMessages
		if claudeStopStatusSettled(status) || claudeTranscriptHasClosingText(messages) {
			break
		}
	}
	return status, messages
}

func claudeStopStatusSettled(status analyzer.Status) bool {
	switch status {
	case analyzer.StatusPlanReady, analyzer.StatusQuestion, analyzer.StatusSessionLimitReached,
		analyzer.StatusAPIError, analyzer.StatusAPIErrorOverloaded:
		return true
	}
	return false
}

func claudeTranscriptHasClosingText(messages []jsonl.Message) bool {
	currentTurn := jsonl.FilterMessagesAfterTimestamp(messages, jsonl.GetLastUserTimestamp(messages))
	if len(currentTurn) == 0 {
		return false
	}
	last := currentTurn[len(currentTurn)-1]
	text := false
	for _, block := range last.Message.Content {
		if block.Type == "tool_use" {
			return false
		}
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			text = true
		}
	}
	return text
}

// The user timestamp distinguishes separate turns with identical replies.
// It is already present when a normal Claude transcript has reached Stop;
// empty is used for no-session-persistence turns without a transcript.
func claudeStopPayloadHash(final, userTimestamp string) string {
	material := userTimestamp + "\x00" + strings.TrimSpace(final)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(material)))
}

func claudeStopTurnLockKey(sessionID, userTimestamp string) string {
	material := sessionID + "\x00" + userTimestamp
	return fmt.Sprintf("claude-turn-%x", sha256.Sum256([]byte(material)))
}

// isSubagentTranscript checks if the transcript path indicates a subagent session.
// Claude Code stores subagent transcripts in paths containing /subagents/ segment.
func isSubagentTranscript(transcriptPath string) bool {
	// Normalize path separators for cross-platform compatibility
	normalized := filepath.ToSlash(transcriptPath)
	return strings.Contains(normalized, "/subagents/")
}
