package webhook

import (
	"fmt"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/sessionname"
)

const (
	discordEmbedAuthorLimit   = 256
	discordEmbedFooterLimit   = 2048
	agentNotificationsLogoURL = "https://777genius.github.io/agent-notifications/agent-notifications-logo.png"
)

// normalizeAgentSource returns the machine-readable agent identity used in
// structured payloads and templates, reusing the same config.AgentID enum
// the config/composition-root layer already defines (internal/config/agents.go)
// instead of a parallel string enum. Empty defaults to AgentClaude since every
// pre-multi-agent caller only ever sent Claude events.
func normalizeAgentSource(source string) string {
	if source == "" {
		return string(config.AgentClaude)
	}
	return source
}

// agentDisplayName returns the human-readable product name shown in chat
// notifications (Slack/Discord/Telegram/Lark). Unrecognized agent_source
// values (a future agent not yet given a friendly name here) fall back to
// the normalized source string itself rather than being mislabeled as
// Claude Code, per the forward-compatibility contract documented for
// agent_source in docs/webhooks/custom.md.
func agentDisplayName(source string) string {
	normalized := normalizeAgentSource(source)
	switch config.AgentID(normalized) {
	case config.AgentCodex:
		return "Codex"
	case config.AgentClaude:
		return "Claude Code"
	default:
		return normalized
	}
}

// Formatter renders a SendContext into a preset-specific webhook payload.
//
// Implementations should treat ctx.Message as the pre-joined notification text
// and use the structured fields (RawBody, ActionSummary, SessionName, Folder,
// GitBranch) only when the underlying transport benefits from richer layouts.
type Formatter interface {
	Format(ctx SendContext, statusInfo config.StatusInfo) (interface{}, error)
}

// SlackFormatter formats messages for Slack
type SlackFormatter struct{}

func (f *SlackFormatter) Format(ctx SendContext, statusInfo config.StatusInfo) (interface{}, error) {
	color := getColorForStatus(ctx.Status)

	return map[string]interface{}{
		"attachments": []map[string]interface{}{
			{
				"color":       color,
				"title":       statusInfo.Title,
				"text":        ctx.Message,
				"footer":      fmt.Sprintf("Session: %s | %s", ctx.SessionID, agentDisplayName(ctx.AgentSource)),
				"footer_icon": agentNotificationsLogoURL,
				"ts":          time.Now().Unix(),
				"mrkdwn_in":   []string{"text"},
			},
		},
	}, nil
}

// DiscordFormatter formats messages for Discord using native embed structure:
// author / title / description / inline fields / footer.
type DiscordFormatter struct{}

func (f *DiscordFormatter) Format(ctx SendContext, statusInfo config.StatusInfo) (interface{}, error) {
	embed := map[string]interface{}{
		"title":     statusInfo.Title,
		"color":     getDiscordColorInt(ctx.Status),
		"timestamp": time.Now().Format(time.RFC3339),
	}

	if author := buildDiscordAuthor(ctx); author != "" {
		embed["author"] = map[string]interface{}{"name": author}
	}

	description := strings.TrimSpace(ctx.RawBody)
	if description == "" {
		// Fallback for callers that haven't populated structured fields yet.
		description = ctx.Message
	}
	if description != "" {
		embed["description"] = description
	}

	if fields := parseActionSummary(ctx.ActionSummary); len(fields) > 0 {
		embed["fields"] = fields
	}

	embed["footer"] = map[string]interface{}{
		"text": buildDiscordFooter(ctx),
	}

	return map[string]interface{}{
		"username":   agentDisplayName(ctx.AgentSource),
		"avatar_url": agentNotificationsLogoURL,
		"embeds":     []map[string]interface{}{embed},
	}, nil
}

// buildDiscordAuthor returns the embed author line, e.g.:
//
//	"phoenix 439d1884 · claude-utils (main)"
//	"phoenix 439d1884 · claude-utils"      // no git branch
//	"phoenix 439d1884"                      // no folder either
//
// Returns "" when no session-derived label is available.
func buildDiscordAuthor(ctx SendContext) string {
	name := ctx.SessionName
	if name == "" && ctx.SessionID != "" {
		name = sessionname.GenerateSessionLabel(ctx.SessionID)
	}
	if name == "" {
		return ""
	}

	parts := []string{name}
	if ctx.Folder != "" {
		parts = append(parts, ctx.Folder)
	}
	author := strings.Join(parts, " · ")
	if ctx.GitBranch != "" {
		author += fmt.Sprintf(" (%s)", ctx.GitBranch)
	}
	return truncateMiddle(author, discordEmbedAuthorLimit)
}

// buildDiscordFooter returns the embed footer text.
// Uses the raw session UUID so the footer is not redundant with the friendly
// label that already appears in the author line. Agent identity is shown via
// the top-level "username" field only (see Format below), not repeated here,
// so there is a single place to update per agent instead of two that could
// drift out of sync.
func buildDiscordFooter(ctx SendContext) string {
	if ctx.SessionID == "" {
		return "Agent Notifications"
	}
	return truncateMiddle(fmt.Sprintf("Session: %s", ctx.SessionID), discordEmbedFooterLimit)
}

// truncateMiddle keeps both the start and end of a string visible while
// enforcing a hard character limit for Discord embed fields.
func truncateMiddle(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit || limit <= 0 {
		return s
	}
	if limit == 1 {
		return string(runes[:1])
	}
	if limit == 2 {
		return ".."
	}

	const ellipsis = "..."
	available := limit - len([]rune(ellipsis))
	head := available / 2
	tail := available - head

	return string(runes[:head]) + ellipsis + string(runes[len(runes)-tail:])
}

