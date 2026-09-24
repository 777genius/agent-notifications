package notifier

// SendOption customises a single desktop notification delivery.
type SendOption func(*sendOptions)

type sendOptions struct {
	muteSound bool
}

// WithoutSound delivers the notification without playing the plugin's own audio
// cue. The banner is still sent, so it reaches the desktop's notification
// centre and is visible once the user is available again.
func WithoutSound() SendOption {
	return func(o *sendOptions) { o.muteSound = true }
}

// resolveSendOptions folds the variadic options into a value. A nil option is
// ignored rather than panicking, so a caller building an option slice
// conditionally cannot crash the hook.
func resolveSendOptions(opts []SendOption) sendOptions {
	var resolved sendOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&resolved)
		}
	}
	return resolved
}
