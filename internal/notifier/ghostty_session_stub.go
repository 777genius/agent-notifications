//go:build !darwin

package notifier

func MaybeCaptureGhosttyTerminalID(configOverride, sessionID, cwd string) {}

func MaybeCaptureGhosttyTerminalIDOnPrompt(configOverride, sessionID, cwd string) {}

func loadStoredGhosttyTerminalID(sessionID string) string {
	return ""
}
