package notifier

import (
	"strings"
)

// extractSessionInfo extracts session name and git branch from message
// Format: "[session-name|branch] message" or "[session-name] message"
// Returns session name, git branch (may be empty), and clean message
func extractSessionInfo(message string) (sessionName, gitBranch, cleanMessage string) {
	message = strings.TrimSpace(message)

	// Check if message starts with [
	if !strings.HasPrefix(message, "[") {
		return "", "", message
	}

	// Find closing bracket
	closingIdx := strings.Index(message, "]")
	if closingIdx == -1 {
		return "", "", message
	}

	// Extract content inside brackets
	bracketContent := message[1:closingIdx]

	// Check if there's a pipe separator for git branch
	if pipeIdx := strings.Index(bracketContent, "|"); pipeIdx != -1 {
		sessionName = bracketContent[:pipeIdx]
		gitBranch = bracketContent[pipeIdx+1:]
	} else {
		sessionName = bracketContent
		gitBranch = ""
	}

	// Extract clean message (everything after "] ")
	cleanMessage = strings.TrimSpace(message[closingIdx+1:])

	return sessionName, gitBranch, cleanMessage
}
