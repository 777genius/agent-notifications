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
}
type record struct {
	Schema    int     `json:"schema"`
	Status    string  `json:"status"`
	Reason    string  `json:"reason"`
	Before    lookup  `json:"before"`
	After     lookup  `json:"after"`
	Missing   [2]bool `json:"candidatesAbsentBefore"`
	Prepared  bool    `json:"twoOwnedCandidatesPrepared"`
	Qualified bool    `json:"qualificationGranted"`
}

func inside(root, path string) bool {
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
func query(root string) lookup {
	path, err := windows.KnownFolderPath(windows.FOLDERID_Programs, 0)
	if err != nil {
		var code syscall.Errno
		if errors.As(err, &code) {
			return lookup{Code: uint32(code)}
		}
		return lookup{Code: 0xffffffff}
	}
	h := sha256.Sum256([]byte(filepath.Clean(path)))
	return lookup{Inside: filepath.IsAbs(path) && filepath.Clean(path) == path && walk(root, path, false), Hash: hex.EncodeToString(h[:])}
}
func walk(root, target string, create bool) bool {
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
				return false
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
	r := record{Schema: 1, Status: "rejected", Reason: "owned_environment_required"}
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
	candidates := [2]string{filepath.Join(appdata, "Microsoft", "Windows", "Start Menu", "Programs"), filepath.Join(home, "AppData", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs")}
	for i, p := range candidates {
		_, e := os.Lstat(p)
		r.Missing[i] = os.IsNotExist(e)
	}
	r.Before = query(root)
	r.Reason = "before_lookup_rejected"
	if r.Before.Code == 0 {
		if !r.Before.Inside {
			return
		}
	} else if (r.Before.Code != 3 && r.Before.Code != 0x80070003) || !r.Missing[0] || !r.Missing[1] {
		return
	}
	r.Reason = "owned_directory_preparation_rejected"
	for _, p := range candidates {
		if !walk(root, p, true) {
			return
		}
	}
	r.Prepared = true
	r.After = query(root)
	r.Reason = "after_lookup_rejected"
	if r.After.Code != 0 || !r.After.Inside || (r.Before.Code == 0 && r.Before.Hash != r.After.Hash) {
		return
	}
	r.Status, r.Reason, exit = "observed_only", "", 0
}
