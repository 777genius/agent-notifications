package hooks

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/benchmark"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/dedup"
	"github.com/777genius/agent-notifications/internal/errorhandler"
	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/notifier"
	"github.com/777genius/agent-notifications/internal/platform"
	"github.com/777genius/agent-notifications/internal/sessionname"
	"github.com/777genius/agent-notifications/internal/state"
	"github.com/777genius/agent-notifications/internal/summary"
	"github.com/777genius/agent-notifications/internal/teamstate"
	"github.com/777genius/agent-notifications/internal/webhook"
	"github.com/777genius/agent-notifications/pkg/jsonl"
)

// maxNotifyDelaySeconds bounds notifyDelaySeconds so the desktop grace-period
// delay can never push the hook past the timeout configured in hooks.json.
const maxNotifyDelaySeconds = 25

// Test seams for the focus-aware / delayed desktop notification path.
var (
	isTerminalFocused = notifier.IsTerminalFocused
	sleepFunc         = time.Sleep
)

type notificationDelivery struct {
	webhookQueued    bool
	desktopDelivered bool
}

func (d notificationDelivery) delivered() bool {
	return d.webhookQueued || d.desktopDelivered
}

// HookData represents the data received from Claude Code hooks
type HookData struct {
	TranscriptPath       string `json:"transcript_path"`
	LastAssistantMessage string `json:"last_assistant_message,omitempty"`
	SessionID            string `json:"session_id"`
	CWD                  string `json:"cwd"`
	ToolName             string `json:"tool_name,omitempty"`
	HookEventName        string `json:"hook_event_name,omitempty"`
	// Team-related fields (present in TeammateIdle, TaskCreated, TaskCompleted hooks)
	TeamName     string `json:"team_name,omitempty"`
	TeammateName string `json:"teammate_name,omitempty"`
}

// notifierInterface defines the interface for sending desktop notifications
type notifierInterface interface {
	SendDesktop(status analyzer.Status, message, sessionID, cwd string) error
	Close() error
}

// webhookInterface defines the interface for sending webhook notifications
type webhookInterface interface {
	SendAsyncWithContext(sendCtx webhook.SendContext)
	Shutdown(timeout time.Duration) error
}

// Handler handles hook events
type Handler struct {
	cfg          *config.Config
	dedupMgr     *dedup.Manager
	stateMgr     *state.Manager
	teamStateMgr *teamstate.Manager
	notifierSvc  notifierInterface
	webhookSvc   webhookInterface
	pluginRoot   string
	product      Product
	source       EventSource
	// codexEnricher is the DIP seam for Codex turn classification; nil
	// selects the built-in payload heuristic. See CodexTurnEnricher.
	codexEnricher CodexTurnEnricher
}

// NewHandler creates a new hook handler
func NewHandler(pluginRoot string) (*Handler, error) {
	// Load config
	cfg, err := config.LoadForAgent(pluginRoot, config.AgentClaude)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	return newHandlerWithConfig(pluginRoot, cfg, ProductClaude, nil)
}

// NewHandlerWithSource creates a handler for the composition root with an
// explicit product and event source. Unlike NewHandler, config warnings go to
// the file log only: observation routes must not write to stderr.
func NewHandlerWithSource(pluginRoot string, product Product, source EventSource) (*Handler, error) {
	agent, err := configAgent(product)
	if err != nil {
		return nil, err
	}
	cfg, err := config.LoadForAgentQuiet(pluginRoot, agent)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	return newHandlerWithConfig(pluginRoot, cfg, product, source)
}

func newHandlerWithConfig(pluginRoot string, cfg *config.Config, product Product, source EventSource) (*Handler, error) {
	// Validate config
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &Handler{
		cfg:          cfg,
		dedupMgr:     dedup.NewManager(),
		stateMgr:     state.NewManager(),
		teamStateMgr: teamstate.NewManager(""),
		notifierSvc:  notifier.New(cfg),
		webhookSvc:   webhook.New(cfg),
		pluginRoot:   pluginRoot,
		product:      product,
		source:       source,
	}, nil
}

// eventSource returns the injected source, defaulting to the legacy Claude
// decoder so zero-value handlers (tests, NewHandler) keep today's behavior.
func (h *Handler) eventSource() EventSource {
	if h.source != nil {
		return h.source
	}
	return ClaudeSource{}
}

// turnEnricher returns the injected Codex enricher, defaulting to the pure
// payload heuristic.
func (h *Handler) turnEnricher() CodexTurnEnricher {
	if h.codexEnricher != nil {
		return h.codexEnricher
	}
	return heuristicEnricher{}
}

// eventKeys derives the state/dedup identities for the event.
func (h *Handler) eventKeys(ev Event) eventKeys {
	if ev.Product == ProductCodex {
		return codexKeys(ev)
	}
	return claudeKeys(ev.Session.SessionID)
}

