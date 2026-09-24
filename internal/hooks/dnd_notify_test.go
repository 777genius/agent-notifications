package hooks

import (
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dndNotifyConfig returns a minimal config with desktop notifications enabled and
// respectDoNotDisturb set as requested. A nil mode leaves the option unset, which
// is the shipped default.
func dndNotifyConfig(mode *string, delaySeconds *int) *config.Config {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = true
	cfg.Notifications.RespectDoNotDisturb = mode
	cfg.Notifications.NotifyDelaySeconds = delaySeconds
	return cfg
}

// stubDoNotDisturb replaces the DND seam and reports how often it was consulted.
func stubDoNotDisturb(t *testing.T, active bool) *int {
	t.Helper()
	calls := 0
	restore := isDoNotDisturb
	isDoNotDisturb = func() bool {
		calls++
		return active
	}
	t.Cleanup(func() { isDoNotDisturb = restore })
	return &calls
}

func dndMode(mode string) *string { return &mode }

func TestSendDesktopNotification_DNDNotConsultedWhenOptionOff(t *testing.T) {
	// respectDoNotDisturb unset (default) - DND state must never be queried, so
	// the hook path pays nothing for a feature that is switched off.
	handler, mockNotif, _ := newTestHandler(t, dndNotifyConfig(nil, nil))
	calls := stubDoNotDisturb(t, true)

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.True(t, delivered, "sent notification should be recorded as delivered")
	assert.True(t, mockNotif.wasCalled(), "notification must be delivered when the option is off")
	assert.Zero(t, *calls, "DND must not be checked when respectDoNotDisturb is off")
	if call := mockNotif.lastCall(); assert.NotNil(t, call) {
		assert.Zero(t, call.optionCount, "delivery must carry no options when the option is off")
	}
}

func TestSendDesktopNotification_DNDNotConsultedForExplicitOff(t *testing.T) {
	handler, mockNotif, _ := newTestHandler(t, dndNotifyConfig(dndMode("off"), nil))
	calls := stubDoNotDisturb(t, true)

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.True(t, delivered)
	assert.True(t, mockNotif.wasCalled())
	assert.Zero(t, *calls, `DND must not be checked for respectDoNotDisturb="off"`)
}

func TestSendDesktopNotification_DNDNotConsultedForUnknownMode(t *testing.T) {
	// An unrecognised value degrades to "off" rather than to a mode that could
	// swallow notifications.
	handler, mockNotif, _ := newTestHandler(t, dndNotifyConfig(dndMode("yes please"), nil))
	calls := stubDoNotDisturb(t, true)

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.True(t, delivered)
	assert.True(t, mockNotif.wasCalled())
	assert.Zero(t, *calls, "an unknown respectDoNotDisturb value must behave exactly like off")
}

func TestSendDesktopNotification_SuppressModeDropsNotificationDuringDND(t *testing.T) {
	handler, mockNotif, _ := newTestHandler(t, dndNotifyConfig(dndMode("suppress"), nil))
	calls := stubDoNotDisturb(t, true)

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.False(t, delivered, "suppressed notification must not be recorded as delivered")
	assert.False(t, mockNotif.wasCalled(), "suppress mode must deliver nothing while DND is active")
	assert.Equal(t, 1, *calls, "DND should be consulted exactly once")
}

func TestSendDesktopNotification_SuppressModeDeliversOutsideDND(t *testing.T) {
	handler, mockNotif, _ := newTestHandler(t, dndNotifyConfig(dndMode("suppress"), nil))
	stubDoNotDisturb(t, false)

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.True(t, delivered)
	require.True(t, mockNotif.wasCalled())
	if call := mockNotif.lastCall(); assert.NotNil(t, call) {
		assert.Zero(t, call.optionCount, "a normal delivery must not be muted")
	}
}

func TestSendDesktopNotification_SilentModeMutesButStillDelivers(t *testing.T) {
	handler, mockNotif, _ := newTestHandler(t, dndNotifyConfig(dndMode("silent"), nil))
	calls := stubDoNotDisturb(t, true)

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.True(t, delivered, "silent mode still delivers the banner so it reaches the notification centre")
	require.True(t, mockNotif.wasCalled())
	assert.Equal(t, 1, *calls, "DND should be consulted exactly once")
	if call := mockNotif.lastCall(); assert.NotNil(t, call) {
		assert.Equal(t, 1, call.optionCount, "silent mode must request a muted delivery (WithoutSound)")
	}
}

func TestSendDesktopNotification_SilentModeDeliversWithSoundOutsideDND(t *testing.T) {
	handler, mockNotif, _ := newTestHandler(t, dndNotifyConfig(dndMode("silent"), nil))
	stubDoNotDisturb(t, false)

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.True(t, delivered)
	require.True(t, mockNotif.wasCalled())
	if call := mockNotif.lastCall(); assert.NotNil(t, call) {
		assert.Zero(t, call.optionCount, "no DND means an ordinary delivery, sound included")
	}
}

// TestSendDesktopNotification_DNDCheckedAfterDelay pins the ordering: the state
// is read at delivery time, so toggling DND during notifyDelaySeconds is honoured.
func TestSendDesktopNotification_DNDCheckedAfterDelay(t *testing.T) {
	delay := 5
	handler, mockNotif, _ := newTestHandler(t, dndNotifyConfig(dndMode("suppress"), &delay))

	var order []string

	restoreSleep := sleepFunc
	var slept time.Duration
	sleepFunc = func(d time.Duration) {
		slept = d
		order = append(order, "sleep")
	}
	defer func() { sleepFunc = restoreSleep }()

	restoreDND := isDoNotDisturb
	isDoNotDisturb = func() bool {
		order = append(order, "dnd")
		return true // the user switched DND on during the grace period
	}
	defer func() { isDoNotDisturb = restoreDND }()

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.Equal(t, 5*time.Second, slept, "the delay still runs before the DND check")
	assert.Equal(t, []string{"sleep", "dnd"}, order, "DND must be read after the delay, not before it")
	assert.False(t, delivered)
	assert.False(t, mockNotif.wasCalled())
}

// TestSendDesktopNotification_FocusSuppressionSkipsDNDProbe keeps the two gates
// ordered cheapest-decisive-first: an already-suppressed notification must not
// pay for a DND probe.
func TestSendDesktopNotification_FocusSuppressionSkipsDNDProbe(t *testing.T) {
	on := true
	cfg := dndNotifyConfig(dndMode("suppress"), nil)
	cfg.Notifications.NotifyOnlyWhenUnfocused = &on
	handler, mockNotif, _ := newTestHandler(t, cfg)

	restoreFocus := isTerminalFocused
	isTerminalFocused = func(_, _ string) bool { return true }
	defer func() { isTerminalFocused = restoreFocus }()

	calls := stubDoNotDisturb(t, true)

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.False(t, delivered)
	assert.False(t, mockNotif.wasCalled())
	assert.Zero(t, *calls, "a notification already suppressed by focus must not probe DND")
}

// TestHandleHook_DNDSuppressionDoesNotStartCooldowns mirrors the focus-suppression
// contract: a notification that never reached the desktop must not consume the
// question cooldown budget.
func TestHandleHook_DNDSuppressionDoesNotStartCooldowns(t *testing.T) {
	taskCooldown := 0
	anyCooldown := 30
	cfg := dndNotifyConfig(dndMode("suppress"), nil)
	cfg.Notifications.Webhook.Enabled = false
	cfg.Notifications.SuppressQuestionAfterTaskCompleteSeconds = &taskCooldown
	cfg.Notifications.SuppressQuestionAfterAnyNotificationSeconds = &anyCooldown

	handler, mockNotif, _ := newTestHandler(t, cfg)

	inDND := true
	restore := isDoNotDisturb
	isDoNotDisturb = func() bool { return inDND }
	defer func() { isDoNotDisturb = restore }()

	sessionID := "test-dnd-suppression-state"
	transcriptPath := createTempTranscript(t, buildTranscriptWithTools([]string{"Write"}, 300))
	err := handler.HandleHook("Stop", buildHookDataJSON(HookData{
		SessionID:      sessionID,
		TranscriptPath: transcriptPath,
		CWD:            "/test",
	}))
	require.NoError(t, err)
	assert.False(t, mockNotif.wasCalled(), "DND should suppress the task_complete desktop notification")

	sessionState, err := handler.stateMgr.Load(sessionID)
	require.NoError(t, err)
	if assert.NotNil(t, sessionState) {
		assert.Zero(t, sessionState.LastNotificationTime, "a suppressed notification must not start notification cooldowns")
		assert.Empty(t, sessionState.LastNotificationStatus)
	}

	inDND = false
	err = handler.HandleHook("Notification", buildHookDataJSON(HookData{
		SessionID: sessionID,
		CWD:       "/test",
	}))
	require.NoError(t, err)
	assert.True(t, mockNotif.wasCalled(), "a later question must not be blocked by the suppressed notification")
}
