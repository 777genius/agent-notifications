package geminiinstall

import (
	"errors"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"path/filepath"
)

// ResolveConfigRoot observes the same single profile authority as Apply.
// GeminiHome is the parent of .gemini; ConfigRoot takes precedence.
// It does not read settings, create directories, or launch Gemini.
func ResolveConfigRoot(r Request) (string, error) {
	root := r.ConfigRoot
	if root == "" {
		home := r.GeminiHome
		if home == "" {
			home = r.HomeDir
		}
		if !filepath.IsAbs(home) {
			return "", errors.New("absolute Gemini home or --config-root required")
		}
		root = filepath.Join(home, ".gemini")
	}
	if !filepath.IsAbs(root) {
		return "", errors.New("absolute Gemini config root required")
	}
	root, err := installruntime.CanonicalPath(root)
	if err != nil {
		return "", err
	}
	return root, nil
}
