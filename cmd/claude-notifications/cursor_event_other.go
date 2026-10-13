//go:build !linux && !windows

package main

import (
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/notification"
)

func newCursorDesktopPort(portable.Binding, string) notification.DeliveryPort { return nil }
