package notification

// WithTerminalClick chooses a verified local terminal only after the
// agent-specific consumer has admitted the event. The selected desktop port
// still validates the origin at native handoff, and the callback rechecks it
// again when clicked. Non-qualified routes remain informational.
func WithTerminalClick(request Request, target *TerminalTarget, authorized bool) Request {
	if !authorized || target == nil {
		return request
	}
	origin := *target
	request.Terminal = &origin
	request.Navigation = BestEffort
	request.Policy.ClickToFocus = true
	return request
}
