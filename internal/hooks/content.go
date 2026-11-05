package hooks

import (
	"strings"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/benchmark"
	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/platform"
	"github.com/777genius/agent-notifications/pkg/jsonl"
)

// hookContent holds rendering and dedup facts for this invocation only.
// It is not an event contract or a persisted state format.
type hookContent struct{ body, actions, message, stopHash, turnTS string }

func (h *Handler) prepareHookContent(ev Event, keys eventKeys, hookEvent string, classified *hookClassification, bench *benchmark.Context) (hookContent, bool) {
	status, parsedMessages := classified.status, classified.messages
	var err error
	// Notification and PreToolUse also need the current Claude turn identity.
	// Reusing these messages for summary generation avoids a second read.
	if ev.Product == ProductClaude && len(parsedMessages) == 0 &&
		ev.Session.TranscriptPath != "" && platform.FileExists(ev.Session.TranscriptPath) {
		if messages, parseErr := jsonl.ParseFile(ev.Session.TranscriptPath); parseErr == nil {
			parsedMessages = messages
		}
	}
	turnTS := ""
	if ev.Product == ProductClaude {
		turnTS = jsonl.GetLastUserTimestamp(parsedMessages)
	}

	// Generate message
	bench.Start("message.generate")
	body, actions := h.generateMessage(ev, status, parsedMessages, classified.insight)
	message := joinMessageParts(body, actions)
	var stopHash string
	if ev.Product == ProductClaude && ev.Kind() == EventStop {
		// The host final text is stable before and after the transcript flush.
		// The last user timestamp separates identical replies in distinct turns.
		// Without that timestamp there is no trustworthy turn identity, so fall
		// back to the legacy rendered-content check instead.
		if p, ok := ev.Payload.(StopPayload); ok && strings.TrimSpace(p.AssistantMessage) != "" && turnTS != "" {
			stopHash = claudeStopPayloadHash(p.AssistantMessage, turnTS)
		}
	}
	if ev.Product == ProductQoder && ev.Kind() == EventStopFailure {
		// Qoder has no turn id. A host replay repeats the exact native payload,
		// while an independent failure carries its own error details. Only the
		// digest of class and payload is persisted, never the raw error text.
		stopHash = claudeStopPayloadHash(string(ev.Raw), "qoder-stop-failure\x00"+string(status))
	}
	bench.Elapsed("message.generate")

	// Acquire content lock to prevent race between different hooks (Stop vs Notification)
	// This ensures only one process can check and update duplicate state at a time
	// Distinct Codex and Qoder prompts and subagents use event-scoped dedup
	// only; a session content lock held by another hook must not drop them.
	// Claude retains its original session locking, including permissions.
	skipContentLock := (ev.Product == ProductCodex || ev.Product == ProductQoder) && (status == analyzer.StatusPermissionRequest ||
		ev.Kind() == EventPreToolUse || ev.Kind() == EventSubagentStop || ev.Kind() == EventStopFailure)
	contentLockAcquired := false
	if !skipContentLock {
		contentLockAcquired, err = h.dedupMgr.AcquireContentLock(keys.stateKey)
		if err != nil {
			logging.Warn("Failed to acquire content lock: %v", err)
			// Error (not "lock busy") - continue without lock as fallback
		} else if !contentLockAcquired {
			// Lock is held by another process - it's already handling this notification
			logging.Warn("Content lock held by another process: session=%s hook=%s (notification skipped)", keys.stateKey, hookEvent)
			return hookContent{}, false
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
	// - Qoder StopFailure: an actual failure must not collapse into a previous
	//   successful Stop with identical copy. A replay of the same native
	//   payload after the event lock expires is bounded by its payload digest.
	// Turn-level duplicates of the event itself stay bounded by the
	// turn+tool/agent(+call)-scoped dedup lock.
	skipContentDedup := status == analyzer.StatusPermissionRequest ||
		(ev.Product == ProductCodex && (ev.Kind() == EventPreToolUse || ev.Kind() == EventSubagentStop))
	if ev.Product == ProductQoder && ev.Kind() == EventStopFailure {
		isDuplicateFailure, failureErr := h.stateMgr.IsDuplicateStopPayload(keys.stateKey, stopHash, 180)
		if failureErr != nil {
			logging.Warn("Failed to check duplicate Qoder StopFailure payload: %v", failureErr)
		} else if isDuplicateFailure {
			logging.Debug("Duplicate Qoder StopFailure payload detected within 3 minutes, skipping")
			return hookContent{}, false
		}
	} else if !skipContentDedup {
		isDuplicateStop, stopErr := h.stateMgr.IsDuplicateStopPayload(keys.stateKey, stopHash, 180)
		if stopErr != nil {
			logging.Warn("Failed to check duplicate Stop payload: %v", stopErr)
		} else if isDuplicateStop {
			logging.Debug("Duplicate Stop payload detected within 3 minutes, skipping")
			return hookContent{}, false
		}
		if ev.Product == ProductClaude && turnTS != "" &&
			(ev.Kind() == EventStop || ev.Kind() == EventNotification) {
			// The body/turn pair is stable across transcript flushes and hook
			// types. In particular, a duration suffix must not make the same
			// answer from Notification look like new content.
			isDuplicate, turnErr := h.stateMgr.IsDuplicateTurnBody(keys.stateKey, body, turnTS, hookEvent, 180)
			if turnErr != nil {
				logging.Warn("Failed to check duplicate Claude turn body: %v", turnErr)
			} else if isDuplicate {
				logging.Debug("Duplicate Claude turn body detected within 3 minutes, skipping")
				return hookContent{}, false
			}
			if !isDuplicate {
				// Interactive prompts keep the old rendered-message check. A Stop
				// after another hook in this turn needs it too: for example,
				// ExitPlanMode and Stop can both render the same plan-ready banner.
				// Do not apply it to a later Stop turn with an identical answer.
				checkLegacy := ev.Kind() == EventNotification
				if ev.Kind() == EventStop {
					previous, loadErr := h.stateMgr.Load(keys.stateKey)
					if loadErr != nil {
						logging.Warn("Failed to load previous Claude notification: %v", loadErr)
					} else if previous != nil && previous.LastNotificationTurn == turnTS &&
						previous.LastNotificationEvent != "Stop" && previous.LastNotificationEvent != "Notification" {
						checkLegacy = true
					}
				}
				if checkLegacy {
					isDuplicate, err := h.stateMgr.IsDuplicateMessage(keys.stateKey, message, 180)
					if err != nil {
						logging.Warn("Failed to check duplicate message: %v", err)
					} else if isDuplicate {
						logging.Debug("Duplicate message content detected within 3 minutes, skipping")
						return hookContent{}, false
					}
				}
			}
		} else {
			// Without a turn identity, retain the conservative legacy content
			// check. Distinct identical answers cannot be proven in this mode.
			isDuplicate, err := h.stateMgr.IsDuplicateMessage(keys.stateKey, message, 180)
			if err != nil {
				logging.Warn("Failed to check duplicate message: %v", err)
			} else if isDuplicate {
				logging.Debug("Duplicate message content detected within 3 minutes, skipping")
				return hookContent{}, false
			}
		}
	}

	// Release the cross-hook content lock before any delivery work. Desktop
	// delivery may intentionally sleep for notifyDelaySeconds, and holding this
	// lock during that delay would make concurrent hooks skip notifications.
	releaseContentLock()

	return hookContent{body: body, actions: actions, message: message, stopHash: stopHash, turnTS: turnTS}, true
}
