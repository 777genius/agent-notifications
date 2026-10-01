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

const originToken = `"__AGENT_NOTIFICATIONS_ORIGIN__"`

type RegistrationRenderer struct{}

func (RegistrationRenderer) RenderRegistration(executable, controlRoot, origin string) ([]byte, error) {
	return renderRegistration(bundle, executable, controlRoot, origin)
}

// This pure boundary operates on the actual supplied asset; no two-token asset
// can become origin-bound, and no replacement asset is synthesized.
func renderRegistration(asset, executable, controlRoot, origin string) ([]byte, error) {
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable || strings.ContainsRune(executable, 0) ||
		!filepath.IsAbs(controlRoot) || filepath.Clean(controlRoot) != controlRoot || strings.ContainsRune(controlRoot, 0) || len(origin) != 64 {
		return nil, errors.New("invalid_opencode_bundle")
	}
	for _, c := range origin {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return nil, errors.New("invalid_opencode_bundle")
		}
	}
	for _, token := range []string{executableToken, controlRootToken, originToken} {
		if strings.Count(asset, token) != 1 {
			return nil, errors.New("invalid_opencode_bundle")
		}
	}
	return []byte(strings.NewReplacer(executableToken, strconv.Quote(executable), controlRootToken, strconv.Quote(controlRoot), originToken, strconv.Quote(origin)).Replace(asset)), nil
}
