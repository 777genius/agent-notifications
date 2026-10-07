//go:build !linux

package notifier

// desktopIsDoNotDisturb reports the desktop's Do Not Disturb state on platforms
// where the plugin has no detector yet, which means it always reports "unknown"
// (false) and notifications are delivered with their sound.
//
// macOS (Focus modes) and Windows (Focus Assist / quiet hours) both expose the
// state only through undocumented, version-fragile sources, so they are tracked
// as follow-up work rather than guessed at here - a detector that is wrong in
// the "in DND" direction silently swallows notifications, which is the one
// failure mode this feature must not have. See docs/DO_NOT_DISTURB.md.
//
// Adding a platform means dropping in a dnd_<goos>.go with its own
// desktopIsDoNotDisturb and excluding that GOOS from this file's build tag. No
// change to the config, hook, or notifier API is required.
func desktopIsDoNotDisturb() bool {
	return false
}
