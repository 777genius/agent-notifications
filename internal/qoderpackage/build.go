// Package qoderpackage builds the separate native Qoder Stop-hook bundle.
// It does not alter the portable MCP package or managed runtime ledger.
package qoderpackage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
	"github.com/777genius/agent-notifications/internal/thirdpartynotices"
)

type BuildRequest struct{ Version, GOOS, GOARCH, Executable, OutputRoot, Archive string }
type Package struct{ Root, Archive, ArchiveSHA256, BinaryName string }

// AssetName identifies the separate native hook package, not the MCP package.
func AssetName(goos, goarch string) string {
	return fmt.Sprintf("agent-notify-qoder-%s-%s.zip", goos, goarch)
}

// Build copies a final release executable without rebuilding it. All output
// paths must be new: rerunning cannot overwrite a user's installed package.
func Build(req BuildRequest) (Package, error) {
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`).MatchString(req.Version) {
		return Package{}, fmt.Errorf("qoder package requires a semver version without v prefix")
	}
	if req.GOOS != "darwin" && req.GOOS != "linux" && req.GOOS != "windows" {
		return Package{}, fmt.Errorf("unsupported GOOS")
	}
	if req.GOARCH != "amd64" && req.GOARCH != "arm64" {
		return Package{}, fmt.Errorf("unsupported GOARCH")
	}
	if !filepath.IsAbs(req.Executable) || !filepath.IsAbs(req.OutputRoot) || req.Archive != "" && !filepath.IsAbs(req.Archive) {
		return Package{}, fmt.Errorf("package paths must be absolute")
	}
	binary, err := os.ReadFile(req.Executable)
	if err != nil {
		return Package{}, err
	}
	info, err := os.Lstat(req.Executable)
	if err != nil {
		return Package{}, err
	}
	if !info.Mode().IsRegular() || len(binary) == 0 {
		return Package{}, fmt.Errorf("executable must be a nonempty regular file")
	}
	if _, err := os.Lstat(req.OutputRoot); !os.IsNotExist(err) {
		return Package{}, fmt.Errorf("refusing existing or inaccessible output root")
	}
	name := "claude-notifications"
	if req.GOOS == "windows" {
		name += ".exe"
	}
	manifest := map[string]string{"name": "agent-notify-qoder", "version": req.Version, "description": "Completion notifications for Qoder"}
	standardManifest := map[string]string{"$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json", "name": manifest["name"], "version": manifest["version"], "description": manifest["description"]}
	hook := map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "${QODER_PLUGIN_ROOT}/bin/" + name, "args": []string{"handle-hook", "Stop", "--product", "qoder"}, "name": "agent-notify-qoder-stop", "timeout": 40}}}}}}
	if err := os.MkdirAll(filepath.Dir(req.OutputRoot), 0700); err != nil {
		return Package{}, err
	}
	if err := os.Mkdir(req.OutputRoot, 0700); err != nil {
		return Package{}, err
	}
	files := map[string][]byte{thirdpartynotices.Filename: []byte(thirdpartynotices.Text()), "README.md": []byte("# Agent Notify for Qoder\n\nNative Stop-hook completion notifications. No MCP observer is installed.\nSee the source docs/QODER.md for configuration and qualification scope.\n")}
	for path, value := range map[string]any{".qoder-plugin/plugin.json": manifest, "plugin.json": standardManifest, "hooks/hooks.json": hook} {
		body, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return Package{}, err
		}
		files[path] = append(body, '\n')
	}
	files["bin/"+name] = binary
	for rel, body := range files {
		path := filepath.Join(req.OutputRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return Package{}, err
		}
		mode := os.FileMode(0600)
		if rel == "bin/"+name {
			mode = 0755
		}
		if err := os.WriteFile(path, body, mode); err != nil {
			return Package{}, err
		}
	}
	result := Package{Root: req.OutputRoot, BinaryName: name}
	if req.Archive != "" {
		sum, err := portableasset.Zip(req.OutputRoot, req.Archive)
		if err != nil {
			return Package{}, err
		}
		result.Archive, result.ArchiveSHA256 = req.Archive, sum
	}
	return result, nil
}