// eventToolName extracts the tool identity for diagnostics.
func eventToolName(ev Event) string {
	switch p := ev.Payload.(type) {
	case PreToolUsePayload:
		return p.ToolName
	case PermissionRequestPayload:
		return p.ToolName
	}
	return ""
}

// HandleHook handles a hook event
func (h *Handler) HandleHook(hookEvent string, input io.Reader) error {
	// Benchmark instrumentation (enabled via config debug.benchmark)
	bench := benchmark.New(h.cfg.IsBenchmarkEnabled(), logging.Info)
	bench.Start("hook.total")
	defer func() {
		bench.Elapsed("hook.total")
		bench.Report()
	}()

	// Add panic recovery for robustness
	defer errorhandler.HandlePanic()

	// Skip notifications when running in background judge mode (e.g., double-shot-latte plugin)
	// The CLAUDE_HOOK_JUDGE_MODE env var is set by plugins that spawn background Claude instances
	// to evaluate context/decide on continuation - we don't want notifications from these
	// Can be disabled via config: "respectJudgeMode": false
	if h.cfg.ShouldRespectJudgeMode() && os.Getenv("CLAUDE_HOOK_JUDGE_MODE") == "true" {
		return nil
	}

	// Ensure notifier resources are cleaned up when function exits
	defer func() {
		bench.Start("notifier.close")
		if err := h.notifierSvc.Close(); err != nil {
			logging.Warn("Failed to close notifier: %v", err)
		}
		bench.Elapsed("notifier.close")
	}()

	// Ensure webhook sender waits for in-flight requests before exit
	defer func() {
		bench.Start("webhook.shutdown")
		if err := h.webhookSvc.Shutdown(5 * time.Second); err != nil {
			logging.Warn("Failed to shutdown webhook sender: %v", err)
		}
		bench.Elapsed("webhook.shutdown")
	}()

	logging.SetPrefix(fmt.Sprintf("PID:%d", os.Getpid()))
	logging.Debug("=== Hook triggered: %s ===", hookEvent)

	// Decode via the event source (legacy Claude decoder by default)
	bench.Start("stdin.parse")
	ev, err := h.eventSource().Decode(context.Background(), hookEvent, input)
	if err != nil {
		return err
	}
	bench.Elapsed("stdin.parse")

	logging.Debug("Hook data: session=%s, transcript=%s, tool=%s",
		ev.Session.SessionID, ev.Session.TranscriptPath, eventToolName(ev))

	keys := h.eventKeys(ev)

	// Claude-only: the Ghostty capture persists session state under the RAW
	// session id, and Codex state must only ever use hashed identities
	// (doc 00 §6.1). Codex click-to-focus is not a declared feature.
	if h.cfg.Notifications.Desktop.ClickToFocus && ev.Product == ProductClaude &&
		(ev.Kind() == EventPreToolUse || ev.Kind() == EventNotification) {
		notifier.MaybeCaptureGhosttyTerminalID(
			h.cfg.Notifications.Desktop.TerminalBundleID,
			ev.Session.SessionID,
			ev.Session.CWD,
		)
	}

	// Phase 1: Early duplicate check (per hook event type)
	bench.Start("dedup.early_check")
	if h.dedupMgr.CheckEarlyDuplicate(keys.lockKey, hookEvent) {
		bench.Elapsed("dedup.early_check")
		logging.Debug("Early duplicate detected, skipping")
		return nil
	}
	bench.Elapsed("dedup.early_check")

	// Check if any notification method is enabled
	if !h.cfg.IsAnyNotificationEnabled() {
		logging.Debug("All notifications disabled, exiting")
		return nil
	}

	// Policy router: the sealed payload type selects the branch.
	// PayloadEventName stays diagnostic and never routes.
	var status analyzer.Status
	var parsedMessages []jsonl.Message // reused by generateMessage to avoid double I/O
	var insight *TurnInsight           // enriched Codex classification, when available

	switch p := ev.Payload.(type) {
	case PreToolUsePayload:
		if ev.Product == ProductCodex {
			// Codex interactive tools (request_user_input) map to question
			// notifications with the question text projected via allowlist.
			in := h.turnEnricher().EnrichPreToolUse(context.Background(), ev, p)
			if in.Status == analyzer.StatusUnknown {
				logging.Debug("Codex PreToolUse: tool %q is not notification-relevant, skipping", p.ToolName)
				return nil
			}
			logging.Debug("Codex PreToolUse: tool=%s → status=%s", p.ToolName, in.Status)
			status = in.Status
			insight = &in
			break
		}
		status = h.handlePreToolUse(ev, p)
	case NotificationPayload:
		// Notification hook fires when Claude needs user input (permission
		// dialogs, questions), so it always maps to question status.
		logging.Debug("Notification event received → question status")
		status = analyzer.StatusQuestion
	case StopPayload:
		if ev.Product == ProductCodex {
			// Codex continuation turns (stop_hook_active) never notify,
			// preventing recursive/continuation duplicates.
			if p.Continuation {
				logging.Debug("Codex Stop: continuation turn, suppressing")
				return nil
			}
			in := h.turnEnricher().EnrichStop(context.Background(), ev, p)
			status = in.Status
			insight = &in
			defer h.cleanupOldLocks()
			break
		}

		// A Stop event is the MAIN agent finishing, so suppress only when its
		// transcript_path actually points at a subagent/teammate transcript
		// (.../subagents/...). Note: on current Claude Code the Stop hook receives
		// the parent session transcript, so this rarely matches — kept as a
		// forward-compatible guard for transcripts that are routed differently.
		if h.cfg.ShouldSuppressForSubagents() && isSubagentTranscript(ev.Session.TranscriptPath) {
			logging.Debug("Stop: subagent transcript detected (%s), suppressing (config: suppressForSubagents)", ev.Session.TranscriptPath)
			return nil
		}

		// Team mode: check if this session is a team lead and suppress if needed
		if h.cfg.GetTeamMode() == "wait-all" {
			if teamInfo := h.teamStateMgr.DetectTeamLead(ev.Session.SessionID); teamInfo != nil {
				logging.Debug("Stop: team lead detected for team %q (members: %d), checking team state",
					teamInfo.TeamName, len(teamInfo.Members))

				// Record that the lead has stopped
				if err := h.teamStateMgr.RecordLeadStopped(teamInfo.TeamName); err != nil {
					logging.Warn("Stop: failed to record lead stopped: %v", err)
				}

				// Check if all teammates are already idle
				allIdle, err := h.teamStateMgr.CheckAllIdle(teamInfo.TeamName, teamInfo.Members)
				if err != nil {
					logging.Warn("Stop: failed to check team idle state: %v", err)
				}

				if !allIdle {
					// Not all teammates idle yet — suppress notification, wait for TeammateIdle
					logging.Debug("Stop: team %q has active teammates, suppressing notification", teamInfo.TeamName)
					return nil
				}

				// All teammates are idle — proceed with notification and mark as notified
				logging.Debug("Stop: team %q all teammates idle, sending notification", teamInfo.TeamName)
				if err := h.teamStateMgr.MarkNotified(teamInfo.TeamName); err != nil {
					logging.Warn("Stop: failed to mark team notified: %v", err)
				}
			}
		} else if h.cfg.GetTeamMode() == "never" {
			if teamInfo := h.teamStateMgr.DetectTeamLead(ev.Session.SessionID); teamInfo != nil {
				logging.Debug("Stop: team mode is 'never', suppressing for team %q", teamInfo.TeamName)
				return nil
			}
		}
		// teamMode "always" or not a team lead: fall through to normal processing

		// Analyze the transcript to determine status
		bench.Start("stop.analyze")
		status, parsedMessages, err = h.handleStopEvent(ev)
		bench.Elapsed("stop.analyze")
		if err != nil {
			return err
		}
		status, insight = h.fallbackClaudeStop(status, p.AssistantMessage)
		// Note: We don't delete session state here to preserve cooldown info
		// State files have TTL and will be cleaned up automatically
		defer h.cleanupOldLocks()
	case SubagentStopPayload:
		if ev.Product == ProductCodex {
			// Codex SubagentStop delivery mirrors the Claude opt-in
			// semantics: the global subagent suppression wins, then the
			// explicit notifyOnSubagentStop flag must be set.
			if h.cfg.ShouldSuppressForSubagents() {
				logging.Debug("Codex SubagentStop: suppressing (config: suppressForSubagents)")
				return nil
			}
			if !h.cfg.Notifications.NotifyOnSubagentStop {
				logging.Debug("Codex SubagentStop: notifications disabled (config: notifyOnSubagentStop), skipping")
				return nil
			}
			if p.Stop.Continuation {
				logging.Debug("Codex SubagentStop: continuation turn, suppressing")
				return nil
			}
			in := h.turnEnricher().EnrichStop(context.Background(), ev, p.Stop)
			status = in.Status
			insight = &in
			defer h.cleanupOldLocks()
			break
		}

		// A SubagentStop event always denotes a subagent (Task tool) finishing,
		// so the event type itself — not the transcript path — is the reliable
		// subagent signal. Claude Code passes the PARENT session transcript_path
		// to this hook (e.g. .../<session>.jsonl), NOT the subagent's
		// .../<session>/subagents/agent-*.jsonl file, so isSubagentTranscript()
		// never matches here. Suppress by the event so suppressForSubagents works
		// as a safety net regardless of notifyOnSubagentStop.
		if h.cfg.ShouldSuppressForSubagents() {
			logging.Debug("SubagentStop: suppressing subagent notification (config: suppressForSubagents)")
			return nil
		}
		// Not globally suppressed — honor the explicit opt-in flag.
		if !h.cfg.Notifications.NotifyOnSubagentStop {
			logging.Debug("SubagentStop: notifications disabled (config: notifyOnSubagentStop), skipping")
			return nil
		}
		// Opted in and not suppressed: handle like Stop.
		logging.Debug("SubagentStop: notifications enabled (config), processing")
		bench.Start("stop.analyze")
		status, parsedMessages, err = h.handleStopEvent(ev)
		bench.Elapsed("stop.analyze")
		if err != nil {
			return err
		}
		status, insight = h.fallbackClaudeStop(status, p.Stop.AssistantMessage)
		defer h.cleanupOldLocks()
	case PermissionRequestPayload:
		// Codex-only in this milestone: the host is waiting on user approval.
		logging.Debug("PermissionRequest: tool=%s", p.ToolName)
		status = analyzer.StatusPermissionRequest
	case TeammateIdlePayload:
		return h.handleTeammateIdle(ev, p)
	default:
		return fmt.Errorf("unknown hook event: %s", hookEvent)
	}

	// If status is unknown, skip
	if status == analyzer.StatusUnknown {
		logging.Debug("Status is unknown, skipping notification")
		return nil
	}

	// Check suppress-filters before any state mutations (dedup lock, cooldowns)
	bench.Start("git.branch")
	{
		gitBranch := platform.GetGitBranch(ev.Session.CWD)
		bench.Elapsed("git.branch")
		folderName := filepath.Base(ev.Session.CWD)
		if h.cfg.ShouldFilter(string(status), gitBranch, folderName) {
			logging.Debug("Notification suppressed by filter: status=%s branch=%q folder=%s", status, gitBranch, folderName)
			return nil
		}
	}

	// Phase 2: Acquire lock before sending (per hook event type)
	acquired, err := h.dedupMgr.AcquireLock(keys.lockKey, hookEvent)
	if err != nil {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}
	if !acquired {
		logging.Debug("Failed to acquire lock (duplicate), skipping")
		return nil
	}

	logging.Debug("Lock acquired, proceeding with notification")
	// Note: Lock is NOT released - it ages out naturally after 2s to prevent rapid duplicates

	// Check cooldown for question status BEFORE updating notification time.
	// The cooldown exists to collapse Claude's paired PreToolUse/Notification
	// double-fire; a Codex question tool has no paired event and blocks the
	// session until answered, so it bypasses the cooldown (its duplicates are
	// bounded by the turn+call-scoped dedup lock).
	codexQuestionPrompt := ev.Product == ProductCodex && ev.Kind() == EventPreToolUse
	if status == analyzer.StatusQuestion && !codexQuestionPrompt {
		logging.Debug("Checking question cooldown: cooldownSeconds=%d", h.cfg.GetSuppressQuestionAfterAnyNotificationSeconds())

		// Load state to log its contents
		sessionState, stateErr := h.stateMgr.Load(keys.stateKey)
		if stateErr != nil {
			logging.Warn("Failed to load state for logging: %v", stateErr)
		} else if sessionState != nil {
			logging.Debug("Session state: lastNotificationTime=%d, lastNotificationStatus=%s",
				sessionState.LastNotificationTime, sessionState.LastNotificationStatus)
		} else {
			logging.Debug("No session state found")
		}

		// First, check if we should suppress question after ANY notification (not just task_complete)
		suppressAfterAny, err := h.stateMgr.ShouldSuppressQuestionAfterAnyNotification(
			keys.stateKey,
			h.cfg.GetSuppressQuestionAfterAnyNotificationSeconds(),
		)
		if err != nil {
			logging.Warn("Failed to check cooldown after any notification: %v", err)
		} else if suppressAfterAny {
			logging.Debug("Question suppressed due to recent notification from this session")
			// Lock will be released by defer
			return nil
		} else {
			logging.Debug("Question NOT suppressed (cooldown check passed)")
		}

		// Also check legacy cooldown after task_complete
		suppress, err := h.stateMgr.ShouldSuppressQuestion(
			keys.stateKey,
			h.cfg.GetSuppressQuestionAfterTaskCompleteSeconds(),
		)
		if err != nil {
			logging.Warn("Failed to check cooldown: %v", err)
		} else if suppress {
			logging.Debug("Question suppressed due to cooldown after task complete")
			// Lock will be released by defer
			return nil
		}
	}

	// Update state (only for task_complete, PreToolUse already updated state)
	if status == analyzer.StatusTaskComplete {
		if err := h.stateMgr.UpdateTaskComplete(keys.stateKey); err != nil {
			logging.Warn("Failed to update task complete state: %v", err)
		}
	}

	// Generate message
	bench.Start("message.generate")
	body, actions := h.generateMessage(ev, status, parsedMessages, insight)
	message := joinMessageParts(body, actions)
	var stopHash string
	currentTurn := jsonl.FilterMessagesAfterTimestamp(parsedMessages, jsonl.GetLastUserTimestamp(parsedMessages))
	if ev.Product == ProductClaude && (ev.Kind() == EventStop || ev.Kind() == EventSubagentStop) &&
		len(jsonl.ExtractTools(currentTurn)) == 0 {
		// A text-only Stop can arrive before its final transcript line.
		// Track a separate hash of the host final message so transcript replay
		// is stable while rendered-content dedup stays shared across hooks.
		var final string
		switch p := ev.Payload.(type) {
		case StopPayload:
			final = p.AssistantMessage
		case SubagentStopPayload:
			final = p.Stop.AssistantMessage
		}
		if strings.TrimSpace(final) != "" {
			stopHash = claudeStopPayloadHash(final, jsonl.GetLastUserTimestamp(parsedMessages))
		}
	}
	bench.Elapsed("message.generate")

	// Acquire content lock to prevent race between different hooks (Stop vs Notification)
	// This ensures only one process can check and update duplicate state at a time
	// Distinct Codex prompts and subagents use event-scoped dedup only.
	// Claude retains its original session locking, including permissions.
	skipContentLock := ev.Product == ProductCodex && (status == analyzer.StatusPermissionRequest ||
		ev.Kind() == EventPreToolUse || ev.Kind() == EventSubagentStop)
	contentLockAcquired := false
	if !skipContentLock {
		contentLockAcquired, err = h.dedupMgr.AcquireContentLock(keys.stateKey)
		if err != nil {
			logging.Warn("Failed to acquire content lock: %v", err)
			// Error (not "lock busy") - continue without lock as fallback
		} else if !contentLockAcquired {
			// Lock is held by another process - it's already handling this notification
			logging.Warn("Content lock held by another process: session=%s hook=%s (notification skipped)", keys.stateKey, hookEvent)
			return nil
		}
	}

	releaseContentLock := func() {
		if contentLockAcquired {
			if err := h.dedupMgr.ReleaseContentLock(keys.stateKey); err != nil {
				logging.Warn("Failed to release content lock: %v", err)
			}
			contentLockAcquired = false
		}
	}
	defer releaseContentLock()

	// Check for duplicate message content (3 minutes = 180 seconds window).
	// Exempt cases where the session-wide window contradicts the contract's
	// event-scoped dedup identity (doc 00 §6.1):
	// - permission_request: deterministic body, a later prompt for the same
	//   tool is a REAL prompt the user must see;
	// - Codex questions: re-asked verbatim in a later turn is still a real
	//   blocking prompt;
	// - Codex SubagentStop: parallel subagents finishing with an identical
	//   final message must not collapse into one notification.
	// Turn-level duplicates of the event itself stay bounded by the
	// turn+tool/agent(+call)-scoped dedup lock.
	skipContentDedup := status == analyzer.StatusPermissionRequest ||
		(ev.Product == ProductCodex && (ev.Kind() == EventPreToolUse || ev.Kind() == EventSubagentStop))
	if !skipContentDedup {
		isDuplicateStop, stopErr := h.stateMgr.IsDuplicateStopPayload(keys.stateKey, stopHash, 180)
		if stopErr != nil {
			logging.Warn("Failed to check duplicate Stop payload: %v", stopErr)
		} else if isDuplicateStop {
			logging.Debug("Duplicate Stop payload detected within 3 minutes, skipping")
			return nil
		}
		isDuplicate, err := h.stateMgr.IsDuplicateMessage(keys.stateKey, message, 180)
		if err != nil {
			logging.Warn("Failed to check duplicate message: %v", err)
		} else if isDuplicate {
			logging.Debug("Duplicate message content detected within 3 minutes, skipping")
			return nil
		}
	}

	// Release the cross-hook content lock before any delivery work. Desktop
	// delivery may intentionally sleep for notifyDelaySeconds, and holding this
	// lock during that delay would make concurrent hooks skip notifications.
	releaseContentLock()

	// Send notifications
	bench.Start("notify.send")
	delivery := h.sendNotifications(status, body, actions, ev.Session.SessionID, ev.Session.CWD)
	bench.Elapsed("notify.send")

	if delivery.delivered() {
		if err := h.stateMgr.UpdateLastNotificationWithStop(keys.stateKey, status, message, stopHash); err != nil {
			logging.Warn("Failed to update last notification: %v", err)
		}
	} else {
		logging.Debug("No notification delivery was recorded (all channels disabled, suppressed, or failed)")
	}

	logging.Debug("=== Hook completed: %s ===", hookEvent)
	return nil
}

