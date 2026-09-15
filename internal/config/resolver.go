package config

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// FreshNeutral is the shared rollout build policy; bridge artifacts set false.
const FreshNeutral = true

const OverrideEnv = "AGENT_NOTIFICATIONS_CONFIG"

// EnvSnapshot contains only path-policy inputs; no product, cwd or asset root.
// Lstat and ReadDir are required injected read-only dependencies. Tests must not
// supply host filesystem functions for synthetic paths.
type EnvSnapshot struct {
	GOOS    string
	Vars    map[string]string
	Lstat   func(string) (fs.FileInfo, error)
	ReadDir func(string) ([]fs.DirEntry, error)
	// Canonicalize resolves existing ancestors while preserving the final entry.
	Canonicalize  func(string) (string, error)
	configRoot    string
	configRootErr error
	nativeRoot    bool
	// Native metadata adapter; nil for synthetic resolver environments.
	permissionDiagnostics func(string) []Diagnostic
}

// SnapshotEnv captures path-policy environment once per invocation.
func SnapshotEnv() EnvSnapshot {
	e := EnvSnapshot{GOOS: runtime.GOOS, Vars: map[string]string{}, Lstat: os.Lstat, ReadDir: os.ReadDir, Canonicalize: canonicalParent}
	for _, k := range []string{OverrideEnv, "HOME", "USERPROFILE", "APPDATA", "XDG_CONFIG_HOME"} {
		if v, ok := os.LookupEnv(k); ok {
			e.Vars[k] = v
		}
	}
	// Normalize relative XDG before calling Go's version-dependent adapter;
	// never mutate the process environment.
	e.nativeRoot = true
	e.permissionDiagnostics = nativePermissionDiagnostics
	if runtime.GOOS == "linux" && e.Vars["XDG_CONFIG_HOME"] != "" && !validAbsolute("linux", e.Vars["XDG_CONFIG_HOME"]) {
		e.configRoot = filepath.Join(e.Vars["HOME"], ".config")
	} else {
		e.configRoot, e.configRootErr = os.UserConfigDir()
	}
	return e
}

