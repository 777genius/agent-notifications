package uapinstaller

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Discover returns metadata of this beta's supported providers, including
// whether a client executable is present. It does not create state, run a
// helper, or execute a found file.
func (e *Engine) Discover() []ClientMetadata {
	return []ClientMetadata{
		e.clientMetadata("claude"),
		e.clientMetadata("codex"),
	}
}

func (e *Engine) clientMetadata(id string) ClientMetadata {
	meta := ClientMetadata{ClientID: id, Scopes: []string{"user"}}
	explicit := ""
	if e.cfg.ClientExecutables != nil {
		explicit = e.cfg.ClientExecutables[id]
	}
	path, ok := executablePresent(explicit, id)
	if !ok {
		return meta
	}
	meta.ExecutablePresent = true
	meta.ExecutablePath = path
	return meta
}

func executablePresent(explicit, name string) (string, bool) {
	if explicit != "" {
		return explicit, regularFile(explicit)
	}
	path, err := exec.LookPath(name)
	if err != nil || path == "" {
		return "", false
	}
	if !filepath.IsAbs(path) {
		abs, absErr := filepath.Abs(path)
		if absErr != nil {
			return "", false
		}
		path = abs
	}
	if !regularFile(path) {
		return "", false
	}
	return path, true
}

func regularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}