// handlePreToolUse handles PreToolUse hook
func (h *Handler) handlePreToolUse(ev Event, p PreToolUsePayload) analyzer.Status {
	logging.Debug("PreToolUse: tool_name='%s'", p.ToolName)

	status := analyzer.GetStatusForPreToolUse(p.ToolName)

	// Write session state BEFORE returning (prevents race with Notification hook)
	// This matches bash version behavior: state is written BEFORE notification is sent
	if status == analyzer.StatusPlanReady || status == analyzer.StatusQuestion {
		if err := h.stateMgr.UpdateInteractiveTool(ev.Session.SessionID, p.ToolName, ev.Session.CWD); err != nil {
			logging.Warn("Failed to update interactive tool state: %v", err)
		} else {
			logging.Debug("PreToolUse: session state written (tool=%s)", p.ToolName)
		}
	}

	return status
}

// handleTeammateIdle handles the TeammateIdle hook event.
// Records the teammate as idle, checks if all teammates are idle + lead stopped,
// and sends a notification when both conditions are met.
func (h *Handler) handleTeammateIdle(ev Event, p TeammateIdlePayload) error {
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
		logging.Warn("TeammateIdle: failed to record idle state: %v", err)
		return nil
	}

	// Check if all conditions are met: lead stopped + all teammates idle
	allIdle, err := h.teamStateMgr.CheckAllIdle(p.TeamName, teamInfo.Members)
	if err != nil {
		logging.Warn("TeammateIdle: failed to check team idle state: %v", err)
		return nil
	}

	if !allIdle {
		logging.Debug("TeammateIdle: not all conditions met yet for team %q", p.TeamName)
		return nil
	}

	// All conditions met — send notification
	logging.Debug("TeammateIdle: all teammates idle + lead stopped for team %q, sending notification", p.TeamName)

	if err := h.teamStateMgr.MarkNotified(p.TeamName); err != nil {
		logging.Warn("TeammateIdle: failed to mark team notified: %v", err)
	}

	status := analyzer.StatusTaskComplete
	body := fmt.Sprintf("Team %q: all teammates finished work", p.TeamName)

	h.sendNotifications(status, body, "", ev.Session.SessionID, ev.Session.CWD)

	logging.Debug("=== Hook completed: TeammateIdle (team notification sent) ===")
	return nil
}

