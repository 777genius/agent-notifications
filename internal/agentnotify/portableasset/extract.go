package portableasset

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrExistingRoot      = errors.New("destination already exists")
	ErrUnexpectedArchive = errors.New("unexpected archive entry")
	ErrUnsafeArchive     = errors.New("unsafe archive entry")
	ErrChecksumMismatch  = errors.New("archive checksum mismatch")
	ErrInvalidLayout     = errors.New("portable package layout is invalid")
)

const extractedRootName = "root"

// Extract unpacks a ZIP archive into destRoot, which must not already exist.
func Extract(archivePath, destRoot string) error {
	data, err := readArchive(archivePath)
	if err != nil {
		return err
	}
	return extractBytes(data, destRoot)
}

// OpenArchive verifies the archive SHA-256, unpacks into a new owned directory
// under destParent, and checks the standard package layout.
func OpenArchive(archivePath, destParent, expectedSHA256 string) (string, error) {
	if !explicitAbs(archivePath) || !explicitAbs(destParent) {
		return "", fmt.Errorf("archive and destination parent must be explicit absolute paths")
	}
	data, err := readArchive(archivePath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if expectedSHA256 != "" && !strings.EqualFold(digest, expectedSHA256) {
		return "", fmt.Errorf("%w: got %s", ErrChecksumMismatch, digest)
	}
	if err := os.MkdirAll(destParent, 0700); err != nil {
		return "", err
	}
	destRoot := filepath.Join(destParent, extractedRootName)
	if err := extractBytes(data, destRoot); err != nil {
		return "", err
	}
	if err := VerifyLayout(destRoot); err != nil {
		_ = os.RemoveAll(destRoot)
		return "", err
	}
	return destRoot, nil
}

func readArchive(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: archive is not a regular file", ErrUnsafeArchive)
	}
	if info.Size() > maxArchiveSize {
		return nil, fmt.Errorf("%w: archive exceeds %d bytes", ErrUnsafeArchive, maxArchiveSize)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxArchiveSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxArchiveSize {
		return nil, fmt.Errorf("%w: archive exceeds %d bytes", ErrUnsafeArchive, maxArchiveSize)
	}
	return data, nil
}

func extractBytes(data []byte, destRoot string) error {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	return extractArchive(r, destRoot)
}

func extractArchive(r *zip.Reader, destRoot string) error {
	destRoot, err := filepath.Abs(destRoot)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(destRoot); err == nil {
		return fmt.Errorf("%w: %s", ErrExistingRoot, destRoot)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(destRoot, 0700); err != nil {
		return err
	}
	var uncompressed int64
	seen := map[string]bool{}
	for _, f := range r.File {
		n, err := extractEntry(destRoot, f, seen)
		if err != nil {
			_ = os.RemoveAll(destRoot)
			return err
		}
		uncompressed += n
		if uncompressed > maxArchiveSize {
			_ = os.RemoveAll(destRoot)
			return fmt.Errorf("%w: uncompressed size exceeds %d bytes", ErrUnsafeArchive, maxArchiveSize)
		}
	}
	return nil
}

func extractEntry(destRoot string, f *zip.File, seen map[string]bool) (int64, error) {
	name := filepath.ToSlash(f.Name)
	name = strings.TrimPrefix(name, "./")
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "..") || !filepath.IsLocal(filepath.FromSlash(name)) {
		return 0, fmt.Errorf("%w: %s", ErrUnsafeArchive, f.Name)
	}
	if f.Mode()&os.ModeSymlink != 0 || f.Mode()&(os.ModeNamedPipe|os.ModeSocket|os.ModeDevice) != 0 {
		return 0, fmt.Errorf("%w: %s", ErrUnsafeArchive, f.Name)
	}
	if seen[name] {
		return 0, fmt.Errorf("%w: duplicate %s", ErrUnexpectedArchive, f.Name)
	}
	seen[name] = true
	target := filepath.Join(destRoot, filepath.FromSlash(name))
	rel, err := filepath.Rel(destRoot, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || !filepath.IsLocal(rel) {
		return 0, fmt.Errorf("%w: %s", ErrUnsafeArchive, f.Name)
	}
	if f.FileInfo().IsDir() || strings.HasSuffix(name, "/") {
		return 0, os.MkdirAll(target, 0700)
	}
	if !allowedArchivePath(name) {
		return 0, fmt.Errorf("%w: %s", ErrUnexpectedArchive, f.Name)
	}
	if f.UncompressedSize64 > uint64(maxArchiveFile) {
		return 0, fmt.Errorf("%w: %s exceeds %d bytes", ErrUnsafeArchive, f.Name, maxArchiveFile)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return 0, err
	}
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer func() { _ = rc.Close() }()
	mode := f.Mode().Perm()
	if mode == 0 {
		mode = 0600
	}
	if strings.HasPrefix(name, "bin/") {
		mode |= 0755
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return 0, err
	}
	written, err := io.CopyN(out, rc, maxArchiveFile+1)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return written, err
	}
	if written > maxArchiveFile {
		return written, fmt.Errorf("%w: %s exceeds %d bytes", ErrUnsafeArchive, f.Name, maxArchiveFile)
	}
	return written, nil
}

func allowedArchivePath(name string) bool {
	switch name {
	case "plugin.json", "mcp.json", "skills/agent-notify/SKILL.md":
		return true
	}
	if strings.HasPrefix(name, "bin/") && strings.Count(name, "/") == 1 {
		base := filepath.Base(name)
		return base == "claude-notifications" || base == "claude-notifications.exe"
	}
	return false
}

// VerifyLayout checks the self-contained standard package. It does not compare
// skill bytes with the current embed: historical repair uses the asset skill.
func VerifyLayout(root string) error {
	pluginBody, err := os.ReadFile(filepath.Join(root, "plugin.json"))
	if err != nil {
		return fmt.Errorf("%w: plugin.json: %v", ErrInvalidLayout, err)
	}
	var plugin struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(pluginBody, &plugin); err != nil {
		return fmt.Errorf("%w: plugin.json: %v", ErrInvalidLayout, err)
	}
	if plugin.Name != pluginName || plugin.Version == "" || strings.Contains(plugin.Version, "/") {
		return fmt.Errorf("%w: plugin.json name/version", ErrInvalidLayout)
	}
	mcpBody, err := os.ReadFile(filepath.Join(root, "mcp.json"))
	if err != nil {
		return fmt.Errorf("%w: mcp.json: %v", ErrInvalidLayout, err)
	}
	var mcp struct {
		Servers map[string]struct {
			Command string `json:"command"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(mcpBody, &mcp); err != nil {
		return fmt.Errorf("%w: mcp.json: %v", ErrInvalidLayout, err)
	}
	server, ok := mcp.Servers[pluginName]
	if !ok || (server.Command != "./bin/"+binaryName("linux") && server.Command != "./bin/"+binaryName("windows")) {
		return fmt.Errorf("%w: mcp command", ErrInvalidLayout)
	}
	skill := filepath.Join(root, filepath.FromSlash("skills/agent-notify/SKILL.md"))
	info, err := os.Lstat(skill)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("%w: skill", ErrInvalidLayout)
	}
	binRel := strings.TrimPrefix(server.Command, "./")
	binPath := filepath.Join(root, filepath.FromSlash(binRel))
	binInfo, err := os.Lstat(binPath)
	if err != nil || !binInfo.Mode().IsRegular() || binInfo.Size() == 0 {
		return fmt.Errorf("%w: package executable", ErrInvalidLayout)
	}
	return nil
}
