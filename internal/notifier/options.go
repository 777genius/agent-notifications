package notifier

import "context"

// SendOption customises a single desktop notification delivery.
type SendOption func(*sendOptions)

type sendOptions struct {
	muteSound                 bool
	hookPresentation          *HookPresentation
	executionContext          context.Context
	runtimeRoot, nativeHelper string
}

// WithInstalledRuntime supplies paths already verified by the installation
// owner under its retained lease. Event/model fields must never supply them.
func WithInstalledRuntime(root, helper string) SendOption {
	return func(o *sendOptions) { o.runtimeRoot, o.nativeHelper = root, helper }
}

// HookPresentation carries literal hook content without the legacy bracket
// envelope. It affects presentation only, never navigation or consent.
type HookPresentation struct {
	SessionName string
	Branch      string
	Folder      string
	Body        string
	Question    string
}

func WithHookPresentation(content HookPresentation) SendOption {
	return func(o *sendOptions) { o.hookPresentation = &content }
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
