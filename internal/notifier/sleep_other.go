//go:build !darwin

package notifier

// desktopDisplayIsAsleep reports display-sleep state on platforms where the
// plugin has no detector yet, which means it always reports "not asleep" and
// notifications are delivered with their sound.
//
// Linux and Windows both expose display power state, but only through
// desktop/compositor-specific and driver-specific sources with no single
// portable query, so they are tracked as follow-up work rather than guessed
// at here - a detector that is wrong in the "asleep" direction silently
// swallows notifications, which is the one failure mode this feature must
// not have.
//
// Adding a platform means dropping in a sleep_<goos>.go with its own
// desktopDisplayIsAsleep and excluding that GOOS from this file's build tag.
// No change to the config, hook, or notifier API is required.
func desktopDisplayIsAsleep() bool {
	return false
}
