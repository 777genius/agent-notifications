package hooks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/dedup"
	"github.com/777genius/agent-notifications/internal/qodersource"
	"github.com/777genius/agent-notifications/internal/teamstate"
)

// Exercise real SDK decode and policy together: continuation, duplicates and
// product isolation must not notify twice or parse foreign transcript files.
func TestQoderStopFlow(t *testing.T) {
	session := fmt.Sprintf("../../qoder-%d", time.Now().UnixNano())
	handler, notify, _ := newTestHandler(t, codexTestConfig())
	handler.product = ProductQoder
	handler.source = QoderSource{}
	payload := func(id string, active bool) string {
		return fmt.Sprintf(`{"session_id":%q,"cwd":"/TEST-qoder","hook_event_name":"Stop","transcript_path":"/must-not-read","stop_hook_active":%t,"last_assistant_message":"Done"}`, id, active)
	}
	if err := handler.HandleHook("Stop", strings.NewReader(payload(session, true))); err != nil {
		t.Fatal(err)
	}
	if notify.wasCalled() {
		t.Fatal("continuation notified")
	}
	if err := handler.HandleHook("Stop", strings.NewReader(payload(session, false))); err != nil {
		t.Fatal(err)
	}
	call := notify.lastCall()
	if call == nil || call.status != analyzer.StatusTaskComplete || !strings.Contains(call.message, "Done") {
		t.Fatalf("missing completion: %+v", call)
	}
	before := len(notify.calls)
	if err := handler.HandleHook("Stop", strings.NewReader(payload(session, false))); err != nil {
		t.Fatal(err)
	}
	if len(notify.calls) != before {
		t.Fatal("duplicate notified")
	}
	ev, err := (QoderSource{}).Decode(context.Background(), "Stop", strings.NewReader(payload(session, false)))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Session.TranscriptPath != "" || strings.Contains(qoderKeys(ev).stateKey, "../") || qoderKeys(ev).stateKey == codexKeys(ev).stateKey {
		t.Fatal("foreign transcript or unsafe/cross-product identity")
	}
	if err := handler.HandleHook("Stop", strings.NewReader(payload(session+"-other", false))); err != nil {
		t.Fatal(err)
	}
	if len(notify.calls) != before+1 {
		t.Fatal("another session suppressed")
	}
}

func qoderFailureTestConfig() *config.Config {
	cfg := codexTestConfig()
	cfg.Statuses[string(analyzer.StatusAgentError)] = config.StatusInfo{Title: "Qoder stopped unexpectedly"}
	cfg.Statuses[string(analyzer.StatusAPIErrorOverloaded)] = config.StatusInfo{Title: "Qoder rate limited"}
	return cfg
}

// These hook-policy fixtures keep invocation locks logically fresh while
// exercising real atomic files. Native lock expiration is covered by dedup tests.
func qoderFreshLocks() *dedup.Manager {
	return dedup.NewManagerWithFileAge(func(string) int64 { return 0 })
}

// An actual Qoder StopFailure is not a successful Stop, and host error text
// may contain credentials/requests. Only the fixed class reaches delivery.
func TestQoderStopFailureRoutesWithoutLeakingRawProviderError(t *testing.T) {
	handler, notify, _ := newTestHandler(t, qoderFailureTestConfig())
	handler.dedupMgr = qoderFreshLocks()
	handler.product, handler.source = ProductQoder, QoderSource{}
	for _, tc := range []struct {
		errorType string
		want      analyzer.Status
	}{
		{"rate_limit", analyzer.StatusAPIErrorOverloaded},
		{"authentication_failed", analyzer.StatusAgentError},
		{"server_error", analyzer.StatusAgentError},
	} {
		session := fmt.Sprintf("../../qoder-stop-failure-%s-%d", tc.errorType, time.Now().UnixNano())
		input := fmt.Sprintf(`{"session_id":%q,"cwd":"/TEST-qoder","hook_event_name":"StopFailure","error_type":%q,"error":"PRIVATE_API_KEY_TEST","error_details":"PRIVATE_REQUEST_TEST","transcript_path":"/must-not-read","last_assistant_message":"PRIVATE_COMPLETION_TEST"}`, session, tc.errorType)
		if err := handler.HandleHook("StopFailure", strings.NewReader(input)); err != nil {
			t.Fatal(err)
		}
		call := notify.lastCall()
		if call == nil || call.status != tc.want {
			t.Fatalf("Qoder StopFailure %q status %+v; want %q", tc.errorType, call, tc.want)
		}
		for _, secret := range []string{"PRIVATE_API_KEY_TEST", "PRIVATE_REQUEST_TEST", "PRIVATE_COMPLETION_TEST", "must-not-read"} {
			if strings.Contains(call.message, secret) {
				t.Fatalf("native Qoder error leaked provider content: %q", call.message)
			}
		}
		before := len(notify.calls)
		if err := handler.HandleHook("StopFailure", strings.NewReader(input)); err != nil {
			t.Fatal(err)
		}
		if len(notify.calls) != before {
			t.Fatal("duplicate failure created second alert")
		}
	}
}