func skipUTF8BOM(input io.Reader) io.Reader {
	reader := bufio.NewReader(input)
	prefix, err := reader.Peek(3)
	if err == nil && bytes.Equal(prefix, []byte{0xEF, 0xBB, 0xBF}) {
		_, _ = reader.Discard(3)
	}
	return reader
}

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

	logging.Debug("Analyzed status: %s", status)
	return status, messages, nil
}

// Claude Code can fire Stop before the final assistant message is flushed to
// the transcript. Its payload carries that message directly, so use it only
// when transcript analysis has no status and text-only notifications are on.
func (h *Handler) fallbackClaudeStop(status analyzer.Status, message string) (analyzer.Status, *TurnInsight) {
	if status != analyzer.StatusUnknown || strings.TrimSpace(message) == "" {
		return status, nil
	}
	// Preserve a short host-reported error even if text-only notifications are
	// disabled. Claude's transcript classifier handles API errors first.
	classified := analyzer.ClassifyLastMessage(message)
	switch classified {
	case analyzer.StatusAPIError, analyzer.StatusAPIErrorOverloaded, analyzer.StatusSessionLimitReached:
		status = classified
	default:
		if !h.cfg.ShouldNotifyOnTextResponse() {
			return analyzer.StatusUnknown, nil
		}
		// Claude text-only Stop is task_complete; questions use Notification.
		status = analyzer.StatusTaskComplete
	}
	// Match the normal transcript summary so a replay after the final line
	// flushes has the same content-dedup key.
	synthetic := []jsonl.Message{{
		Type: "assistant",
		Message: jsonl.MessageContent{Role: "assistant", Content: []jsonl.Content{{
			Type: "text", Text: message,
		}}},
	}}
	body, _ := summary.GenerateFromMessagesStructured(synthetic, status, h.cfg)
	return status, &TurnInsight{Body: body}
}

