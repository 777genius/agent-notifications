package notifier

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/logging"
)

const iTerm2SessionIDEnv = "ITERM_SESSION_ID"

const (
	iTerm2HealthcheckFlag           = "--healthcheck"
	iTerm2HealthcheckExitDisabled   = 11
	iTerm2HealthcheckExitModuleMiss = 12
	iTerm2HealthcheckExitOther      = 13
)

var sendQuickNotification = SendQuickNotification

var (
	iTerm2HealthcheckSuccessTTL = 10 * time.Minute
	iTerm2PythonAPIPromptTTL    = 24 * time.Hour
	iTerm2PythonAPIHealthcheck  = checkIterm2PythonAPIHealth
)

type iTerm2HelperHealth int

const (
	iTerm2HelperUnavailable iTerm2HelperHealth = iota
	iTerm2HelperReady
	iTerm2HelperDisabled
)

// buildIterm2FocusScript prefers iTerm2's exact session reveal URL when the
// current shell exported ITERM_SESSION_ID. This targets the precise tab/pane
// via the iTerm2 Python API helper. If the helper is unavailable or the exact
// session can no longer be resolved, it falls back to app-level activation
// instead of focus-window to avoid confusing Screen Recording prompts on iTerm2.
func buildIterm2FocusScript(cwd string) string {
	sessionID := os.Getenv(iTerm2SessionIDEnv)
	pythonPath, scriptPath, ok := getiTerm2PythonEnv()
	if ok && (sessionID != "" || isUsableFocusCWD(cwd)) &&
		iTerm2PythonAPIHealthcheck(pythonPath, scriptPath) == iTerm2HelperReady {
		helperCmd := fmt.Sprintf("%s %s",
			shellQuote(pythonPath),
			shellQuote(scriptPath),
		)
		if sessionID != "" {
			helperCmd += " --termid " + shellQuote(sessionID)
		}
		if isUsableFocusCWD(cwd) {
			helperCmd += " --cwd " + shellQuote(cwd)
		}

		// If the exact helper fails at click time, keep the fallback at simple
		// app activation instead of switching to focus-window, which would ask
		// for Screen Recording even though the underlying iTerm2 issue is the
		// Python API helper.
		return fmt.Sprintf("%s >/dev/null 2>&1 || open -a iTerm", helperCmd)
	}

	return ""
}

func checkIterm2PythonAPIHealth(pythonPath, scriptPath string) iTerm2HelperHealth {
	if isRecentMarker(iTerm2HealthcheckSuccessMarkerPath(), iTerm2HealthcheckSuccessTTL) {
		return iTerm2HelperReady
	}

	cmd := execCommand(pythonPath, scriptPath, iTerm2HealthcheckFlag)
	output, err := cmd.CombinedOutput()
	outputText := stringsTrimSpace(string(output))
	if err == nil {
		touchMarker(iTerm2HealthcheckSuccessMarkerPath())
		return iTerm2HelperReady
	}

	_ = os.Remove(iTerm2HealthcheckSuccessMarkerPath())

	var exitErr interface{ ExitCode() int }
	if errors.As(err, &exitErr) && exitErr.ExitCode() == iTerm2HealthcheckExitDisabled {
		logging.Warn("iTerm2 Python API is disabled: %s", outputText)
		promptIterm2PythonAPIDisabled()
		return iTerm2HelperDisabled
	}

	if errors.As(err, &exitErr) && exitErr.ExitCode() == iTerm2HealthcheckExitModuleMiss {
		logging.Warn("iTerm2 Python helper module missing: %s", outputText)
		return iTerm2HelperUnavailable
	}

	if errors.As(err, &exitErr) && exitErr.ExitCode() == iTerm2HealthcheckExitOther {
		logging.Warn("iTerm2 Python helper healthcheck failed: %s", outputText)
		if shouldPromptIterm2PythonAPI(outputText) {
			promptIterm2PythonAPIDisabled()
		}
		return iTerm2HelperUnavailable
	}

	logging.Warn("iTerm2 Python helper healthcheck failed: %v output=%s", err, outputText)
	if shouldPromptIterm2PythonAPI(outputText) {
		promptIterm2PythonAPIDisabled()
	}
	return iTerm2HelperUnavailable
}

func shouldPromptIterm2PythonAPI(output string) bool {
	if output == "" {
		return false
	}

	return strings.Contains(output, "problem connecting to iTerm2") ||
		strings.Contains(output, "Ensure the Python API is enabled") ||
		strings.Contains(output, "private/socket")
}

func isUsableFocusCWD(cwd string) bool {
	if cwd == "" {
		return false
	}
	folderName := filepath.Base(cwd)
	return folderName != "" && folderName != "." && folderName != string(filepath.Separator)
}

func iTerm2HealthcheckSuccessMarkerPath() string {
	stableDir, err := config.GetStableConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(stableDir, ".iterm2-python-api-ok")
}

func iTerm2PythonAPIPromptMarkerPath() string {
	stableDir, err := config.GetStableConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(stableDir, ".iterm2-python-api-disabled-prompted")
}

func isRecentMarker(path string, ttl time.Duration) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < ttl
}

func touchMarker(path string) {
	if path == "" {
		return
	}
	dir := filepath.Dir(path)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(path, []byte("1"), 0o644)
}

func promptIterm2PythonAPIDisabled() {
	markerPath := iTerm2PythonAPIPromptMarkerPath()
	if isRecentMarker(markerPath, iTerm2PythonAPIPromptTTL) {
		return
	}

	touchMarker(markerPath)
	_ = sendQuickNotification(
		"iTerm2 Python API Disabled",
		"Enable iTerm2 > Settings > General > Magic > Enable Python API. If you just toggled it, restart iTerm2 so clicks can open the exact tab or pane again.",
		`open -a iTerm`,
	)
}

func stringsTrimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\n' || s[start] == '\t' || s[start] == '\r') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\n' || s[end-1] == '\t' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
