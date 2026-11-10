//go:build linux

package daemon

import (
	"os/exec"
	"strings"
)

// DetectFocusTools returns a map of available focus tools.
func DetectFocusTools() map[string]bool {
	tools := map[string]bool{}

	// Check command-line tools
	for _, tool := range []string{"wlrctl", "kdotool", "xdotool", "wmctrl", "gdbus", "busctl"} {
		_, err := exec.LookPath(tool)
		tools[tool] = err == nil
	}

	// Check GNOME activate-window-by-title extension
	cmd := exec.Command("busctl", "--user", "introspect",
		"org.gnome.Shell",
		"/de/lucaswerkmeister/ActivateWindowByTitle",
	)
	output, err := cmd.CombinedOutput()
	tools["activate-window-by-title"] = err == nil && strings.Contains(string(output), "activateBySubstring")

	return tools
}