// The user timestamp distinguishes separate turns with identical replies.
// It is already present when a normal Claude transcript has reached Stop;
// empty is used for no-session-persistence turns without a transcript.
func claudeStopPayloadHash(final, userTimestamp string) string {
	material := userTimestamp + "\x00" + strings.TrimSpace(final)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(material)))
}

// generateMessage generates a notification body and action summary.
// If messages are provided (from handleStopEvent), uses them directly to avoid re-reading the transcript.
func (h *Handler) generateMessage(ev Event, status analyzer.Status, messages []jsonl.Message, insight *TurnInsight) (body, actions string) {
	// Codex bodies come from payload fields, never from the Claude-format
	// transcript parser: the Codex rollout JSONL is a different schema.
	if ev.Product == ProductCodex {
		if insight != nil && insight.Body != "" {
			return insight.Body, ""
		}
		return h.generateCodexMessage(ev, status), ""
	}
	if insight != nil && insight.Body != "" {
		return insight.Body, ""
	}

	// Use pre-parsed messages if available (eliminates ~234ms double I/O)
	if len(messages) > 0 {
		body, actions = summary.GenerateFromMessagesStructured(messages, status, h.cfg)
	} else if ev.Session.TranscriptPath != "" && platform.FileExists(ev.Session.TranscriptPath) {
		// Fallback: read transcript from file (for non-Stop hooks)
		if parsed, err := jsonl.ParseFile(ev.Session.TranscriptPath); err == nil {
			body, actions = summary.GenerateFromMessagesStructured(parsed, status, h.cfg)
		}
	}

	if body == "" {
		body = summary.GenerateSimple(status, h.cfg)
	}
	return body, actions
}

