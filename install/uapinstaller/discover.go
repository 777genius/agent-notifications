package uapinstaller

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Discover returns metadata of this beta's supported providers, including
// executable presence and current bindings. It does not create state, run a
// helper, or execute a found file.
func (e *Engine) Discover() []ClientMetadata {
	byClient := map[string][]InspectedBinding{}
	if view, err := e.observe(); err == nil || len(view.Installations) > 0 {
		for _, installation := range view.Installations {
			for _, binding := range installation.Bindings {
				byClient[binding.ClientID] = append(byClient[binding.ClientID], binding)
			}
		}
	}
	return []ClientMetadata{
		e.clientMetadata("claude", byClient["claude"]),
		e.clientMetadata("codex", byClient["codex"]),
	}
}

func (e *Engine) clientMetadata(id string, bindings []InspectedBinding) ClientMetadata {
	meta := ClientMetadata{ClientID: id, Scopes: []string{"user"}, Bindings: append([]InspectedBinding(nil), bindings...)}
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
