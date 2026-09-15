package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// UpdatePreflightRequest contains adapter-supplied history, never resolver inputs.
// Baselines must come from release artifacts verified by the caller.
type UpdatePreflightRequest struct {
	Env                  EnvSnapshot           `json:"-"`
	Assets               AssetContext          `json:"-"`
	ActiveBundleRoots    []string              `json:"activeBundleRoots"`
	RefreshDirs          []string              `json:"refreshDirs"`
	ProtectedPaths       []string              `json:"protectedPaths"`
	HistoricalCandidates []HistoricalCandidate `json:"historicalCandidates"`
}
type UpdatePreflightResult struct {
	Status      string       `json:"status"`
	Selection   Selection    `json:"selection"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

// CheckHistorical is the shared read-only guard for automatic missing defaults
// and initialization. It never imports a candidate or guesses its version.
func CheckHistorical(legacy LegacyContext, read func(string, int) (Snapshot, error)) error {
	if read == nil {
		return &Error{Code: ConfigInvalid}
	}
	for _, c := range legacy.Candidates {
		snap, err := read(c.Path, MaxDocumentBytes)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			var ce *Error
			if errors.As(err, &ce) && ce.Code == ConfigLockTimeout {
				return err
			}
			return &Error{Code: ConfigLegacyImportRequired, Path: c.Path}
		}
		baseline := c.TrustedBaseline
		if c.BaselinePath != "" {
			b, err := read(c.BaselinePath, MaxDocumentBytes)
			if err != nil {
				var ce *Error
				if errors.As(err, &ce) && ce.Code == ConfigLockTimeout {
					return err
				}
				return &Error{Code: ConfigLegacyImportRequired, Path: c.Path}
			}
			baseline = b.Bytes
		}
		if c.BaselinePath != "" || c.BaselineSHA256 != "" {
			sum := sha256.Sum256(baseline)
			expected, err := hex.DecodeString(c.BaselineSHA256)
			if err != nil || len(expected) != sha256.Size || !bytes.Equal(expected, sum[:]) {
				return &Error{Code: ConfigLegacyImportRequired, Path: c.Path}
			}
		}
		if baseline == nil || !bytes.Equal(snap.Bytes, baseline) {
			return &Error{Code: ConfigLegacyImportRequired, Path: c.Path}
		}
	}
	return nil
}

// PreflightUpdate is a snapshot, not a durable authorization for later deletion.
// The caller must repeat it immediately before destructive refresh.
func PreflightUpdate(r UpdatePreflightRequest) (UpdatePreflightResult, error) {
	for attempt := 0; attempt < 3; attempt++ {
		out, err := preflightOnce(r)
		if err != nil {
			return out, err
		}
		next, err := resolveForRead(r.Env)
		if err != nil {
			return UpdatePreflightResult{Status: "invalid-config", Selection: next}, err
		}
		if next.Path == out.Selection.Path && next.Exists == out.Selection.Exists {
			return out, nil
		}
	}
	return UpdatePreflightResult{Status: "invalid-config"}, &Error{Code: ConfigChanged}
}
func preflightOnce(r UpdatePreflightRequest) (UpdatePreflightResult, error) {
	s, err := resolveForRead(r.Env)
	out := UpdatePreflightResult{Status: "safe", Selection: s}
	fail := func(status string, err error) (UpdatePreflightResult, error) {
		out.Status = status
		if e, ok := err.(*Error); ok {
			out.Diagnostics = append(out.Diagnostics, Diagnostic{Code: e.Code, Path: e.Path})
		}
		return out, err
	}
	if err != nil {
		return fail("invalid-config", err)
	}
	for _, protected := range r.ProtectedPaths {
		if !validAbsolute(r.Env.GOOS, protected) {
			return fail("unsafe-target", &Error{Code: ConfigUnsafeTarget, Path: protected})
		}
		same, e := sameNativePath(r.Env.GOOS, protected, s.Path)
		if e != nil {
			return fail("unsafe-target", pathError(protected, e))
		}
		if same {
			return fail("unsafe-target", &Error{Code: ConfigUnsafeTarget, Path: s.Path})
		}
	}
	// Check overlap before existing/explicit shortcuts. Canonicalizing ancestors
	// handles relocated bundles and aliases without deriving selection from them.
	for _, dir := range r.RefreshDirs {
		if !validAbsolute(r.Env.GOOS, dir) {
			return fail("unsafe-target", &Error{Code: ConfigUnsafeTarget, Path: dir})
		}
		within, e := withinNativePath(r.Env.GOOS, dir, s.Path)
		if e != nil {
			return fail("unsafe-target", pathError(dir, e))
		}
		if within {
			return fail("unsafe-target", &Error{Code: ConfigUnsafeTarget, Path: s.Path})
		}
	}
	if s.Exists {
		snap, e := ReadFileSnapshot(s.Path, MaxDocumentBytes)
		if e != nil {
			return fail("invalid-config", pathError(s.Path, e))
		}
		d, e := ParseDocument(snap.Bytes, snap.PhysicalPath, true)
		if e == nil {
			_, e = d.Effective(r.Assets)
		}
		if e != nil {
			return fail("invalid-config", pathError(s.Path, e))
		}
		return out, nil
	}
	if s.Source == "explicit" {
		return out, nil
	}
	candidates := append([]HistoricalCandidate(nil), r.HistoricalCandidates...)
	for _, root := range r.ActiveBundleRoots {
		if !validAbsolute(r.Env.GOOS, root) {
			return fail("import-required", &Error{Code: ConfigLegacyImportRequired, Path: root})
		}
		p := filepath.Join(root, "config", "config.json")
		found := false
		for _, c := range candidates {
			if filepath.Clean(c.Path) == p {
				found = true
				break
			}
		}
		if !found {
			candidates = append(candidates, HistoricalCandidate{Path: p})
		}
	}
	if e := CheckHistorical(LegacyContext{Candidates: candidates}, ReadFileSnapshot); e != nil {
		return fail("import-required", e)
	}
	return out, nil
}

type nativeAncestor struct {
	path   string
	info   fs.FileInfo
	suffix []string
}

// deepestNativeAncestor finds only the nearest existing ancestor. Preflight is
// read-only: uncertainty about missing names is handled conservatively below.
func deepestNativeAncestor(p string) (nativeAncestor, error) {
	p = filepath.Clean(p)
	var suffix []string
	for {
		info, err := os.Stat(p)
		if err == nil {
			physical, evalErr := filepath.EvalSymlinks(p)
			if evalErr != nil {
				return nativeAncestor{}, evalErr
			}
			return nativeAncestor{path: physical, info: info, suffix: suffix}, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nativeAncestor{}, err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return nativeAncestor{}, fs.ErrNotExist
		}
		suffix = append([]string{filepath.Base(p)}, suffix...)
		p = parent
	}
}

func sameNativePath(goos, a, b string) (bool, error) {
	aa, err := deepestNativeAncestor(a)
	if err != nil {
		return false, err
	}
	bb, err := deepestNativeAncestor(b)
	if err != nil {
		return false, err
	}
	if !os.SameFile(aa.info, bb.info) {
		return false, nil
	}
	return nativeSuffixEqual(goos, aa.suffix, bb.suffix), nil
}

func nativeSuffixEqual(goos string, a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] == b[i] {
			continue
		}
		// Windows and macOS may resolve absent spellings to the same future
		// entry. With no file to query, fail closed only for their case aliases.
		if (goos != "windows" && goos != "darwin") || !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

func withinNativePath(goos, root, target string) (bool, error) {
	rr, err := deepestNativeAncestor(root)
	if err != nil {
		return false, err
	}
	tt, err := deepestNativeAncestor(target)
	if err != nil {
		return false, err
	}
	if os.SameFile(rr.info, tt.info) {
		return nativeHasPathPrefix(goos, tt.suffix, rr.suffix), nil
	}
	// If target's deepest existing ancestor is below root's, query exactly the
	// corresponding prefix identity instead of scanning unrelated ancestors.
	candidate := tt.path
	var below []string
	for nativeDepth(candidate) > nativeDepth(rr.path) {
		below = append([]string{filepath.Base(candidate)}, below...)
		candidate = filepath.Dir(candidate)
	}
	if nativeDepth(candidate) != nativeDepth(rr.path) {
		return false, nil
	}
	prefixInfo, statErr := os.Stat(candidate)
	if statErr != nil {
		return false, statErr
	}
	if !os.SameFile(rr.info, prefixInfo) {
		return false, nil
	}
	components := append(below, tt.suffix...)
	return nativeHasPathPrefix(goos, components, rr.suffix), nil
}

func nativeDepth(p string) int {
	depth := 0
	for parent := filepath.Dir(p); parent != p; parent = filepath.Dir(p) {
		depth++
		p = parent
	}
	return depth
}

func nativeHasPathPrefix(goos string, path, prefix []string) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i := range prefix {
		if !nativeSuffixEqual(goos, path[i:i+1], prefix[i:i+1]) {
			return false
		}
	}
	return true
}
