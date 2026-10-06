//go:build linux

package linuxcallback

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// This catalog must be generated from the authenticated official package. An
// empty catalog intentionally keeps production unavailable before acquisition.
//
//go:embed vendor-chatgpt-26.930.51102-amd64.json
var catalog []byte

const packageSHA256 = "637c3c94bc50f8ee33a15e2e28ec7f92a787f0943e700efe111bc0bf0d4813b4"

type VendorEntry struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	SHA256 string `json:"sha256,omitempty"`
	Target string `json:"target,omitempty"`
}
type VendorManifest struct {
	Version       int           `json:"version"`
	PackageSHA256 string        `json:"packageSHA256"`
	Root          string        `json:"root"`
	Launcher      string        `json:"launcher"`
	Executable    string        `json:"executable"`
	Entries       []VendorEntry `json:"entries"`
}

func vendor() (VendorManifest, error) {
	var m VendorManifest
	if len(catalog) > 8<<20 || json.Unmarshal(catalog, &m) != nil || m.Version != 1 || m.PackageSHA256 != packageSHA256 || m.Root != "/usr/lib/chatgpt" || m.Launcher != "/usr/lib/chatgpt/codex-launcher" || m.Executable != "/usr/lib/chatgpt/ChatGPT" || len(m.Entries) == 0 || len(m.Entries) > 20000 {
		return m, ErrUnavailable
	}
	return m, nil
}
func SelectedSnapshot(root string) (Snapshot, error) {
	m, e := vendor()
	if e != nil || root != m.Root {
		return Snapshot{}, ErrUnavailable
	}
	return Snapshot{Version: 1, Launcher: m.Launcher, Executable: m.Executable, LauncherSHA256: "8f983245c6c07070e2cdc480be50ec239e0f18ee36069126649d0692595c86ad", ExecutableSHA256: "207c4fbff7e2fcc1b0789448351ac6eed206206d94c5a0835e5f07c7cd73d6e3", ReleaseSHA256: m.PackageSHA256, ManifestSHA256: Digest(catalog)}, nil
}
func vendorAncestors(path string) error {
	for p := filepath.Dir(path); ; p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
			return ErrUnavailable
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 {
			return ErrUnavailable
		}
		if p == "/" {
			break
		}
	}
	return nil
}
func supportedVendor(s Snapshot) bool {
	m, e := vendor()
	return e == nil && s.ReleaseSHA256 == m.PackageSHA256 && s.ManifestSHA256 == Digest(catalog) && s.Launcher == m.Launcher && s.Executable == m.Executable && s.LauncherSHA256 == "8f983245c6c07070e2cdc480be50ec239e0f18ee36069126649d0692595c86ad" && s.ExecutableSHA256 == "207c4fbff7e2fcc1b0789448351ac6eed206206d94c5a0835e5f07c7cd73d6e3"
}
func CheckSelected(s Snapshot) error { return CheckSelectedContext(context.Background(), s) }
func CheckSelectedContext(ctx context.Context, s Snapshot) error {
	if !s.Valid() || !supportedVendor(s) {
		return ErrUnavailable
	}
	m, e := vendor()
	if e != nil {
		return e
	}
	if e = verifyTree(ctx, m); e != nil {
		return e
	}
	// Catalog hashes must independently agree with provisioned release pins.
	pins := map[string]string{s.Launcher: s.LauncherSHA256, s.Executable: s.ExecutableSHA256}
	for path, want := range pins {
		found := false
		for _, entry := range m.Entries {
			if entry.Path == path && entry.Type == "file" && entry.SHA256 == want {
				found = true
				break
			}
		}
		if !found {
			return ErrUnavailable
		}
	}
	return nil
}

// verifyTree checks resources as well as the native executable, and refuses
// undeclared children. These path checks are conservative and non-atomic; they
// do not freeze third-party updates or claim a full client sandbox guarantee.
func verifyTree(ctx context.Context, m VendorManifest) error {
	if !canonical(m.Root) || len(m.Entries) == 0 || len(m.Entries) > 20000 {
		return ErrUnavailable
	}
	expected := make(map[string]VendorEntry, len(m.Entries))
	var total int64
	for _, entry := range m.Entries {
		path := entry.Path
		if !canonical(path) || (path != m.Root && !strings.HasPrefix(path, m.Root+"/") && path != "/usr/bin/chatgpt") {
			return ErrUnavailable
		}
		if _, exists := expected[path]; exists {
			return ErrUnavailable
		}
		expected[path] = entry
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e := vendorAncestors(path); e != nil {
			return e
		}
		info, e := os.Lstat(path)
		if e != nil {
			return e
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 {
			return ErrUnavailable
		}
		switch entry.Type {
		case "directory":
			if !info.IsDir() || info.Mode().Perm()&0022 != 0 {
				return ErrUnavailable
			}
		case "symlink":
			if info.Mode()&os.ModeSymlink == 0 {
				return ErrUnavailable
			}
			target, e := os.Readlink(path)
			if e != nil || target != entry.Target || target == "" {
				return ErrUnavailable
			}
			resolved := target
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(filepath.Dir(path), target)
			}
			resolved = filepath.Clean(resolved)
			if !strings.HasPrefix(resolved, m.Root+"/") {
				return ErrUnavailable
			}
		case "file":
			if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || !validKey(entry.SHA256) || info.Size() < 0 || info.Size() > 512<<20 {
				return ErrUnavailable
			}
			total += info.Size()
			if total > 4<<30 {
				return ErrUnavailable
			}
			f, e := os.Open(path)
			if e != nil {
				return e
			}
			// Failed reads close this read-only handle best-effort; success checks Close below.
			opened, e := f.Stat()
			if e != nil || !os.SameFile(info, opened) {
				_ = f.Close()
				return ErrUnavailable
			}
			h := sha256.New()
			buffer := make([]byte, 64<<10)
			var readBytes int64
			for {
				if ctx.Err() != nil {
					_ = f.Close()
					return ctx.Err()
				}
				n, re := f.Read(buffer)
				if n > 0 {
					readBytes += int64(n)
					if readBytes > 512<<20 || readBytes > info.Size() {
						_ = f.Close()
						return ErrUnavailable
					}
					_, _ = h.Write(buffer[:n])
				}
				if re == io.EOF {
					break
				}
				if re != nil {
					_ = f.Close()
					return re
				}
			}
			after, e := f.Stat()
			closeErr := f.Close()
			if e != nil || closeErr != nil || !os.SameFile(opened, after) || after.Size() != info.Size() || hex.EncodeToString(h.Sum(nil)) != entry.SHA256 {
				return ErrUnavailable
			}
		default:
			return ErrUnavailable
		}
	}
	if _, ok := expected[m.Root]; !ok {
		return ErrUnavailable
	}
	return filepath.WalkDir(m.Root, func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, ok := expected[path]; !ok {
			return ErrUnavailable
		}
		return nil
	})
}
