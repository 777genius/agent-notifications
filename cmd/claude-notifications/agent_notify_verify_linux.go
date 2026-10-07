//go:build linux

package main

import (
	"context"

	"github.com/777genius/agent-notifications/internal/linuxcallback"
	"github.com/777genius/agent-notifications/internal/notification"
)

func verifyAgentNotifyLinuxBinding(ctx context.Context, binding notification.LinuxBinding) error {
	if ctx == nil || ctx.Err() != nil {
		return linuxcallback.ErrUnavailable
	}
	snapshot, err := linuxcallback.Load(binding)
	if err != nil {
		return err
	}
	if err = linuxcallback.CheckInstallation(binding, snapshot); err != nil {
		return err
	}
	return linuxcallback.CheckSelectedContext(ctx, snapshot)
}
