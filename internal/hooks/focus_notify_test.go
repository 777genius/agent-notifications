package hooks

import (
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/stretchr/testify/assert"
)

// focusNotifyConfig returns a minimal config with desktop notifications enabled
// and the focus/delay options set as requested.
func focusNotifyConfig(onlyWhenUnfocused *bool, delaySeconds *int) *config.Config {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = true
	cfg.Notifications.NotifyOnlyWhenUnfocused = onlyWhenUnfocused
	cfg.Notifications.NotifyDelaySeconds = delaySeconds
	return cfg
}

func TestSendDesktopNotification_SuppressedWhenFocused(t *testing.T) {
	on := true
	handler, mockNotif, _ := newTestHandler(t, focusNotifyConfig(&on, nil))

	restore := isTerminalFocused
	isTerminalFocused = func(_, _ string) bool { return true }
	defer func() { isTerminalFocused = restore }()

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.False(t, delivered, "suppressed notification should not be recorded as delivered")
	assert.False(t, mockNotif.wasCalled(), "notification should be suppressed when the terminal is focused")
}

func TestSendDesktopNotification_DeliveredWhenUnfocused(t *testing.T) {
	on := true
	handler, mockNotif, _ := newTestHandler(t, focusNotifyConfig(&on, nil))

	restore := isTerminalFocused
	isTerminalFocused = func(_, _ string) bool { return false }
	defer func() { isTerminalFocused = restore }()

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.True(t, delivered, "sent notification should be recorded as delivered")
	assert.True(t, mockNotif.wasCalled(), "notification should be delivered when the terminal is not focused")
}

func TestSendDesktopNotification_FocusIgnoredWhenOptionOff(t *testing.T) {
	// notifyOnlyWhenUnfocused unset (default) - focus state must not be consulted.
	handler, mockNotif, _ := newTestHandler(t, focusNotifyConfig(nil, nil))

	restore := isTerminalFocused
	called := false
	isTerminalFocused = func(_, _ string) bool { called = true; return true }
	defer func() { isTerminalFocused = restore }()

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.True(t, delivered, "sent notification should be recorded as delivered")
	assert.True(t, mockNotif.wasCalled(), "notification should always be delivered when the option is off")
	assert.False(t, called, "focus must not be checked when notifyOnlyWhenUnfocused is off")
}

func TestSendDesktopNotification_DelayUsesConfiguredSeconds(t *testing.T) {
	delay := 7
	handler, mockNotif, _ := newTestHandler(t, focusNotifyConfig(nil, &delay))

	restoreSleep := sleepFunc
	var slept time.Duration
	sleepFunc = func(d time.Duration) { slept = d }
	defer func() { sleepFunc = restoreSleep }()

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.Equal(t, 7*time.Second, slept, "should wait for the configured delay")
	assert.True(t, delivered)
	assert.True(t, mockNotif.wasCalled())
}

func TestSendDesktopNotification_DelayClampedToMax(t *testing.T) {
	delay := maxNotifyDelaySeconds + 100
	handler, _, _ := newTestHandler(t, focusNotifyConfig(nil, &delay))

	restoreSleep := sleepFunc
	var slept time.Duration
	sleepFunc = func(d time.Duration) { slept = d }
	defer func() { sleepFunc = restoreSleep }()

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.Equal(t, time.Duration(maxNotifyDelaySeconds)*time.Second, slept, "delay must be clamped to the hook-timeout budget")
	assert.True(t, delivered)
}

func TestSendDesktopNotification_NoDelayWhenZero(t *testing.T) {
	handler, mockNotif, _ := newTestHandler(t, focusNotifyConfig(nil, nil))

	restoreSleep := sleepFunc
	sleepCalled := false
	sleepFunc = func(d time.Duration) { sleepCalled = true }
	defer func() { sleepFunc = restoreSleep }()

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.False(t, sleepCalled, "no delay should occur when notifyDelaySeconds is unset")
	assert.True(t, delivered)
	assert.True(t, mockNotif.wasCalled())
}