// generateCodexMessage projects a Codex payload into a notification body.
// ToolInput is intentionally never rendered: only the tool identity is safe
// to show without redaction.
func (h *Handler) generateCodexMessage(ev Event, status analyzer.Status) string {
	switch p := ev.Payload.(type) {
	case StopPayload:
		if text := strings.TrimSpace(p.AssistantMessage); text != "" {
			return truncateRunes(summary.CleanMarkdown(text), 150)
		}
	case SubagentStopPayload:
		if text := strings.TrimSpace(p.Stop.AssistantMessage); text != "" {
			return truncateRunes(summary.CleanMarkdown(text), 150)
		}
	case PermissionRequestPayload:
		if p.ToolName != "" {
			return fmt.Sprintf("Codex requests permission: %s", p.ToolName)
		}
	}
	return summary.GenerateSimple(status, h.cfg)
}

// truncateRunes shortens s to at most max runes, appending an ellipsis.
func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

// joinMessageParts mirrors summary.appendActions: joins body and actions with a
// single space when actions is non-empty.
func joinMessageParts(body, actions string) string {
	if actions == "" {
		return body
	}
	return body + " " + actions
}

// sendNotifications sends desktop and webhook notifications and reports whether
// at least one user-visible channel was queued or delivered.
//
// body is the summary text (no metadata prefix, no action segments).
// actions is the formatted action summary (e.g. "📝 1 new  ▶ 2 cmds  ⏱ 41s") or "".
func (h *Handler) sendNotifications(status analyzer.Status, body, actions, sessionID, cwd string) notificationDelivery {
	// Add panic recovery to prevent notification failures from crashing the plugin
	defer errorhandler.HandlePanic()

	var delivery notificationDelivery

	sessionName := sessionname.GenerateSessionLabel(sessionID)
	gitBranch := platform.GetGitBranch(cwd)
	folderName := filepath.Base(cwd)

	joined := joinMessageParts(body, actions)

	// Format: "[sessionname|branch folder] message" or "[sessionname folder] message".
	// When neither the session ID nor the cwd carried any usable info (e.g. a
	// Codex event that arrived without session_id/cwd), skip the metadata block
	// entirely instead of showing a garbage "[unknown .]" prefix.
	var enhancedMessage string
	switch {
	case sessionName == "unknown" && gitBranch == "" && folderName == ".":
		enhancedMessage = joined
	case gitBranch != "":
		enhancedMessage = fmt.Sprintf("[%s|%s %s] %s", sessionName, gitBranch, folderName, joined)
	default:
		enhancedMessage = fmt.Sprintf("[%s %s] %s", sessionName, folderName, joined)
	}

	logging.Debug("Session name: %s, git branch: %s, folder: %s", sessionName, gitBranch, folderName)

	statusStr := string(status)

	// Send webhook notification first (async, check per-status enabled). Webhook
	// delivery is independent of the desktop focus/delay handling below, so the
	// notifyDelaySeconds grace period never holds it up.
	if h.cfg.IsStatusWebhookEnabled(statusStr) {
		h.webhookSvc.SendAsyncWithContext(webhook.SendContext{
			Status:        status,
			Message:       enhancedMessage,
			SessionID:     sessionID,
			CWD:           cwd,
			SessionName:   sessionName,
			GitBranch:     gitBranch,
			Folder:        folderName,
			RawBody:       body,
			ActionSummary: actions,
			AgentSource:   string(h.product),
		})
		delivery.webhookQueued = true
	} else {
		logging.Debug("Webhook notification disabled for status: %s", statusStr)
	}

	// Send desktop notification (check per-status enabled)
	if h.cfg.IsStatusDesktopEnabled(statusStr) {
		delivery.desktopDelivered = h.sendDesktopNotification(status, enhancedMessage, sessionID, cwd)
	} else {
		logging.Debug("Desktop notification disabled for status: %s", statusStr)
	}

	return delivery
}

