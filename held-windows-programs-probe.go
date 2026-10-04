//go:build windows

// TEST-only same-environment Programs lookup; no installation or permission API.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

type lookup struct {
	Code   uint32 `json:"windowsCode"`
	Inside bool   `json:"insideOwnedRoot"`
	Hash   string `json:"pathSHA256"`
	Safe   bool   `json:"existingAncestorsNonReparse"`
}
type record struct {
	Schema          int    `json:"schema"`
	Status          string `json:"status"`
	Reason          string `json:"reason"`
	Before          lookup `json:"before"`
	Resolved        lookup `json:"resolved"`
	After           lookup `json:"after"`
	ResolutionFlags uint32 `json:"resolutionFlags"`
	Prepared        bool   `json:"resolvedOwnedDirectoryPrepared"`
	AfterAttempted  bool   `json:"afterAttempted"`
	Qualified       bool   `json:"qualificationGranted"`
}

func inside(root, path string) bool {
	// Match the existing product Windows managed-path component boundary.
	volume := filepath.VolumeName(path)
	if len(volume) != 2 || volume[1] != ':' {
		return false
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, volume+`\`), `\`) {
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		reserved := base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CONIN$" || base == "CONOUT$"
		if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
			reserved = true
		}
		if part == "" || strings.ContainsAny(part, ":/<>\"|?*") || strings.TrimRight(part, " .") != part || reserved {
			return false
		}
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}
func ordinary(path string) bool {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	attr, err := windows.GetFileAttributes(p)
	return err == nil && attr&windows.FILE_ATTRIBUTE_DIRECTORY != 0 && attr&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0
}
func query(root string, flags uint32, allowMissing bool) (lookup, string) {
	path, err := windows.KnownFolderPath(windows.FOLDERID_Programs, flags)
	if err != nil {
		var code syscall.Errno
		if errors.As(err, &code) {
			return lookup{Code: uint32(code)}, ""
		}
		return lookup{Code: 0xffffffff}, ""
	}
	h := sha256.Sum256([]byte(filepath.Clean(path)))
	contained := filepath.IsAbs(path) && filepath.Clean(path) == path && inside(root, path)
	return lookup{Inside: contained, Safe: contained && walk(root, path, false, allowMissing), Hash: hex.EncodeToString(h[:])}, path
}

func walk(root, target string, create, allowMissing bool) bool {
	if !inside(root, target) || !ordinary(root) {
		return false
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	p := root
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		p = filepath.Join(p, part)
		if !ordinary(p) {
			if !create {
				_, err := os.Lstat(p)
				return allowMissing && os.IsNotExist(err)
			}
			if err := os.Mkdir(p, 0700); err != nil {
				return false
			}
			if !ordinary(p) {
				return false
			}
		}
	}
	return true
}
func main() {
	r := record{Schema: 2, Status: "rejected", Reason: "owned_environment_required"}
	exit := 1
	defer func() { _ = json.NewEncoder(os.Stdout).Encode(r); os.Exit(exit) }()
	if len(os.Args) != 2 {
		return
	}
	root := filepath.Clean(os.Args[1])
	home, appdata := filepath.Join(root, "home"), filepath.Join(root, "appdata")
	current, err := os.UserHomeDir()
	if !filepath.IsAbs(root) || root != os.Args[1] || !ordinary(root) || err != nil ||
		filepath.Clean(current) != home || os.Getenv("HOME") != home || os.Getenv("USERPROFILE") != home || os.Getenv("APPDATA") != appdata {
		return
	}
	var target string
	r.Before, target = query(root, 0, false)
	r.Resolved = r.Before
	r.Reason = "before_lookup_rejected"
	if r.Before.Code != 0 {
		if r.Before.Code != 3 && r.Before.Code != 0x80070003 {
			return
		}
		// Read-only diagnostic: do not use CREATE or DEFAULT_PATH, or install flags.
		r.ResolutionFlags = windows.KF_FLAG_DONT_VERIFY
		r.Resolved, target = query(root, windows.KF_FLAG_DONT_VERIFY, true)
	}
	r.Reason = "resolved_lookup_rejected"
	if r.Resolved.Code != 0 || !r.Resolved.Inside || !r.Resolved.Safe {
		return
	}
	r.Reason = "owned_directory_preparation_rejected"
	if !walk(root, target, true, false) {
		return
	}
	r.Prepared = true
	r.AfterAttempted = true
	r.After, _ = query(root, 0, false)
	r.Reason = "after_lookup_rejected"
	if r.After.Code != 0 || !r.After.Inside || !r.After.Safe || r.Resolved.Hash != r.After.Hash {
		return
	}

	r.Status, r.Reason, exit = "observed_only", "", 0
}
