package geminiinstall

import (
	"context"

	"github.com/777genius/agent-notifications/internal/desktopinstall"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// GeminiToastAppID is shared by actual shortcut staging/readiness and the
// trusted Windows toast factory supplied by the business consumer lane.
const (
	GeminiToastAppID = desktopinstall.GeminiToastAppID
	consumerID       = desktopinstall.GeminiConsumerID
)

func PrepareNativeSpool(ctx context.Context, controlRoot string) (string, error) {
	return desktopinstall.PrepareNativeSpoolFor(ctx, controlRoot, desktopinstall.GeminiDesktop)
}

func StageWindowsShortcut(home, target string, desktop bool, ledger installruntime.Ledger) (*installruntime.File, error) {
	return desktopinstall.StageWindowsShortcut(desktopinstall.GeminiDesktop, home, target, desktop, ledger)
}

func WindowsShortcutReady(controlRoot, executable string) error {
	return desktopinstall.WindowsShortcutReadyFor(desktopinstall.GeminiDesktop, controlRoot, executable)
}
