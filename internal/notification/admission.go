package notification

// AdmissionDecision is the transport-independent validation result for a
// desktop delivery request. Empty Status permits platform-specific checks.
type AdmissionDecision struct {
	Navigation Navigation
	Status     string
	Reason     string
}

// AssessDelivery is the shared admission policy for the native desktop
// backends. It never probes a client, looks up a target, plays sound or sends
// a notification. Platform-specific navigation and receipt authority remain
// with the corresponding DeliveryPort.
func AssessDelivery(r Request) AdmissionDecision {
	nav := r.Navigation
	if nav == "" {
		nav = Required
	}
	out := AdmissionDecision{Navigation: nav}
	switch nav {
	case Required, BestEffort, None:
	default:
		out.Status, out.Reason = "rejected", "malformed_request"
		return out
	}
	if !r.Policy.Valid {
		out.Status, out.Reason = "rejected", "configuration_invalid"
		return out
	}
	if !r.Policy.ExplicitEnabled || !r.Policy.DesktopEnabled {
		out.Status, out.Reason = "suppressed", "disabled"
	}
	return out
}
