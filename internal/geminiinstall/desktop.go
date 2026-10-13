package geminiinstall

import (
	"context"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/opencodeinstall"
)

// GeminiToastAppID is shared by actual shortcut staging/readiness and the
// trusted Windows toast factory supplied by the business consumer lane.
const (
	GeminiToastAppID = opencodeinstall.GeminiToastAppID
	consumerID       = opencodeinstall.GeminiConsumerID
)

func PrepareNativeSpool(ctx context.Context, controlRoot string) (string, error) {
	return opencodeinstall.PrepareNativeSpoolFor(ctx, controlRoot, opencodeinstall.GeminiDesktop)
}

func StageWindowsShortcut(home, target string, desktop bool, ledger installruntime.Ledger) (*installruntime.File, error) {
	return opencodeinstall.StageWindowsShortcut(opencodeinstall.GeminiDesktop, home, target, desktop, ledger)
}

func WindowsShortcutReady(controlRoot, executable string) error {
	return opencodeinstall.WindowsShortcutReadyFor(opencodeinstall.GeminiDesktop, controlRoot, executable)
}
