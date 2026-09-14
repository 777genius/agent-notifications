// Package portableasset builds and extracts the version-bound Agent Notify
// portable package. The zip is a Notifications release asset, not a UAP
// marketplace item. Skill bytes come from the canonical embed, not a hand copy.
package portableasset

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/777genius/agent-notifications/skills"
)

const (
	pluginName     = "agent-notify"
	maxArchiveFile = 80 << 20
	maxArchiveSize = 96 << 20
)

// AssetName is the stable release filename. It is not a source identity.
func AssetName(goos, goarch string) string {
	return fmt.Sprintf("agent-notify-portable-%s-%s.zip", goos, goarch)
}

// BuildRequest names one release's signed/final platform executable.
type BuildRequest struct {
	Version, GOOS, GOARCH, Executable, OutputRoot, Archive string
}

// Package is a self-contained local root. ArchiveSHA256 is of the zip bytes.
type Package struct {
	Root, Archive, ArchiveSHA256, BinaryName string
}

func binaryName(goos string) string {
	if goos == "windows" {
		return "claude-notifications.exe"
	}
	return "claude-notifications"
}

func mcpCommand(goos string) string {
	return "./bin/" + binaryName(goos)
}

// Build writes plugin.json, mcp.json, the canonical skill, and the release
// executable into OutputRoot. It optionally zips that root to Archive.
func Build(req BuildRequest) (Package, error) {
	if req.Version == "" || strings.Contains(req.Version, "/") {
		return Package{}, fmt.Errorf("portable package version is required")
	}
	if req.GOOS == "" || req.GOARCH == "" {
		return Package{}, fmt.Errorf("portable package GOOS/GOARCH are required")
	}
	if !explicitAbs(req.Executable) || !explicitAbs(req.OutputRoot) {
		return Package{}, fmt.Errorf("executable and output root must be explicit absolute paths")
	}
	if req.Archive != "" && !explicitAbs(req.Archive) {
		return Package{}, fmt.Errorf("archive path must be explicit")
	}
	info, err := os.Stat(req.Executable)
	if err != nil {
		return Package{}, err
	}
	if !info.Mode().IsRegular() {
		return Package{}, fmt.Errorf("executable must be a regular file")
	}
	if _, err := os.Lstat(req.OutputRoot); err == nil {
		return Package{}, fmt.Errorf("refusing to write over existing package root")
	}
	if err := os.MkdirAll(req.OutputRoot, 0700); err != nil {
		return Package{}, err
	}
	name := binaryName(req.GOOS)
	plugin := map[string]string{
		"$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
		"name":    pluginName,
		"version": req.Version,
	}
	pluginJSON, err := json.MarshalIndent(plugin, "", "  ")
	if err != nil {
		return Package{}, err
	}
	mcp := map[string]any{
		"$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
		"mcpServers": map[string]any{
			pluginName: map[string]any{
				"type":    "stdio",
				"command": mcpCommand(req.GOOS),
				"args":    []string{},
				"env":     map[string]string{},
			},
		},
	}
	mcpJSON, err := json.MarshalIndent(mcp, "", "  ")
	if err != nil {
		return Package{}, err
	}
	files := map[string][]byte{
		"plugin.json":                  append(pluginJSON, '\n'),
		"mcp.json":                     append(mcpJSON, '\n'),
		"skills/agent-notify/SKILL.md": skills.AgentNotify(),
	}
	for rel, data := range files {
		path := filepath.Join(req.OutputRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return Package{}, err
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			return Package{}, err
		}
	}
	binDir := filepath.Join(req.OutputRoot, "bin")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		return Package{}, err
	}
	destBin := filepath.Join(binDir, name)
	if err := copyRegular(req.Executable, destBin, 0755); err != nil {
		return Package{}, err
	}
	out := Package{Root: req.OutputRoot, BinaryName: name}
	if req.Archive != "" {
		sum, err := Zip(req.OutputRoot, req.Archive)
		if err != nil {
			return Package{}, err
		}
		out.Archive, out.ArchiveSHA256 = req.Archive, sum
	}
	return out, VerifyLayout(req.OutputRoot)
}

func copyRegular(src, dest string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source is not a regular file")
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// Zip archives root with no extra version directory. SHA-256 is of the zip file.
func Zip(root, dest string) (string, error) {
	if !explicitAbs(root) || !explicitAbs(dest) {
		return "", fmt.Errorf("zip paths must be explicit")
	}
	if _, err := os.Lstat(dest); err == nil {
		return "", fmt.Errorf("refusing to write over existing archive")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	out := io.MultiWriter(f, hash)
	zw := zip.NewWriter(out)
	err = filepath.WalkDir(root, func(p string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name := path.Clean(filepath.ToSlash(rel))
		if entry.IsDir() {
			_, err := zw.CreateHeader(&zip.FileHeader{Name: name + "/", Method: zip.Deflate})
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing non-regular package file %s", rel)
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = name
		header.Method = zip.Deflate
		if strings.HasPrefix(name, "bin/") {
			header.SetMode(info.Mode() | 0755)
		}
		w, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(w, in)
		_ = in.Close()
		return copyErr
	})
	closeErr := zw.Close()
	fileErr := f.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if fileErr != nil {
		return "", fileErr
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func explicitAbs(p string) bool {
	return p != "" && filepath.IsAbs(p) && filepath.Clean(p) == p
}