// sendDesktopNotification delivers the desktop notification, honoring the
// notifyDelaySeconds grace period and the notifyOnlyWhenUnfocused suppression
// from issue #93.
//
// When notifyDelaySeconds > 0 the hook waits that many seconds (bounded by
// maxNotifyDelaySeconds to stay within the hook timeout) before delivering, so a
// quick task you are already watching can finish before any banner appears. When
// notifyOnlyWhenUnfocused is set, the notification is dropped if the terminal
// window has OS focus at delivery time - checked after the delay, so the two
// options compose into "only notify once I have looked away". Both options are
// independent and default off; webhook delivery is unaffected.
func (h *Handler) sendDesktopNotification(status analyzer.Status, message, sessionID, cwd string) bool {
	if delay := h.cfg.GetNotifyDelaySeconds(); delay > 0 {
		if delay > maxNotifyDelaySeconds {
			logging.Warn("notifyDelaySeconds=%d exceeds the hook timeout budget; clamping to %ds", delay, maxNotifyDelaySeconds)
			delay = maxNotifyDelaySeconds
		}
		logging.Debug("Delaying desktop notification by %ds", delay)
		sleepFunc(time.Duration(delay) * time.Second)
	}

	if h.cfg.ShouldNotifyOnlyWhenUnfocused() && isTerminalFocused(sessionID, cwd) {
		logging.Debug("Desktop notification suppressed: terminal window has focus")
		return false
	}

	if err := h.notifierSvc.SendDesktop(status, message, sessionID, cwd); err != nil {
		h.maybeEmitDesktopPermissionGuidance(err)
		errorhandler.HandleError(err, "Failed to send desktop notification")
		return false
	}

	return true
}

