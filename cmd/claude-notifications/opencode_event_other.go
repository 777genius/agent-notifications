//go:build !linux && !windows && !darwin

package main

import "github.com/777genius/agent-notifications/internal/notification"

// Other platforms stay unavailable until they have a native delivery port.
func newOpenCodeDesktopPort() notification.DeliveryPort { return nil }
