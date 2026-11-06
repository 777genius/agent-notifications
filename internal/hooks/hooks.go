package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/benchmark"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/dedup"
	"github.com/777genius/agent-notifications/internal/errorhandler"
	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/notifier"
	"github.com/777genius/agent-notifications/internal/platform"
	"github.com/777genius/agent-notifications/internal/state"
	"github.com/777genius/agent-notifications/internal/teamstate"
	"github.com/777genius/agent-notifications/internal/webhook"
	"github.com/777genius/agent-notifications/pkg/jsonl"
)

// HookData represents the data received from Claude Code hooks
type HookData struct {
	SessionTitle         json.RawMessage `json:"session_title,omitempty"`
	ToolInput            json.RawMessage `json:"tool_input,omitempty"`
	NotificationType     string          `json:"notification_type,omitempty"`
	Message              string          `json:"message,omitempty"`
	TranscriptPath       string          `json:"transcript_path"`
	LastAssistantMessage string          `json:"last_assistant_message,omitempty"`
	SessionID            string          `json:"session_id"`
	CWD                  string          `json:"cwd"`
	ToolName             string          `json:"tool_name,omitempty"`
	HookEventName        string          `json:"hook_event_name,omitempty"`
	ErrorType            string          `json:"error_type,omitempty"`
	// Team-related fields (present in TeammateIdle, TaskCreated, TaskCompleted hooks)
	TeamName     string `json:"team_name,omitempty"`
	TeammateName string `json:"teammate_name,omitempty"`
}

// NativeTitle tolerates missing or malformed optional title metadata.
func (d HookData) NativeTitle() string {
	var title string
	_ = json.Unmarshal(d.SessionTitle, &title)
	return title
}

// notifierInterface defines the interface for sending desktop notifications
type notifierInterface interface {
	SendDesktop(status analyzer.Status, message, sessionID, cwd string, opts ...notifier.SendOption) error
	Close() error
}

// webhookInterface defines the interface for sending webhook notifications
type webhookInterface interface {
	SendAsyncWithContext(sendCtx webhook.SendContext)
	Shutdown(timeout time.Duration) error
}

// Handler handles hook events
type Handler struct {
	cfg           *config.Config
	dedupMgr      *dedup.Manager
	stateMgr      *state.Manager
	teamStateMgr  *teamstate.Manager
	notifierSvc   notifierInterface
	webhookSvc    webhookInterface
	pluginRoot    string
	product       Product
	source        EventSource
	terminalFocus notifier.SendOption // request-local verified Qoder action, nil if unqualified
	// codexEnricher is the DIP seam for Codex turn classification; nil
	// selects the built-in payload heuristic. See CodexTurnEnricher.
	codexEnricher CodexTurnEnricher
	// Test seam for a deterministic append between JSONL parse and settle.
	afterClaudeTranscriptParse func()
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

// NewHandlerWithClaudeSource creates a Claude handler with an explicit event
// source while retaining NewHandler's stderr warning contract for config
// diagnostics.
func NewHandlerWithClaudeSource(pluginRoot string, source EventSource) (*Handler, error) {
	cfg, err := config.LoadForAgent(pluginRoot, config.AgentClaude)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	return newHandlerWithConfig(pluginRoot, cfg, ProductClaude, source)
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

// RestrictChannels intersects effective product configuration with installer
// consent before handling the event. Both delivery services share this config.
func (h *Handler) RestrictChannels(desktop, webhook bool) {
	h.cfg.Notifications.Desktop.Enabled = h.cfg.Notifications.Desktop.Enabled && desktop
	h.cfg.Notifications.Webhook.Enabled = h.cfg.Notifications.Webhook.Enabled && webhook
}

// BindManagedTerminalClick is available only to a Qoder hook that already
// holds its verified installed policy/channel lease. Legacy unmanaged hooks
// and remote/headless sessions cannot attach a click action.
func (h *Handler) BindManagedTerminalClick(ctx context.Context, root, installationID string, generation uint64) {
	if h == nil || h.product != ProductQoder || h.cfg == nil || !h.cfg.IsDesktopEnabled() {
		return
	}
	if origin := notifier.CaptureLocalTerminal(ctx, h.cfg, config.AgentQoder, root, installationID, generation); origin != nil {
		h.terminalFocus = notifier.WithTerminalTarget(origin)
	}
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
	if ev.Product == ProductQoder {
		return qoderKeys(ev)
	}
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
	// Claude Stop has no explicit turn id. Its turn-scoped lock key can only be
	// derived after reading the transcript; a session-scoped early lock would
	// suppress a second legitimate turn completed within two seconds.
	deferClaudeStopLock := ev.Product == ProductClaude && ev.Kind() == EventStop
	if !deferClaudeStopLock && h.dedupMgr.CheckEarlyDuplicate(keys.lockKey, hookEvent) {
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

	// Product classification owns transcript/team access and interactive state.
	classified, err := h.classifyHook(ev, keys, hookEvent, bench)
	if err != nil || classified == nil {
		return err
	}
	if classified.cleanup {
		defer h.cleanupOldLocks()
	}
	status, parsedMessages := classified.status, classified.messages
	if deferClaudeStopLock {
		if userTS := jsonl.GetLastUserTimestamp(parsedMessages); userTS != "" {
			keys.lockKey = claudeStopTurnLockKey(ev.Session.SessionID, userTS)
		}
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

	content, ok := h.prepareHookContent(ev, keys, hookEvent, classified, bench)
	if !ok {
		return nil
	}

	// Send notifications
	bench.Start("notify.send")
	recordedDelivery := false
	delivery := h.sendNotifications(status, content.body, content.actions, ev.Session.SessionID, ev.Session.CWD, func() {
		recordedDelivery = true
		if err := h.stateMgr.UpdateLastNotificationWithIdentity(keys.stateKey, status, content.message, content.stopHash, content.body, content.turnTS, hookEvent); err != nil {
			logging.Warn("Failed to update last notification: %v", err)
		}
	}, h.notificationDetails(ev, classified.insight))
	bench.Elapsed("notify.send")

	if !recordedDelivery && !delivery.delivered() {
		logging.Debug("No notification delivery was recorded (all channels disabled, suppressed, or failed)")
	}

	logging.Debug("=== Hook completed: %s ===", hookEvent)
	return nil
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

func configAgent(product Product) (config.AgentID, error) {
	switch product {
	case ProductClaude:
		return config.AgentClaude, nil
	case ProductCodex:
		return config.AgentCodex, nil
	case ProductQoder:
		return config.AgentQoder, nil
	default:
		return "", fmt.Errorf("unsupported product: %q", product)
	}
}