// A successful turn and an unexpected failure are distinct user events even
// when the assistant content looks like the fixed failure banner. The
// event-scoped lock still suppresses an exact repeated failure invocation.
func TestQoderStopFailureDoesNotCollideWithCompletedTurnDedup(t *testing.T) {
	handler, notify, _ := newTestHandler(t, qoderFailureTestConfig())
	handler.dedupMgr = qoderFreshLocks()
	handler.product, handler.source = ProductQoder, QoderSource{}
	session := fmt.Sprintf("../../qoder-stop-vs-failure-%d", time.Now().UnixNano())
	stop := fmt.Sprintf(`{"session_id":%q,"cwd":"/TEST-qoder","hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":"Qoder stopped unexpectedly"}`, session)
	failure := fmt.Sprintf(`{"session_id":%q,"cwd":"/TEST-qoder","hook_event_name":"StopFailure","error_type":"server_error","error":"PRIVATE_ERROR"}`, session)
	if err := handler.HandleHook("Stop", strings.NewReader(stop)); err != nil {
		t.Fatal(err)
	}
	if err := handler.HandleHook("StopFailure", strings.NewReader(failure)); err != nil {
		t.Fatal(err)
	}
	if len(notify.calls) != 2 || notify.lastCall() == nil || notify.lastCall().status != analyzer.StatusAgentError {
		t.Fatalf("Qoder failure collapsed into prior successful Stop: %+v", notify.calls)
	}
	if err := handler.HandleHook("StopFailure", strings.NewReader(failure)); err != nil {
		t.Fatal(err)
	}
	if len(notify.calls) != 2 {
		t.Fatalf("event-scoped duplicate failure was redelivered: %d", len(notify.calls))
	}
}

func TestQoderStopFailureCannotImpersonateOtherEventsOrProducts(t *testing.T) {
	for _, payload := range []struct{ event, raw string }{
		{"StopFailure", `{"session_id":"s","hook_event_name":"Stop","error_type":"rate_limit"}`},
		{"StopFailure", `{"hook_event_name":"StopFailure","error_type":"rate_limit"}`},
		{"Stop", `{"session_id":"s","hook_event_name":"StopFailure","error_type":"rate_limit"}`},
	} {
		if _, err := (QoderSource{}).Decode(context.Background(), payload.event, strings.NewReader(payload.raw)); err == nil {
			t.Fatalf("accepted mismatched Qoder event %q", payload.event)
		}
	}
	if err := ValidateEvent(Event{Product: ProductClaude, PayloadEventName: "StopFailure", Session: SessionContext{SessionID: "s"}, Payload: StopFailurePayload{RateLimited: true}}); err == nil {
		t.Fatal("Claude impersonated Qoder-only failure event")
	}
}

func TestQoderDecodeRejectsUnsupportedAndOversized(t *testing.T) {
	for _, tc := range []struct{ event, input string }{
		{"PermissionDenied", `{"session_id":"s","hook_event_name":"PermissionDenied"}`},
		{"Stop", `{"session_id":"s","hook_event_name":"UserPromptSubmit"}`},
		{"Stop", `{"hook_event_name":"Stop"}`},
		{"Stop", strings.Repeat("x", qodersource.MaxPayloadBytes+1)},
	} {
		if _, err := (QoderSource{}).Decode(context.Background(), tc.event, strings.NewReader(tc.input)); err == nil {
			t.Fatalf("accepted invalid payload event %s", tc.event)
		}
	}
}

