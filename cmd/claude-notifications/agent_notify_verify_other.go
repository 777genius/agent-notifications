//go:build !linux

package main

import (
	"context"
	"fmt"

	"github.com/777genius/agent-notifications/internal/notification"
)

func verifyAgentNotifyLinuxBinding(context.Context, notification.LinuxBinding) error {
	return fmt.Errorf("Linux callback unavailable on this platform")
}