// actionEmojiField maps the leading emoji of an action segment to a field name.
// Keep in sync with summary.buildActionsString.
var actionEmojiField = []struct {
	prefix string
	name   string
}{
	{"📝", "New"},
	{"✏️", "Edited"},
	{"▶", "Commands"},
	{"⏱", "Duration"},
}

// parseActionSummary splits an action summary string (e.g.
// "📝 1 new  ▶ 2 cmds  ⏱ 41s") into Discord embed fields. Unknown segments are
// collected into a single "Details" field so future emoji additions never lose
// information.
func parseActionSummary(s string) []map[string]interface{} {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}

	segments := strings.Split(s, "  ")
	fields := make([]map[string]interface{}, 0, len(segments))
	var unknown []string

	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}

		matched := false
		for _, m := range actionEmojiField {
			if strings.HasPrefix(seg, m.prefix) {
				value := strings.TrimSpace(strings.TrimPrefix(seg, m.prefix))
				if value == "" {
					// Discord rejects fields with empty value (HTTP 400). Skip
					// segments that have no payload after their emoji prefix.
					matched = true
					break
				}
				fields = append(fields, map[string]interface{}{
					"name":   m.name,
					"value":  value,
					"inline": true,
				})
				matched = true
				break
			}
		}
		if !matched {
			unknown = append(unknown, seg)
		}
	}

	if len(unknown) > 0 {
		logging.Debug("Discord embed: unknown action segments %v", unknown)
		fields = append(fields, map[string]interface{}{
			"name":   "Details",
			"value":  strings.Join(unknown, " "),
			"inline": true,
		})
	}

	return fields
}

// TelegramFormatter formats messages for Telegram with HTML
type TelegramFormatter struct {
	ChatID string
}

func (f *TelegramFormatter) Format(ctx SendContext, statusInfo config.StatusInfo) (interface{}, error) {
	emoji := getEmojiForStatus(ctx.Status)
	text := fmt.Sprintf("<b>%s %s</b>\n\n%s\n\n<i>Session: %s · %s</i>",
		emoji, statusInfo.Title, ctx.Message, ctx.SessionID, agentDisplayName(ctx.AgentSource))

	return map[string]interface{}{
		"chat_id":    f.ChatID,
		"text":       text,
		"parse_mode": "HTML",
	}, nil
}

// getColorForStatus returns color hex code for status (Slack)
func getColorForStatus(status analyzer.Status) string {
	switch status {
	case analyzer.StatusTaskComplete:
		return "#28a745" // Green
	case analyzer.StatusReviewComplete:
		return "#17a2b8" // Teal
	case analyzer.StatusQuestion:
		return "#ffc107" // Yellow/Orange
	case analyzer.StatusPlanReady:
		return "#007bff" // Blue
	case analyzer.StatusPermissionRequest:
		return "#fd7e14" // Orange
	default:
		return "#6c757d" // Gray
	}
}

// getDiscordColorInt returns Discord color integer for status
func getDiscordColorInt(status analyzer.Status) int {
	switch status {
	case analyzer.StatusTaskComplete:
		return 0x28a745 // Green
	case analyzer.StatusReviewComplete:
		return 0x17a2b8 // Teal
	case analyzer.StatusQuestion:
		return 0xffc107 // Yellow
	case analyzer.StatusPlanReady:
		return 0x007bff // Blue
	case analyzer.StatusPermissionRequest:
		return 0xfd7e14 // Orange
	default:
		return 0x6c757d // Gray
	}
}

// getEmojiForStatus returns emoji for status (Telegram)
func getEmojiForStatus(status analyzer.Status) string {
	switch status {
	case analyzer.StatusTaskComplete:
		return "✅"
	case analyzer.StatusReviewComplete:
		return "🔍"
	case analyzer.StatusQuestion:
		return "❓"
	case analyzer.StatusPlanReady:
		return "📋"
	case analyzer.StatusPermissionRequest:
		return "🔐"
	default:
		return "ℹ️"
	}
}

// LarkFormatter formats messages for Feishu/Lark with interactive cards
type LarkFormatter struct{}

func (f *LarkFormatter) Format(ctx SendContext, statusInfo config.StatusInfo) (interface{}, error) {
	return map[string]interface{}{
		"msg_type": "interactive",
		"card": map[string]interface{}{
			"config": map[string]interface{}{
				"wide_screen_mode": true,
			},
			"header": map[string]interface{}{
				"title": map[string]interface{}{
					"tag":     "plain_text",
					"content": statusInfo.Title,
				},
				"template": getLarkColorTemplate(ctx.Status),
			},
			"elements": []map[string]interface{}{
				{
					"tag": "div",
					"text": map[string]interface{}{
						"tag":     "plain_text",
						"content": ctx.Message,
					},
				},
				{
					"tag": "hr",
				},
				{
					"tag": "div",
					"text": map[string]interface{}{
						"tag":     "plain_text",
						"content": fmt.Sprintf("Session: %s · %s", ctx.SessionID, agentDisplayName(ctx.AgentSource)),
					},
				},
			},
		},
	}, nil
}

// getLarkColorTemplate returns Lark color template for status
func getLarkColorTemplate(status analyzer.Status) string {
	switch status {
	case analyzer.StatusTaskComplete:
		return "green"
	case analyzer.StatusReviewComplete:
		return "yellow"
	case analyzer.StatusQuestion:
		return "red"
	case analyzer.StatusPlanReady:
		return "blue"
	case analyzer.StatusPermissionRequest:
		return "orange"
	default:
		return "grey"
	}
}
