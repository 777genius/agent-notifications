package portableasset

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	// DefaultReleaseDownloadRoot is the GitHub release download directory.
	// Exact identity is v{version}/AssetName, not "latest".
	DefaultReleaseDownloadRoot = "https://github.com/777genius/agent-notifications/releases/download"
	maxChecksumsSize           = 64 << 10
)

// Getter loads one URL. Tests inject a local server; production uses HTTPGet.
type Getter func(ctx context.Context, rawURL string) ([]byte, error)

// FetchRequest names one accepted release revision. DownloadRoot is the
// directory that contains v{version}/ assets, without a trailing slash.
type FetchRequest struct {
	Version, GOOS, GOARCH, DestParent, DownloadRoot string
	Get                                             Getter
}

// Fetch downloads checksums.txt and the platform zip for Version, verifies the
// archive digest, and unpacks into DestParent/root. It does not substitute
// git templates or the running master's tree when the asset is missing.
func Fetch(ctx context.Context, req FetchRequest) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("context required")
	}
	version := strings.TrimPrefix(strings.TrimSpace(req.Version), "v")
	if version == "" || strings.ContainsAny(version, "/\\") {
		return "", fmt.Errorf("release version is required")
	}
	if req.GOOS == "" || req.GOARCH == "" {
		return "", fmt.Errorf("GOOS/GOARCH are required")
	}
	if !explicitAbs(req.DestParent) {
		return "", fmt.Errorf("destination parent must be an explicit absolute path")
	}
	root := strings.TrimRight(req.DownloadRoot, "/")
	if root == "" {
		root = DefaultReleaseDownloadRoot
	}
	get := req.Get
	if get == nil {
		get = HTTPGet
	}
	asset := AssetName(req.GOOS, req.GOARCH)
	base := root + "/v" + version
	list, err := get(ctx, base+"/checksums.txt")
	if err != nil {
		return "", err
	}
	expected, err := checksumFor(list, asset)
	if err != nil {
		return "", err
	}
	data, err := get(ctx, base+"/"+asset)
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxArchiveSize {
		return "", fmt.Errorf("%w: archive exceeds %d bytes", ErrUnsafeArchive, maxArchiveSize)
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if !strings.EqualFold(digest, expected) {
		return "", fmt.Errorf("%w: got %s", ErrChecksumMismatch, digest)
	}
	if err := os.MkdirAll(req.DestParent, 0700); err != nil {
		return "", err
	}
	archive := filepath.Join(req.DestParent, asset)
	if err := os.WriteFile(archive, data, 0600); err != nil {
		return "", err
	}
	return OpenArchive(archive, req.DestParent, expected)
}

// HTTPGet fetches an http(s) URL with the archive size bound.
func HTTPGet(ctx context.Context, rawURL string) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context required")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported download scheme %q", u.Scheme)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL == nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
				return fmt.Errorf("redirect to unsupported scheme")
			}
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", u.Redacted(), resp.StatusCode)
	}
	limit := int64(maxArchiveSize)
	if resp.ContentLength > 0 && resp.ContentLength <= maxChecksumsSize && strings.HasSuffix(u.Path, "/checksums.txt") {
		limit = maxChecksumsSize
	}
	if strings.HasSuffix(u.Path, "/checksums.txt") {
		limit = maxChecksumsSize
	}
	if resp.ContentLength > limit {
		return nil, fmt.Errorf("%w: body exceeds %d bytes", ErrUnsafeArchive, limit)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: body exceeds %d bytes", ErrUnsafeArchive, limit)
	}
	return data, nil
}

func checksumFor(list []byte, name string) (string, error) {
	if int64(len(list)) > maxChecksumsSize {
		return "", fmt.Errorf("%w: checksums.txt exceeds %d bytes", ErrUnsafeArchive, maxChecksumsSize)
	}
	want := filepath.Base(name)
	var found string
	for _, line := range bytes.Split(list, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		digest, file, ok := parseChecksumLine(line)
		if !ok {
			continue
		}
		if filepath.Base(file) != want || !hex64(digest) {
			continue
		}
		if found != "" && !strings.EqualFold(found, digest) {
			return "", fmt.Errorf("checksums.txt has conflicting entries for %s", want)
		}
		found = digest
	}
	if found == "" {
		return "", fmt.Errorf("checksums.txt has no entry for %s", want)
	}
	return found, nil
}

func parseChecksumLine(line []byte) (digest, name string, ok bool) {
	if len(line) < 66 {
		return "", "", false
	}
	digest = strings.ToLower(string(line[:64]))
	if !hex64(digest) {
		return "", "", false
	}
	rest := bytes.TrimSpace(line[64:])
	if len(rest) == 0 {
		return "", "", false
	}
	if rest[0] == '*' {
		rest = rest[1:]
	}
	name = string(bytes.TrimSpace(rest))
	if name == "" || strings.Contains(name, "\x00") {
		return "", "", false
	}
	return digest, name, true
}

func hex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
