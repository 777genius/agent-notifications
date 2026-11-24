//go:build !darwin

package opencodeinstall

import (
	"context"
	"errors"

	"github.com/777genius/agent-notifications/internal/desktopinstall"
)

func PrepareNativeSpool(context.Context, string) (string, error) {
	return "", errors.New("OpenCode native spool requires macOS")
}

func PrepareNativeSpoolFor(ctx context.Context, controlRoot string, product DesktopProduct) (string, error) {
	return desktopinstall.PrepareNativeSpoolFor(ctx, controlRoot, product)
}

func SetupPermission(context.Context, string, bool) (string, error) {
	return "unavailable", errors.New("OpenCode native permission requires macOS")
}
