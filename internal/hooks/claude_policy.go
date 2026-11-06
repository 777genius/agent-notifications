package hooks

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/benchmark"
	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/platform"
	"github.com/777genius/agent-notifications/pkg/jsonl"
)

// classifyClaudeHook is the only classifier that reads Claude transcripts or
// team authority. Other adapters cannot acquire those capabilities by emitting
// a payload with the same shape.
func (h *Handler) classifyClaudeHook(ev Event, keys eventKeys, bench *benchmark.Context) (*hookClassification, error) {
	result := &hookClassification{}
	var status analyzer.Status
	var parsedMessages []jsonl.Message
	var insight *TurnInsight
	var err error
	switch p := ev.Payload.(type) {
	case PreToolUsePayload:
		return h.classifyInteractiveTool(ev, p, keys.stateKey), nil
	case NotificationPayload:
		logging.Debug("Notification event received → question status")
		status = analyzer.StatusQuestion
	case StopPayload:
		// A Stop event is the MAIN agent finishing, so suppress only when its
		// transcript_path actually points at a subagent/teammate transcript
		// (.../subagents/...). Note: on current Claude Code the Stop hook receives
		// the parent session transcript, so this rarely matches — kept as a
		// forward-compatible guard for transcripts that are routed differently.
		if h.cfg.ShouldSuppressForSubagents() && isSubagentTranscript(ev.Session.TranscriptPath) {
			logging.Debug("Stop: subagent transcript detected (%s), suppressing (config: suppressForSubagents)", ev.Session.TranscriptPath)
			return nil, nil
		}

		// Analyze and allow legacy payloads to settle before checking age: a
		// long-running turn may not have flushed its final record yet.
		preParseSize := transcriptSizeBeforeParse(ev.Session.TranscriptPath)
		bench.Start("stop.analyze")
		status, parsedMessages, err = h.handleStopEvent(ev)
		bench.Elapsed("stop.analyze")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(p.AssistantMessage) == "" {
			status, parsedMessages = h.settleClaudeTranscript(ev, status, parsedMessages, preParseSize)
		}

		// A headless resume of a finished session fires Stop without appending
		// anything (#119). Check after settling and before changing team state.
		if age := platform.FileAge(ev.Session.TranscriptPath); age > staleTranscriptSeconds &&
			!claudeStopHasUnflushedReply(parsedMessages, p.AssistantMessage) {
			logging.Debug("Stop: transcript unchanged for %ds, skipping stale completion", age)
			h.cleanupOldLocks()
			return nil, nil
		}

		// Team mode: check if this session is a team lead and suppress if needed
		if h.cfg.GetTeamMode() == "wait-all" {
			if teamInfo := h.teamStateMgr.DetectTeamLead(ev.Session.SessionID); teamInfo != nil {
				logging.Debug("Stop: team lead detected for team %q (members: %d), checking team state",
					teamInfo.TeamName, len(teamInfo.Members))

				// Record that the lead has stopped
				if err := h.teamStateMgr.RecordLeadStopped(teamInfo.TeamName); err != nil {
					return nil, fmt.Errorf("stop: record lead stopped: %w", err)
				}

				// Check readiness and claim completion under the same team file lock.
				claimed, err := h.teamStateMgr.ClaimAllIdle(teamInfo.TeamName, teamInfo.Members)
				if err != nil {
					return nil, fmt.Errorf("stop: claim team completion: %w", err)
				}

				if !claimed {
					// Not all teammates idle yet — suppress notification, wait for TeammateIdle
					logging.Debug("Stop: team %q has active teammates, suppressing notification", teamInfo.TeamName)
					return nil, nil
				}

				// This hook owns the completion; proceed with notification.
				logging.Debug("Stop: team %q all teammates idle, sending notification", teamInfo.TeamName)
			}
		} else if h.cfg.GetTeamMode() == "never" {
			if teamInfo := h.teamStateMgr.DetectTeamLead(ev.Session.SessionID); teamInfo != nil {
				logging.Debug("Stop: team mode is 'never', suppressing for team %q", teamInfo.TeamName)
				return nil, nil
			}
		}
		// teamMode "always" or not a team lead: fall through to normal processing

		status, insight = h.enrichClaudeStop(status, p.AssistantMessage, parsedMessages)
		// Note: We don't delete session state here to preserve cooldown info
		// State files have TTL and will be cleaned up automatically
		result.cleanup = true
	case SubagentStopPayload:
		// A SubagentStop event always denotes a subagent (Task tool) finishing,
		// so the event type itself — not the transcript path — is the reliable
		// subagent signal. Claude Code passes the PARENT session transcript_path
		// to this hook (e.g. .../<session>.jsonl), NOT the subagent's
		// .../<session>/subagents/agent-*.jsonl file, so isSubagentTranscript()
		// never matches here. Suppress by the event so suppressForSubagents works
		// as a safety net regardless of notifyOnSubagentStop.
		if h.cfg.ShouldSuppressForSubagents() {
			logging.Debug("SubagentStop: suppressing subagent notification (config: suppressForSubagents)")
			return nil, nil
		}
		// Not globally suppressed — honor the explicit opt-in flag.
		if !h.cfg.Notifications.NotifyOnSubagentStop {
			logging.Debug("SubagentStop: notifications disabled (config: notifyOnSubagentStop), skipping")
			return nil, nil
		}
		// Opted in and not suppressed: handle like Stop.
		logging.Debug("SubagentStop: notifications enabled (config), processing")
		preParseSize := transcriptSizeBeforeParse(ev.Session.TranscriptPath)
		bench.Start("stop.analyze")
		status, parsedMessages, err = h.handleStopEvent(ev)
		bench.Elapsed("stop.analyze")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(p.Stop.AssistantMessage) == "" {
			status, parsedMessages = h.settleClaudeTranscript(ev, status, parsedMessages, preParseSize)
		}
		status, insight = h.enrichClaudeStop(status, p.Stop.AssistantMessage, parsedMessages)
		result.cleanup = true
	case PermissionRequestPayload:
		status = analyzer.StatusPermissionRequest
	case TeammateIdlePayload:
		return nil, h.handleTeammateIdle(ev, p)
	default:
		return nil, fmt.Errorf("unknown hook event: %s", ev.Kind())
	}
	result.status, result.messages, result.insight = status, parsedMessages, insight
	return result, nil
}

