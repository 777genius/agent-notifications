//go:build darwin

package opencodeevent

import "github.com/777genius/agent-notifications/internal/notifier"

func (h *Handoff) NativeInstallation() notifier.NativeInstallation {
	return h.lease.NativeInstallation(h.ctx)
}