func TestQoderAttentionHookFlow(t *testing.T) {
	handler, notify, _ := newTestHandler(t, codexTestConfig())
	handler.product = ProductQoder
	handler.source = QoderSource{}
	payload := func(event, session, extra string) string {
		return fmt.Sprintf(`{"session_id":%q,"cwd":"/TEST-qoder","hook_event_name":%q%s}`, session, event, extra)
	}

	// Notification routes to question status (needs-attention).
	if err := handler.HandleHook("Notification", strings.NewReader(payload("Notification", fmt.Sprintf("../../qoder-attn-n-%d", time.Now().UnixNano()), ""))); err != nil {
		t.Fatal(err)
	}
	if call := notify.lastCall(); call == nil || call.status != analyzer.StatusQuestion {
		t.Fatalf("notification not routed to question: %+v", call)
	}

	// PermissionRequest routes to permission status with the Qoder label.
	session := fmt.Sprintf("../../qoder-attn-p-%d", time.Now().UnixNano())
	if err := handler.HandleHook("PermissionRequest", strings.NewReader(payload("PermissionRequest", session, `,"tool_name":"Bash"`))); err != nil {
		t.Fatal(err)
	}
	call := notify.lastCall()
	if call == nil || call.status != analyzer.StatusPermissionRequest || !strings.Contains(call.message, "Qoder requests permission: Bash") {
		t.Fatalf("permission request not routed: %+v", call)
	}
	if strings.Contains(call.message, "Codex") {
		t.Fatalf("permission request leaked the wrong product label: %q", call.message)
	}

	// PreToolUse for ExitPlanMode routes to plan ready.
	if err := handler.HandleHook("PreToolUse", strings.NewReader(payload("PreToolUse", fmt.Sprintf("../../qoder-attn-e-%d", time.Now().UnixNano()), `,"tool_name":"ExitPlanMode"`))); err != nil {
		t.Fatal(err)
	}
	if call := notify.lastCall(); call == nil || call.status != analyzer.StatusPlanReady {
		t.Fatalf("ExitPlanMode not routed to plan ready: %+v", call)
	}

	// Discriminator: PreToolUse for a non-interactive tool must not notify.
	before := len(notify.calls)
	if err := handler.HandleHook("PreToolUse", strings.NewReader(payload("PreToolUse", fmt.Sprintf("../../qoder-attn-b-%d", time.Now().UnixNano()), `,"tool_name":"Bash"`))); err != nil {
		t.Fatal(err)
	}
	if len(notify.calls) != before {
		t.Fatal("non-interactive PreToolUse tool notified")
	}
}

func TestQoderSubagentStopOptIn(t *testing.T) {
	payload := func(session string) string {
		return fmt.Sprintf(`{"session_id":%q,"cwd":"/TEST-qoder","hook_event_name":"SubagentStop","last_assistant_message":"Sub done"}`, session)
	}

	// Discriminator: opt-in disabled must stay silent.
	handler, notify, _ := newTestHandler(t, codexTestConfig())
	handler.product = ProductQoder
	handler.source = QoderSource{}
	if err := handler.HandleHook("SubagentStop", strings.NewReader(payload(fmt.Sprintf("../../qoder-sub-off-%d", time.Now().UnixNano())))); err != nil {
		t.Fatal(err)
	}
	if notify.wasCalled() {
		t.Fatal("subagent stop notified without opt-in")
	}

	// Opt-in enabled notifies with the subagent completion.
	cfg := codexTestConfig()
	cfg.Notifications.NotifyOnSubagentStop = true
	noSuppress := false
	cfg.Notifications.SuppressForSubagents = &noSuppress
	handler, notify, _ = newTestHandler(t, cfg)
	handler.product = ProductQoder
	handler.source = QoderSource{}
	if err := handler.HandleHook("SubagentStop", strings.NewReader(payload(fmt.Sprintf("../../qoder-sub-on-%d", time.Now().UnixNano())))); err != nil {
		t.Fatal(err)
	}
	call := notify.lastCall()
	if call == nil || call.status != analyzer.StatusTaskComplete || !strings.Contains(call.message, "Sub done") {
		t.Fatalf("subagent completion missing: %+v", call)
	}
}

