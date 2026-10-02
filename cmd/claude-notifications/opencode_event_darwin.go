//go:build darwin

package main

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
	"github.com/777genius/agent-notifications/internal/opencodeinstall"
)

func newOpenCodeDesktopPort() notification.DeliveryPort {
	root := os.Getenv("AGENT_NOTIFICATIONS_CONTROL_ROOT")
	if root == "" {
		var err error
		root, err = installruntime.ControlRoot()
		if err != nil {
			return nil
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s, err := installruntime.ReadPolicySnapshot(ctx, root)
	if err != nil {
		return nil
	}
	return notifier.NewStructuredDelivery(opencodeinstall.NativeInstallation{
		ControlRoot: root, Expected: s, Executable: executable,
	}, filepath.Join(root, "opencode-native-spool"))
}
