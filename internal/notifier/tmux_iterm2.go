package notifier

import (
	"fmt"
	"os"
	"path/filepath"
)

// getiTerm2PythonEnv returns the absolute paths to the Python interpreter
// inside the iTerm2 venv and the tab-switch helper script.
// Returns ("", "", false) if either is not found.
func getiTerm2PythonEnv() (pythonPath string, scriptPath string, ok bool) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", "", false
	}

	pythonPath = filepath.Join(homeDir, ".claude",
		"claude-notifications-go", "iterm2-venv", "bin", "python3")
	if _, err := os.Stat(pythonPath); err != nil {
		return "", "", false
	}

	pluginRoot := os.Getenv("CLAUDE_PLUGIN_ROOT")
	if pluginRoot == "" {
		return "", "", false
	}
	scriptPath = filepath.Join(pluginRoot, "scripts", "iterm2-select-tab.py")
	if _, err := os.Stat(scriptPath); err != nil {
		return "", "", false
	}

	return pythonPath, scriptPath, true
}

const iTerm2BundleID = "com.googlecode.iterm2"

func isIterm2BundleID(bundleID string) bool {
	return bundleID == iTerm2BundleID
}

// buildIterm2TmuxNotifierArgs constructs terminal-notifier arguments for
// iTerm2 + tmux. The Python helper handles both tmux -CC (via tmuxWindowPane)
// and plain tmux (via tmux client tty fallback) to avoid mutating the wrong
// tmux client in multi-tab setups.
func buildIterm2TmuxNotifierArgs(title, message, paneTarget, bundleID string) ([]string, error) {
	pythonPath, scriptPath, ok := getiTerm2PythonEnv()
	if !ok {
		return nil, fmt.Errorf("iterm2 venv or helper script not found")
	}
	if iTerm2PythonAPIHealthcheck(pythonPath, scriptPath) != iTerm2HelperReady {
		return nil, fmt.Errorf("iterm2 python api unavailable")
	}

	tmuxPath := getTmuxPath()
	socketPath := getTmuxSocketPath()

	executeCmd := fmt.Sprintf("'%s' '%s' --pane '%s' --tmux-path '%s'",
		pythonPath, scriptPath, paneTarget, tmuxPath)
	if socketPath != "" {
		executeCmd = fmt.Sprintf("%s --socket '%s'", executeCmd, socketPath)
	}

	args := []string{
		"-title", title,
		"-message", message,
		"-activate", bundleID,
		"-execute", executeCmd,
	}
	return args, nil
}

// buildTmuxCCNotifierArgs is kept as a compatibility wrapper for the existing
// tmux -CC tests; the helper now supports both control mode and plain tmux.
func buildTmuxCCNotifierArgs(title, message, paneTarget, bundleID string) ([]string, error) {
	return buildIterm2TmuxNotifierArgs(title, message, paneTarget, bundleID)
}