func TestQoderAttentionDecodeRejectsMismatch(t *testing.T) {
	for _, tc := range []struct{ event, input string }{
		{"Notification", `{"session_id":"s","hook_event_name":"Stop"}`},
		{"PermissionRequest", `{"session_id":"s","hook_event_name":"PermissionRequest2"}`},
		{"SubagentStop", `{"hook_event_name":"SubagentStop"}`},
		{"TeammateIdle", `{"session_id":"s","hook_event_name":"SessionStart"}`},
		{"PreToolUse", `{"session_id":"s"}`},
		{"SessionStart", `{"session_id":"s","hook_event_name":"SessionStart"}`},
		{"Notification", strings.Repeat("x", qodersource.MaxPayloadBytes+1)},
	} {
		if _, err := (QoderSource{}).Decode(context.Background(), tc.event, strings.NewReader(tc.input)); err == nil {
			t.Fatalf("accepted invalid payload event %s", tc.event)
		}
	}
}

func TestQoderTeammateIdleDecodeAndGuard(t *testing.T) {
	handler, notify, _ := newTestHandler(t, codexTestConfig())
	handler.product = ProductQoder
	handler.source = QoderSource{}
	// Missing team identity must stay silent even when decoded correctly.
	if err := handler.HandleHook("TeammateIdle", strings.NewReader(`{"session_id":"../../qoder-ti-1","cwd":"/TEST-qoder","hook_event_name":"TeammateIdle"}`)); err != nil {
		t.Fatal(err)
	}
	if notify.wasCalled() {
		t.Fatal("incomplete TeammateIdle notified")
	}
	ev, err := (QoderSource{}).Decode(context.Background(), "TeammateIdle", strings.NewReader(`{"session_id":"s","cwd":"/TEST-qoder","hook_event_name":"TeammateIdle","team_name":"t","teammate_name":"m"}`))
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := ev.Payload.(TeammateIdlePayload); !ok || p.TeamName != "t" || p.TeammateName != "m" {
		t.Fatalf("teammate payload not decoded: %+v", ev.Payload)
	}
	if ev.Session.TranscriptPath != "" {
		t.Fatal("qoder decode leaked transcript path")
	}
}

// A session content lock held by another hook models a Stop/Notification race.
// Qoder prompts and subagent stops must still deliver, exactly like Codex;
// completions keep the session lock so a racing Stop cannot double-notify.
func TestQoderEventsDeliverWhileContentLockHeld(t *testing.T) {
	for _, kind := range []string{"PermissionRequest", "PreToolUse", "SubagentStop", "StopFailure", "Stop"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("TMPDIR", root)
			t.Setenv("TMP", root)
			t.Setenv("TEMP", root)
			session := fmt.Sprintf("../../qoder-held-%s-%d", strings.ToLower(kind), time.Now().UnixNano())
			body := ""
			switch kind {
			case "PermissionRequest":
				body = `,"tool_name":"Bash"`
			case "PreToolUse":
				body = `,"tool_name":"ExitPlanMode"`
			case "SubagentStop":
				body = `,"last_assistant_message":"Sub done"`
			case "StopFailure":
				body = `,"error_type":"server_error","error":"PRIVATE_NATIVE_ERROR"`
			case "Stop":
				body = `,"stop_hook_active":false,"last_assistant_message":"Done"`
			}
			payload := fmt.Sprintf(`{"session_id":%q,"cwd":"/TEST-qoder","hook_event_name":%q%s}`, session, kind, body)
			cfg := qoderFailureTestConfig()
			cfg.Notifications.NotifyOnSubagentStop = true
			noSuppress := false
			cfg.Notifications.SuppressForSubagents = &noSuppress
			h, notifier, _ := newTestHandler(t, cfg)
			h.dedupMgr = qoderFreshLocks()
			h.product = ProductQoder
			h.source = QoderSource{}
			ev, err := h.source.Decode(context.Background(), kind, strings.NewReader(payload))
			if err != nil {
				t.Fatal(err)
			}
			key := qoderKeys(ev).stateKey
			ok, err := h.dedupMgr.AcquireContentLock(key)
			if err != nil || !ok {
				t.Fatalf("hold lock: %v %v", ok, err)
			}
			lockPath := filepath.Join(root, fmt.Sprintf("claude-notification-%s-content.lock", key))
			before, err := os.Stat(lockPath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := h.dedupMgr.ReleaseContentLock(key); err != nil {
					t.Error(err)
				}
			}()
			if err := h.HandleHook(kind, strings.NewReader(payload)); err != nil {
				t.Fatal(err)
			}
			want := 1
			if kind == "Stop" {
				want = 0
			}
			if got := notifier.callCount(); got != want {
				t.Fatalf("deliveries while lock held = %d, want %d", got, want)
			}
			after, err := os.Stat(lockPath)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("handler removed or replaced another event's lock: %v", err)
			}
		})
	}
}

