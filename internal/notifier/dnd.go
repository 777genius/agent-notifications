package notifier

// IsDoNotDisturb reports whether the desktop session is currently in a
// Do Not Disturb / notification-inhibited state.
//
// Like IsTerminalFocused, it is deliberately conservative: it returns true ONLY
// when it can positively confirm that notifications are inhibited. Any
// uncertainty - an unsupported platform or desktop, a notification daemon that
// exposes no DND state, a D-Bus error, a timeout, or an unparseable value -
// returns false, so the notification and its sound are delivered.
//
// This bias matters: a wrong "in DND" result silently swallows a cue the user is
// waiting for, whereas a wrong "not in DND" result merely plays one sound the
// user did not want. The failure mode is therefore always an extra sound, never
// a missing notification.
//
// Detection is implemented on Linux (see dnd_linux.go). Every other platform
// reports "not in DND"; see docs/DO_NOT_DISTURB.md for the per-platform status.
func IsDoNotDisturb() bool {
	return desktopIsDoNotDisturb()
}
