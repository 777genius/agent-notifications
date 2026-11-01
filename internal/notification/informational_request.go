package notification

// InformationalDesktopRequest builds a navigation-free native desktop request
// after the agent-specific consumer has checked registration, channel consent,
// status enablement, source identity and its one-shot delivery claim.
// It does not authorize a desktop effect on its own.
func InformationalDesktopRequest(content Content, correlationID string, deadline Deadline, systemSound bool) Request {
	return Request{
		Content:       content,
		CorrelationID: correlationID,
		Deadline:      deadline,
		Policy: PolicySnapshot{
			Valid: true, ExplicitEnabled: true, DesktopEnabled: true,
			SoundEnabled: systemSound,
		},
		Navigation: None,
		Silent:     !systemSound,
	}
}
