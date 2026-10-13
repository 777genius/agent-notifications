//go:build !linux && !windows && !darwin

package main

import (
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/opencodeevent"
)

// Other platforms stay unavailable until they have a native delivery port.
func newOpenCodeDesktopPort(_ *opencodeevent.Handoff, _ string) notification.DeliveryPort { return nil }
