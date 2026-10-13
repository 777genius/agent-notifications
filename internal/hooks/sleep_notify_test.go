package hooks

import (
	"testing"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sleepNotifyConfig returns a minimal config with desktop notifications enabled
// and respectDisplaySleep set as requested. A nil value leaves the option
// unset, which is the shipped default.
func sleepNotifyConfig(respectDisplaySleep *bool) *config.Config {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = true
	cfg.Notifications.RespectDisplaySleep = respectDisplaySleep
	return cfg
}

// stubDisplayAsleep replaces the display-sleep seam and reports how often it
// was consulted.
func stubDisplayAsleep(t *testing.T, asleep bool) *int {
	t.Helper()
	calls := 0
	restore := isDisplayAsleep
	isDisplayAsleep = func() bool {
		calls++
		return asleep
	}
	t.Cleanup(func() { isDisplayAsleep = restore })
	return &calls
}

func TestSendDesktopNotification_DisplaySleepNotConsultedWhenOptionOff(t *testing.T) {
	// respectDisplaySleep unset (default) - display sleep state must never be
	// queried, so the hook path pays nothing for a feature that is switched off.
	handler, mockNotif, _ := newTestHandler(t, sleepNotifyConfig(nil))
	calls := stubDisplayAsleep(t, true)

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.True(t, delivered, "sent notification should be recorded as delivered")
	assert.True(t, mockNotif.wasCalled(), "notification must be delivered when the option is off")
	assert.Zero(t, *calls, "display sleep must not be checked when respectDisplaySleep is off")
	if call := mockNotif.lastCall(); assert.NotNil(t, call) {
		assert.Zero(t, call.optionCount, "delivery must carry no options when the option is off")
	}
}

func TestSendDesktopNotification_DisplaySleepNotConsultedWhenExplicitlyDisabled(t *testing.T) {
	handler, mockNotif, _ := newTestHandler(t, sleepNotifyConfig(boolPtr(false)))
	calls := stubDisplayAsleep(t, true)

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.True(t, delivered)
	assert.True(t, mockNotif.wasCalled())
	assert.Zero(t, *calls, "respectDisplaySleep=false must behave exactly like unset")
}

func TestSendDesktopNotification_MutesWhileDisplayAsleep(t *testing.T) {
	handler, mockNotif, _ := newTestHandler(t, sleepNotifyConfig(boolPtr(true)))
	calls := stubDisplayAsleep(t, true)

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.True(t, delivered, "the banner is still delivered so it reaches the notification centre")
	require.True(t, mockNotif.wasCalled())
	assert.Equal(t, 1, *calls, "display sleep should be consulted exactly once")
	if call := mockNotif.lastCall(); assert.NotNil(t, call) {
		assert.Equal(t, 1, call.optionCount, "a muted delivery must request WithoutSound")
	}
}

func TestSendDesktopNotification_DeliversWithSoundWhileDisplayAwake(t *testing.T) {
	handler, mockNotif, _ := newTestHandler(t, sleepNotifyConfig(boolPtr(true)))
	require.NotZero(t, stubDisplayAsleep(t, false))

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.True(t, delivered)
	require.True(t, mockNotif.wasCalled())
	if call := mockNotif.lastCall(); assert.NotNil(t, call) {
		assert.Zero(t, call.optionCount, "an awake display must not mute the notification")
	}
}

// TestSendDesktopNotification_FocusSuppressionSkipsDisplaySleepProbe mirrors the
// DND version of this contract: an already-suppressed notification must not pay
// for a display-sleep probe either.
func TestSendDesktopNotification_FocusSuppressionSkipsDisplaySleepProbe(t *testing.T) {
	on := true
	cfg := sleepNotifyConfig(boolPtr(true))
	cfg.Notifications.NotifyOnlyWhenUnfocused = &on
	handler, mockNotif, _ := newTestHandler(t, cfg)

	restoreFocus := isTerminalFocused
	isTerminalFocused = func(_, _ string) bool { return true }
	defer func() { isTerminalFocused = restoreFocus }()

	calls := stubDisplayAsleep(t, true)

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.False(t, delivered)
	assert.False(t, mockNotif.wasCalled())
	assert.Zero(t, *calls, "a notification already suppressed by focus must not probe display sleep")
}

// TestSendDesktopNotification_DNDMuteSkipsDisplaySleepProbe pins the
// cheapest-decisive-first order between the two mute sources: once Do Not
// Disturb has already muted the sound, checking display sleep cannot change
// the outcome, so it is skipped.
func TestSendDesktopNotification_DNDMuteSkipsDisplaySleepProbe(t *testing.T) {
	silent := "silent"
	cfg := sleepNotifyConfig(boolPtr(true))
	cfg.Notifications.RespectDoNotDisturb = &silent
	handler, mockNotif, _ := newTestHandler(t, cfg)

	stubDoNotDisturb(t, true)
	calls := stubDisplayAsleep(t, true)

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.True(t, delivered)
	require.True(t, mockNotif.wasCalled())
	assert.Zero(t, *calls, "display sleep must not be probed once DND already muted the sound")
	if call := mockNotif.lastCall(); assert.NotNil(t, call) {
		assert.Equal(t, 1, call.optionCount, "the delivery must still be muted exactly once, not twice")
	}
}

// TestSendDesktopNotification_DisplaySleepMutesWhenDNDDoesNot checks the reverse
// composition: a machine outside DND can still have its display asleep, and the
// mute must apply independently.
func TestSendDesktopNotification_DisplaySleepMutesWhenDNDDoesNot(t *testing.T) {
	off := "off"
	cfg := sleepNotifyConfig(boolPtr(true))
	cfg.Notifications.RespectDoNotDisturb = &off
	handler, mockNotif, _ := newTestHandler(t, cfg)

	dndCalls := stubDoNotDisturb(t, false)
	sleepCalls := stubDisplayAsleep(t, true)

	delivered := handler.sendDesktopNotification(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd")

	assert.True(t, delivered)
	require.True(t, mockNotif.wasCalled())
	assert.Zero(t, *dndCalls, `DND must not be checked for respectDoNotDisturb="off"`)
	assert.Equal(t, 1, *sleepCalls)
	if call := mockNotif.lastCall(); assert.NotNil(t, call) {
		assert.Equal(t, 1, call.optionCount, "display sleep alone must still mute the delivery")
	}
}