// isSubagentTranscript checks if the transcript path indicates a subagent session.
// Claude Code stores subagent transcripts in paths containing /subagents/ segment.
func isSubagentTranscript(transcriptPath string) bool {
	// Normalize path separators for cross-platform compatibility
	normalized := filepath.ToSlash(transcriptPath)
	return strings.Contains(normalized, "/subagents/")
}

// cleanupOldLocks cleans up old lock and state files but preserves session state for cooldown
func (h *Handler) cleanupOldLocks() {
	// Cleanup old locks (older than 60 seconds)
	if err := h.dedupMgr.Cleanup(60); err != nil {
		logging.Warn("Failed to cleanup old locks: %v", err)
	}

	// Cleanup old state files (older than 60 seconds)
	if err := h.stateMgr.Cleanup(60); err != nil {
		logging.Warn("Failed to cleanup old state files: %v", err)
	}
}

func (h *Handler) maybeEmitDesktopPermissionGuidance(err error) {
	// Observation routes (Codex) must never write to stdout; the guidance is
	// a Claude Code systemMessage feature.
	if h.product == ProductCodex {
		return
	}

	if !platform.IsMacOS() {
		return
	}

	var permissionErr *notifier.NotificationPermissionDeniedError
	if !errors.As(err, &permissionErr) {
		return
	}

	if !h.shouldEmitPermissionGuidance() {
		return
	}

	message := "[agent-notifications] macOS is blocking Agent Notifications. Open System Settings > Notifications > Agent Notifications and enable notifications. This can happen after older ad-hoc installs or stale notification permissions."
	fmt.Printf("{\"systemMessage\":%q}\n", message)
}

func (h *Handler) shouldEmitPermissionGuidance() bool {
	cacheDir, err := os.UserCacheDir()
	if err != nil || cacheDir == "" {
		return true
	}

	stampDir := filepath.Join(cacheDir, "claude-notifications-go")
	stampPath := filepath.Join(stampDir, "macos-notification-permission-reminder")

	if info, err := os.Stat(stampPath); err == nil {
		if time.Since(info.ModTime()) < 24*time.Hour {
			return false
		}
	}

	if err := os.MkdirAll(stampDir, 0o755); err != nil {
		return true
	}
	if err := os.WriteFile(stampPath, []byte(time.Now().Format(time.RFC3339)), 0o644); err != nil {
		return true
	}

	return true
}

func configAgent(product Product) (config.AgentID, error) {
	switch product {
	case ProductClaude:
		return config.AgentClaude, nil
	case ProductCodex:
		return config.AgentCodex, nil
	default:
		return "", fmt.Errorf("unsupported product: %q", product)
	}
}
