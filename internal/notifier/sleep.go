package notifier

// IsDisplayAsleep reports whether the desktop's display(s) are currently
// asleep, so callers can mute a sound nobody is present to hear.
//
// Like IsDoNotDisturb, it is deliberately conservative: it returns true ONLY
// when it can positively confirm every active display is asleep. Any
// uncertainty - an unsupported platform, a failed system query, or no
// displays found - returns false, so the notification's sound is delivered.
// A wrong "asleep" result would silently swallow a cue the user is waiting
// for; a wrong "awake" result merely plays one sound nobody heard.
//
// Detection is implemented on macOS (see sleep_darwin.go). Every other
// platform reports "not asleep".
func IsDisplayAsleep() bool {
	return desktopDisplayIsAsleep()
}
