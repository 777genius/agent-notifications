//go:build !linux

package main

import "github.com/777genius/agent-notifications/internal/notification"

// PR3 initially qualifies Linux only. Other platforms stay unavailable.
func newOpenCodeDesktopPort() notification.DeliveryPort { return nil }