// handleTeammateIdle handles the TeammateIdle hook event.
// Records the teammate as idle, checks if all teammates are idle + lead stopped,
// and sends a notification when both conditions are met.
func (h *Handler) handleTeammateIdle(ev Event, p TeammateIdlePayload) error {
	if ev.Product != ProductClaude {
		return nil
	}
	if p.TeamName == "" || p.TeammateName == "" {
		logging.Debug("TeammateIdle: missing team_name or teammate_name, skipping")
		return nil
	}

	teamMode := h.cfg.GetTeamMode()
	if teamMode != "wait-all" {
		logging.Debug("TeammateIdle: teamMode=%q, skipping (only active in wait-all mode)", teamMode)
		return nil
	}

	// Dedup: prevent rapid duplicate TeammateIdle events for the same teammate
	dedupKey := ev.Session.SessionID + "-" + p.TeammateName
	if h.dedupMgr.CheckEarlyDuplicate(dedupKey, "TeammateIdle") {
		logging.Debug("TeammateIdle: duplicate for %q, skipping", p.TeammateName)
		return nil
	}

	logging.Debug("TeammateIdle: teammate=%q team=%q", p.TeammateName, p.TeamName)

	// Get team info to know all expected members
	teamInfo := h.teamStateMgr.DetectTeamByName(p.TeamName)
	if teamInfo == nil {
		logging.Debug("TeammateIdle: team %q config not found, skipping", p.TeamName)
		return nil
	}

	// Record this teammate as idle
	if err := h.teamStateMgr.RecordTeammateIdle(p.TeamName, p.TeammateName); err != nil {
		return fmt.Errorf("teammate idle: record idle state: %w", err)
	}

	// A suppressed teammate must not consume the team's completion claim.
	// An eligible lead Stop can still claim the already-recorded idle members.
	status := analyzer.StatusTaskComplete
	gitBranch := h.gitBranch(ev.Session.CWD)
	folderName := filepath.Base(ev.Session.CWD)
	if h.cfg.ShouldFilter(string(status), gitBranch, folderName) {
		logging.Debug("TeammateIdle: notification suppressed by filter: status=%s branch=%q folder=%s", status, gitBranch, folderName)
		return nil
	}

	// Check readiness and claim completion under the same team file lock.
	claimed, err := h.teamStateMgr.ClaimAllIdle(p.TeamName, teamInfo.Members)
	if err != nil {
		return fmt.Errorf("teammate idle: claim team completion: %w", err)
	}

	if !claimed {
		logging.Debug("TeammateIdle: not all conditions met yet for team %q", p.TeamName)
		return nil
	}

	// All conditions met — send notification.
	logging.Debug("TeammateIdle: all teammates idle + lead stopped for team %q, sending notification", p.TeamName)

	body := fmt.Sprintf("Team %q: all teammates finished work", p.TeamName)

	stateKey := teamInfo.LeadSessionID
	if stateKey == "" {
		stateKey = ev.Session.SessionID
	}
	// Share the admission-time branch result with delivery, even when empty.
	// Re-running Git can exceed the host hook deadline before the banner.
	details := h.notificationDetails(ev, nil)
	details.branch, details.branchFetched = gitBranch, true
	h.sendNotifications(status, body, "", ev.Session.SessionID, ev.Session.CWD, func() {
		if err := h.stateMgr.UpdateLastNotificationWithIdentity(stateKey, status, body, "", body, "", "TeammateIdle"); err != nil {
			logging.Warn("TeammateIdle: failed to update notification state: %v", err)
		}
	}, details)

	logging.Debug("=== Hook completed: TeammateIdle (team notification sent) ===")
	return nil
}
