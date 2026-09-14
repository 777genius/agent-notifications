package uapinstaller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/providers"
)

// Config is copied by New. Later mutation of the caller's value is ignored.
type Config struct {
	// StateRoot is the owned UAP namespace. It is required and must be an
	// absolute clean path. New does not create it.
	StateRoot                                                                 string
	StateFile, LockFile, OperationsDir, PluginDataBase, ManagedRoot, TempRoot string
	HelperExecutable, HelperVersion                                           string
	Runner                                                                    providers.CommandRunner
	// ServerName selects the declared MCP server whose args the host may replace.
	ServerName string
	// ProjectArgs replaces args of ServerName. It is host-owned and must be
	// deterministic. A missing declared server or a callback error fails staging.
	ProjectArgs        func(BindingFacts) ([]string, error)
	OnCommittedBinding func(context.Context, BindingFacts) error
	// Assess is optional and digest-bound. Nil keeps the trusted-local MVP:
	// constructor does not start a download scanner, and missing evaluator is
	// not a hidden allow. When set, block and unavailable never become allow.
	Assess func(context.Context, string, string) (Assessment, error)
	// Progress reports coarse phases. It must not return an error, prompt, or
	// start a nested installer.
	Progress func(ProgressEvent)
	// ClientExecutables are optional explicit client paths Discover Lstats
	// without executing. Empty entries fall back to PATH presence of the
	// well-known binary name. New copies the map.
	ClientExecutables map[string]string
}

func (c Config) resolved() (Config, error) {
	out := c
	if !validRoot(out.StateRoot) {
		return Config{}, fmt.Errorf("%w: StateRoot must be an explicit absolute clean path", ErrInvalidConfig)
	}
	if out.StateFile == "" {
		out.StateFile = filepath.Join(out.StateRoot, "state-v2.json")
	}
	if out.LockFile == "" {
		out.LockFile = filepath.Join(out.StateRoot, "mutation.lock")
	}
	if out.OperationsDir == "" {
		out.OperationsDir = filepath.Join(out.StateRoot, "operations")
	}
	if out.PluginDataBase == "" {
		out.PluginDataBase = filepath.Join(out.StateRoot, "plugin-data")
	}
	if out.ManagedRoot == "" {
		out.ManagedRoot = filepath.Join(out.StateRoot, "managed")
	}
	if out.TempRoot == "" {
		out.TempRoot = filepath.Join(out.StateRoot, "tmp")
	}
	if out.HelperVersion == "" {
		out.HelperVersion = "uap-installer-helper-v1"
	}
	for _, p := range []string{out.StateFile, out.LockFile, out.OperationsDir, out.PluginDataBase, out.ManagedRoot, out.TempRoot} {
		if !validRoot(p) {
			return Config{}, fmt.Errorf("%w: derived or explicit path must be absolute and clean", ErrInvalidConfig)
		}
	}
	if out.HelperExecutable != "" && !validRoot(out.HelperExecutable) {
		return Config{}, fmt.Errorf("%w: HelperExecutable must be an explicit absolute clean path", ErrInvalidConfig)
	}
	if out.ClientExecutables != nil {
		cloned := make(map[string]string, len(out.ClientExecutables))
		for id, p := range out.ClientExecutables {
			if p != "" && !validRoot(p) {
				return Config{}, fmt.Errorf("%w: ClientExecutables[%s] must be an explicit absolute clean path", ErrInvalidConfig, id)
			}
			cloned[id] = p
		}
		out.ClientExecutables = cloned
	}
	return out, nil
}

func validRoot(p string) bool {
	if p == "" || !utf8.ValidString(p) || len(p) > 4096 || !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.ContainsRune(p, 0) {
		return false
	}
	volume := filepath.VolumeName(p)
	return !strings.HasPrefix(volume, `\\`) && !strings.HasPrefix(volume, `//`)
}

// overlappingRoots reports whether two owned paths are the same location or
// nested, including case and symlink aliases on case-insensitive volumes.
func overlappingRoots(a, b string) bool {
	left, right := canonicalRoot(a), canonicalRoot(b)
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	return containedRoot(left, right) || containedRoot(right, left)
}

func containedRoot(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func canonicalRoot(p string) string {
	p = filepath.Clean(p)
	if eval, err := filepath.EvalSymlinks(p); err == nil {
		p = eval
	}
	switch runtime.GOOS {
	case "windows", "darwin":
		return strings.ToLower(p)
	default:
		return p
	}
}