type Selection struct {
	Path        string       `json:"path"`
	Source      string       `json:"source"`
	Exists      bool         `json:"exists"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

// Resolve selects exactly one entry without reading contents or creating files.
// FreshNeutral selects the creation policy; existing L/N and explicit E use the
// same shared selection contract in the bridge and final builds.
func Resolve(e EnvSnapshot) (Selection, error) {
	if e.Lstat == nil || e.ReadDir == nil {
		return Selection{}, &Error{Code: ConfigInvalid}
	}
	if v, ok := e.Vars[OverrideEnv]; ok {
		if !validAbsolute(e.GOOS, v) {
			return Selection{}, &Error{Code: ConfigOverrideInvalid}
		}
		return candidate(e, cleanPath(e.GOOS, v), "explicit")
	}
	home := e.Vars["HOME"]
	if e.GOOS == "windows" {
		home = e.Vars["USERPROFILE"]
	}
	if !validAbsolute(e.GOOS, home) {
		return Selection{}, &Error{Code: ConfigHomeUnavailable}
	}
	legacy, err := candidate(e, joinPath(e.GOOS, home, ".claude", "claude-notifications-go", "config.json"), "legacy")
	if err != nil {
		return legacy, err
	}
	neutral, warnings, baseErr := neutralPath(e, home)
	if legacy.Exists {
		legacy.Diagnostics = append(legacy.Diagnostics, warnings...)
		if baseErr != nil {
			legacy.Diagnostics = append(legacy.Diagnostics, Diagnostic{Code: ConfigBaseUnavailable})
			return legacy, nil
		}
		n, nerr := candidate(e, neutral, "universal")
		if nerr != nil {
			var ce *Error
			if errors.As(nerr, &ce) {
				legacy.Diagnostics = append(legacy.Diagnostics, Diagnostic{Code: ce.Code, Path: neutral})
			}
		}
		if n.Exists {
			legacy.Diagnostics = append(legacy.Diagnostics, Diagnostic{Code: ConfigMultipleCandidates, Path: neutral})
		}
		return legacy, nil
	}
	if baseErr != nil {
		return Selection{}, baseErr
	}
	n, err := candidate(e, neutral, "universal")
	n.Diagnostics = append(n.Diagnostics, warnings...)
	if err == nil && !n.Exists && !FreshNeutral {
		return legacy, nil
	}
	return n, err
}
func candidate(e EnvSnapshot, p, source string) (Selection, error) {
	s := Selection{Path: p, Source: source}
	if e.Canonicalize != nil {
		physical, err := e.Canonicalize(p)
		if err != nil {
			return s, pathErrorAt(p, "resolve-canonicalize", err)
		}
		p = physical
		s.Path = p
	}
	info, err := e.Lstat(p)
	if err == nil {
		s.Exists = true
		if info.Mode()&fs.ModeSymlink != 0 {
			s.Diagnostics = append(s.Diagnostics, Diagnostic{Code: ConfigLinkedPath, Path: p})
		}
		if e.GOOS != "windows" && info.Mode().IsRegular() && info.Mode().Perm()&0044 != 0 {
			s.Diagnostics = append(s.Diagnostics, Diagnostic{Code: ConfigPublicReadable, Path: p})
		}
		if info.Mode().IsRegular() && e.permissionDiagnostics != nil {
			s.Diagnostics = append(s.Diagnostics, e.permissionDiagnostics(p)...)
		}
		// Existence is entry existence, including malformed documents and non-files.
		return s, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return s, pathErrorAt(p, "resolve-lstat", err)
	}
	dir, base := splitPath(e.GOOS, p)
	entries, err := e.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return s, pathErrorAt(p, "resolve-readdir", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		legacyTemp := base == "config.json" && strings.HasPrefix(name, "config-") && strings.HasSuffix(name, ".json.tmp")
		if name == base+".tmp" || legacyTemp || strings.HasPrefix(name, base+".backup-") || strings.HasPrefix(name, base+".tmp-") {
			return s, &Error{Code: ConfigRecoveryRequired, Path: p}
		}
		// Derive only the writer-owned suffix from the directory entry, then
		// ask the native filesystem about requested-basename+suffix. This covers
		// case and Unicode normalization aliases without inventing either.
		if suffix, ok := recoverySuffix(name); ok {
			alias := joinPath(e.GOOS, dir, base+suffix)
			if _, aliasErr := e.Lstat(alias); aliasErr == nil {
				return s, &Error{Code: ConfigRecoveryRequired, Path: p}
			} else if !errors.Is(aliasErr, fs.ErrNotExist) {
				return s, pathErrorAt(alias, "resolve-recovery-lstat", aliasErr)
			}
		}
	}
	return s, nil
}

func recoverySuffix(name string) (string, bool) {
	last := -1
	for _, marker := range []string{".backup-", ".tmp-", ".tmp"} {
		for i := 0; i+len(marker) <= len(name); i++ {
			if strings.EqualFold(name[i:i+len(marker)], marker) && (marker != ".tmp" || i+len(marker) == len(name)) {
				if i > last {
					last = i
				}
			}
		}
	}
	if last < 0 {
		return "", false
	}
	return name[last:], true
}
func pathError(p string, err error) error {
	return pathErrorAt(p, "path", err)
}

func pathErrorAt(p, stage string, err error) error {
	var ce *Error
	if errors.As(err, &ce) {
		if ce.causeStage != "" {
			stage = ce.causeStage
		}
		copy := sanitizedError(ce.Code, p, ce.Offset, stage, nil)
		copy.causeErrno = ce.causeErrno
		return copy
	}
	code := ConfigInvalid
	if errors.Is(err, fs.ErrPermission) {
		code = ConfigPermissionDenied
	}
	return sanitizedError(code, p, 0, stage, err)
}
func neutralPath(e EnvSnapshot, home string) (string, []Diagnostic, error) {
	var root string
	var warnings []Diagnostic
	switch e.GOOS {
	case "darwin":
		root = joinPath(e.GOOS, home, "Library", "Application Support")
	case "windows":
		root = e.Vars["APPDATA"]
		if !validAbsolute(e.GOOS, root) {
			return "", nil, &Error{Code: ConfigBaseUnavailable}
		}
	case "linux":
		root = e.Vars["XDG_CONFIG_HOME"]
		if root == "" || !validAbsolute(e.GOOS, root) {
			if root != "" {
				warnings = append(warnings, Diagnostic{Code: ConfigBaseUnavailable})
			}
			root = joinPath(e.GOOS, home, ".config")
		}
	default:
		return "", nil, &Error{Code: ConfigBaseUnavailable}
	}
	if e.nativeRoot {
		if e.configRootErr != nil || !validAbsolute(e.GOOS, e.configRoot) {
			return "", warnings, &Error{Code: ConfigBaseUnavailable}
		}
		root = e.configRoot
	}
	return joinPath(e.GOOS, root, "agent-notifications", "config.json"), warnings, nil
}
func validAbsolute(goos, p string) bool {
	if strings.TrimSpace(p) == "" || strings.ContainsRune(p, 0) {
		return false
	}
	if goos != "windows" {
		return strings.HasPrefix(p, "/")
	}
	p = strings.ReplaceAll(p, "/", `\`)
	if strings.HasPrefix(p, `\\?\`) || strings.HasPrefix(p, `\\.\`) {
		return false
	}
	var rest string
	if len(p) >= 3 && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) && p[1] == ':' && p[2] == '\\' {
		rest = p[3:]
	} else if strings.HasPrefix(p, `\\`) {
		parts := strings.Split(p[2:], `\`)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return false
		}
		rest = p[2:]
	} else {
		return false
	}
	for _, c := range rest {
		if c < 32 || strings.ContainsRune(`:<>"|?*`, c) {
			return false
		}
	}
	for _, part := range strings.Split(rest, `\`) {
		if part == "." || part == ".." {
			continue
		}
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
		stem := strings.ToUpper(strings.Split(part, ".")[0])
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || (len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9') {
			return false
		}
	}
	return true
}
func cleanPath(goos, p string) string {
	if goos != "windows" {
		return path.Clean(p)
	}
	p = strings.ReplaceAll(p, `\`, "/")
	if strings.HasPrefix(p, "//") {
		parts := strings.SplitN(p[2:], "/", 3)
		if len(parts) < 2 {
			return strings.ReplaceAll(p, "/", `\`)
		}
		rest := "/"
		if len(parts) == 3 {
			rest += parts[2]
		}
		cleaned := path.Clean(rest)
		prefix := `\\` + parts[0] + `\` + parts[1]
		if cleaned == "/" {
			return prefix
		}
		return prefix + strings.ReplaceAll(cleaned, "/", `\`)
	}
	if len(p) >= 3 && p[1] == ':' {
		return p[:2] + strings.ReplaceAll(path.Clean("/"+p[3:]), "/", `\`)
	}
	return strings.ReplaceAll(path.Clean(p), "/", `\`)
}
func joinPath(goos string, parts ...string) string { return cleanPath(goos, strings.Join(parts, "/")) }
func splitPath(goos, p string) (string, string) {
	if goos == "windows" {
		p = strings.ReplaceAll(p, `\`, "/")
		return cleanPath(goos, path.Dir(p)), path.Base(p)
	}
	return filepath.Dir(p), filepath.Base(p)
}

// canonicalParent permits ordinary linked ancestors and missing suffixes, but
// does not follow the final config entry (which must remain diagnosable).
func canonicalParent(p string) (string, error) {
	parent, base := filepath.Split(p)
	parent = filepath.Clean(parent)
	suffix := []string{base}
	for {
		resolved, err := filepath.EvalSymlinks(parent)
		if err == nil {
			parts := append([]string{resolved}, suffix...)
			return normalizeExistingFinal(filepath.Join(parts...))
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", err
		}
		suffix = append([]string{filepath.Base(parent)}, suffix...)
		parent = next
	}
}
