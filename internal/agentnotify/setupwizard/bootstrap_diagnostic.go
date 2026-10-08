package setupwizard

import (
	"context"
	"fmt"
	"strings"

	"github.com/777genius/agent-notifications/internal/agentnotify/clientsetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
)

// Report safe categories, never arbitrary parser errors or configuration values.
// Quoted paths cannot inject terminal controls into a caller's text diagnostic.
func bootstrapMCPInspectionError(target TargetResult) error {
	client := "Selected client"
	switch target.Client {
	case "claude":
		client = "Claude Code"
	case "codex":
		client = "Codex"
	}
	reason := "inspection failed; inspect the existing installation before retrying"
	cause := ErrRefused
	switch {
	case strings.Contains(target.Reason, context.DeadlineExceeded.Error()):
		reason, cause = "inspection timed out; retry with enough time to verify installed files", context.DeadlineExceeded
	case strings.Contains(target.Reason, context.Canceled.Error()):
		reason, cause = "inspection cancelled", context.Canceled
	case strings.Contains(target.Reason, clientsetup.ErrRecovery.Error()):
		reason = "pending installer recovery"
	case strings.Contains(target.Reason, clientsetup.ErrConflict.Error()):
		reason = "installed runtime or registration ownership does not match"
	case strings.Contains(target.Reason, registration.ErrConflict.Error()):
		reason = "existing MCP entry conflicts with installer ownership"
	case strings.Contains(target.Reason, registration.ErrDuplicate.Error()):
		reason = "the notification command is already registered under another MCP name"
	case strings.Contains(target.Reason, registration.ErrInvalid.Error()):
		reason = "invalid MCP configuration or transport fields"
	case strings.Contains(target.Reason, registration.ErrLimit.Error()), strings.Contains(target.Reason, "oversized document"), strings.Contains(target.Reason, "document limit exceeded"):
		reason = "MCP configuration exceeds the inspection size limit"
	}
	return fmt.Errorf("%s MCP inspection failed for %q: %s: %w", client, target.ConfigPath, reason, cause)
}