// Qoder must use its hashed identity for every state write, including the
// interactive-tool update before notification admission. Raw IDs are display
// metadata and must neither mutate Claude state nor become filesystem paths.
func TestQoderInteractiveStateUsesOnlyProductIdentity(t *testing.T) {
	for _, session := range []string{"shared-session", "part/../path-like"} {
		t.Run(session, func(t *testing.T) {
			root := t.TempDir()
			for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
				t.Setenv(key, root)
			}
			h, _, _ := newTestHandler(t, codexTestConfig())
			h.product, h.source = ProductQoder, QoderSource{}
			rawPath := filepath.Join(root, "claude-session-state-"+session+".json")
			var original []byte
			if session == "shared-session" {
				if err := h.stateMgr.UpdateInteractiveTool(session, "CLAUDE_SENTINEL", "/claude"); err != nil {
					t.Fatal(err)
				}
				var err error
				original, err = os.ReadFile(rawPath)
				if err != nil {
					t.Fatal(err)
				}
			}
			input := fmt.Sprintf(`{"session_id":%q,"cwd":"/qoder","hook_event_name":"PreToolUse","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Continue?"}]}}`, session)
			if err := h.HandleHook("PreToolUse", strings.NewReader(input)); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(rawPath)
			if original == nil {
				if !os.IsNotExist(err) {
					t.Fatalf("Qoder raw path was written: %s (error %v)", rawPath, err)
				}
			} else if err != nil || string(raw) != string(original) {
				t.Fatalf("Qoder changed Claude state: %s (error %v)", raw, err)
			}
			ev, err := h.source.Decode(context.Background(), "PreToolUse", strings.NewReader(input))
			if err != nil {
				t.Fatal(err)
			}
			state, err := h.stateMgr.Load(qoderKeys(ev).stateKey)
			if err != nil || state == nil || state.LastInteractiveTool != "AskUserQuestion" || state.CWD != "/qoder" {
				t.Fatalf("Qoder interactive state missing from product identity: %+v (error %v)", state, err)
			}
		})
	}
}

// A Qoder team name is not authority to use a Claude team. Reject that path
// without consuming its claim; the subsequent genuine Claude event still owns
// and delivers the existing wait-all completion.
func TestQoderTeammateIdleCannotConsumeClaudeCompletion(t *testing.T) {
	root := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, root)
	}
	claudeRoot := t.TempDir()
	teamDir := filepath.Join(claudeRoot, "teams", "same-name")
	if err := os.MkdirAll(teamDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "config.json"), []byte(`{"name":"same-name","leadSessionId":"claude-lead","members":[{"name":"worker","agentType":"worker"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := codexTestConfig()
	cfg.Notifications.TeamMode = "wait-all"
	h, notify, _ := newTestHandler(t, cfg)
	h.product, h.source = ProductQoder, QoderSource{}
	h.teamStateMgr = teamstate.NewManager(claudeRoot)
	if err := h.teamStateMgr.RecordLeadStopped("same-name"); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "claude-team-notify-same-name.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	input := `{"session_id":"qoder-session","cwd":"/qoder","hook_event_name":"TeammateIdle","team_name":"same-name","teammate_name":"worker"}`
	if err := h.HandleHook("TeammateIdle", strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(statePath)
	if err != nil || string(after) != string(before) || notify.callCount() != 0 {
		t.Fatalf("Qoder affected Claude team: deliveries=%d before=%s after=%s error=%v", notify.callCount(), before, after, err)
	}
	h.product, h.source = ProductClaude, ClaudeSource{}
	input = `{"session_id":"claude-lead","cwd":"/claude","hook_event_name":"TeammateIdle","team_name":"same-name","teammate_name":"worker"}`
	if err := h.HandleHook("TeammateIdle", strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	state, err := h.teamStateMgr.LoadState("same-name")
	if err != nil || state == nil || state.LeadStopped || state.NotifiedAt == 0 || notify.callCount() != 1 {
		t.Fatalf("Claude completion changed: state=%+v deliveries=%d error=%v", state, notify.callCount(), err)
	}
}
