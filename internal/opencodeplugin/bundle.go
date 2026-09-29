// Package opencodeplugin embeds the exact, self-contained OpenCode product plugin.
package opencodeplugin

import (
	_ "embed"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
)

//go:embed dist/agent-notifications.js
var bundle string

const executableToken = `"__AGENT_NOTIFICATIONS_EXECUTABLE__"`
const controlRootToken = `"__AGENT_NOTIFICATIONS_CONTROL_ROOT__"`

// Render binds the owned executable without evaluating JS or copying SDK source.
func Render(executable, controlRoot string) ([]byte, error) {
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable || strings.ContainsRune(executable, 0) ||
		!filepath.IsAbs(controlRoot) || filepath.Clean(controlRoot) != controlRoot || strings.ContainsRune(controlRoot, 0) ||
		strings.Count(bundle, executableToken) != 1 || strings.Count(bundle, controlRootToken) != 1 {
		return nil, errors.New("invalid_opencode_bundle")
	}
	bound := strings.NewReplacer(executableToken, strconv.Quote(executable), controlRootToken, strconv.Quote(controlRoot))
	return []byte(bound.Replace(bundle)), nil
}