func TestSendDesktopNotification_DelayThenSuppressedOnFocus(t *testing.T) {
	// Combined behavior: wait, then suppress because the terminal regained focus.
	on := true
	delay := 5
	handler, mockNotif, _ := newTestHandler(t, focusNotifyConfig(&on, &delay))

	restoreSleep := sleepFunc
	var slept time.Duration
	sleepFunc = func(d time.Duration) { slept = d }
	defer func() { sleepFunc = restoreSleep }()

	restoreFocus := isTerminalFocused
	isTerminalFocused = func(_, _ string) bool { return true }
	defer func() { isTerminalFocused = restoreFocus }()

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.Equal(t, 5*time.Second, slept, "delay still runs before the focus re-check")
	assert.False(t, delivered, "suppressed notification should not be recorded as delivered")
	assert.False(t, mockNotif.wasCalled(), "notification suppressed after delay because terminal is focused")
}

func TestHandleHook_FocusSuppressionDoesNotRecordLastNotificationOrCooldownQuestion(t *testing.T) {
	on := true
	taskCooldown := 0
	anyCooldown := 30
	cfg := focusNotifyConfig(&on, nil)
	cfg.Notifications.Webhook.Enabled = false
	cfg.Notifications.SuppressQuestionAfterTaskCompleteSeconds = &taskCooldown
	cfg.Notifications.SuppressQuestionAfterAnyNotificationSeconds = &anyCooldown

	handler, mockNotif, _ := newTestHandler(t, cfg)

	focused := true
	restoreFocus := isTerminalFocused
	isTerminalFocused = func(_, _ string) bool { return focused }
	defer func() { isTerminalFocused = restoreFocus }()

	sessionID := "test-focus-suppression-state"
	transcriptPath := createTempTranscript(t, buildTranscriptWithTools([]string{"Write"}, 300))
	err := handler.HandleHook("Stop", buildHookDataJSON(HookData{
		SessionID:      sessionID,
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	}))
	assert.NoError(t, err)
	assert.False(t, mockNotif.wasCalled(), "focused terminal should suppress the task_complete desktop notification")

	sessionState, err := handler.stateMgr.Load(sessionID)
	assert.NoError(t, err)
	if assert.NotNil(t, sessionState) {
		assert.Zero(t, sessionState.LastNotificationTime, "suppressed desktop notification must not start notification cooldowns")
		assert.Empty(t, sessionState.LastNotificationStatus)
		assert.Empty(t, sessionState.LastNotificationMessage)
	}

	focused = false
	err = handler.HandleHook("Notification", buildHookDataJSON(HookData{
		SessionID: sessionID,
		CWD:       "/test",
	}))
	assert.NoError(t, err)
	assert.True(t, mockNotif.wasCalled(), "question notification should not be blocked by a prior suppressed desktop notification")
}

func TestHandleHook_DelayDoesNotHoldContentLock(t *testing.T) {
	delay := 5
	cfg := focusNotifyConfig(nil, &delay)
	cfg.Notifications.Webhook.Enabled = false

	handler, _, _ := newTestHandler(t, cfg)

	restoreSleep := sleepFunc
	slept := make(chan time.Duration, 1)
	releaseSleep := make(chan struct{})
	sleepFunc = func(d time.Duration) {
		slept <- d
		<-releaseSleep
	}
	defer func() { sleepFunc = restoreSleep }()

	sessionID := "test-delay-releases-content-lock"
	transcriptPath := createTempTranscript(t, buildTranscriptWithTools([]string{"Write"}, 300))

	done := make(chan error, 1)
	go func() {
		done <- handler.HandleHook("Stop", buildHookDataJSON(HookData{
			SessionID:      sessionID,
			TranscriptPath: transcriptPath,
			CWD:            "/test",
		}))
	}()

	select {
	case d := <-slept:
		assert.Equal(t, 5*time.Second, d)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for notifyDelaySeconds sleep")
	}

	acquired, err := handler.dedupMgr.AcquireContentLock(sessionID)
	assert.NoError(t, err)
	assert.True(t, acquired, "content lock must be released before notifyDelaySeconds sleep starts")
	if acquired {
		assert.NoError(t, handler.dedupMgr.ReleaseContentLock(sessionID))
	}

	close(releaseSleep)

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for delayed hook to finish")
	}
}
