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

// Render binds the owned executable without evaluating JS or copying SDK source.
func Render(executable string) ([]byte, error) {
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable || strings.ContainsRune(executable, 0) || strings.Count(bundle, executableToken) != 1 {
		return nil, errors.New("invalid_opencode_bundle")
	}
	return []byte(strings.Replace(bundle, executableToken, strconv.Quote(executable), 1)), nil
}
