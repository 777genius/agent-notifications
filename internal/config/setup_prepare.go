package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/strictjson"
)

const globalConfigLimit = 64 * 1024

var errGlobalConfig = errors.New("invalid global desktop configuration")

// PrepareGlobalResult contains provenance, never configuration contents.
// Source is canonical, legacy, or defaults. Ready requires persisted validation.
type PrepareGlobalResult struct {
	Source         string
	Changed, Ready bool
}

func desktopObject(raw []byte) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, errGlobalConfig
	}
	return m, nil
}

// rejectDesktopAliases prevents encoding/json's case-insensitive legacy decoder
// from observing different owned fields than the exact-key setup/runtime parser.
// EqualFold includes Unicode simple folds (for example long s and Kelvin sign).
// Unknown keys at each level remain opaque and are preserved.
func rejectDesktopAliases(m map[string]json.RawMessage, owned ...string) error {
	for key := range m {
		for _, canonical := range owned {
			if key != canonical && strings.EqualFold(key, canonical) {
				return fmt.Errorf("%w: noncanonical owned key alias", errGlobalConfig)
			}
		}
	}
	return nil
}

func completeDesktop(raw []byte, complete bool) ([]byte, [3]bool, bool, error) {
	var values [3]bool
	if strictjson.Validate(raw, strictjson.Budget{Bytes: globalConfigLimit, Depth: 16, Entries: 1024}) != nil {
		return nil, values, false, errGlobalConfig
	}
	top, err := desktopObject(raw)
	if err != nil {
		return nil, values, false, err
	}
	changed := false
	child := func(m map[string]json.RawMessage, key string) (map[string]json.RawMessage, error) {
		if err := rejectDesktopAliases(m, key); err != nil {
			return nil, err
		}
		b, ok := m[key]
		if !ok && complete {
			changed = true
			return map[string]json.RawMessage{}, nil
		}
		return desktopObject(b)
	}
	notifications, err := child(top, "notifications")
	if err != nil {
		return nil, values, false, err
	}
	desktop, err := child(notifications, "desktop")
	if err != nil {
		return nil, values, false, err
	}
	if err := rejectDesktopAliases(desktop, "enabled", "sound", "clickToFocus"); err != nil {
		return nil, values, false, err
	}
	for i, k := range []string{"enabled", "sound", "clickToFocus"} {
		b, ok := desktop[k]
		if !ok && complete {
			b = json.RawMessage("true")
			desktop[k] = b
			changed = true
		}
		if !bytes.Equal(bytes.TrimSpace(b), []byte("true")) && !bytes.Equal(bytes.TrimSpace(b), []byte("false")) {
			return nil, values, false, errGlobalConfig
		}
		values[i] = bytes.Equal(bytes.TrimSpace(b), []byte("true"))
	}
	if !changed {
		return raw, values, false, nil
	}
	desktopRaw, _ := json.Marshal(desktop)
	notifications["desktop"] = desktopRaw
	notificationsRaw, _ := json.Marshal(notifications)
	top["notifications"] = notificationsRaw
	out, err := json.Marshal(top)
	if err == nil {
		_, _, _, err = ValidateGlobalDesktop(out)
	}
	return out, values, true, err
}

// ValidateGlobalDesktop is the shared strict runtime/setup parser. All three
// fields must be explicit booleans, including when desktop is disabled.
func ValidateGlobalDesktop(raw []byte) (enabled, sound, clickToFocus bool, err error) {
	_, v, _, err := completeDesktop(raw, false)
	return v[0], v[1], v[2], err
}

// physicalParent walks with confined descriptors and rejects every symlink.
// Only explicit preparation of the canonical destination may create directories.
func physicalParent(path string, create bool) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(path) == string(filepath.Separator) {
		return nil, fmt.Errorf("config path must be clean and absolute")
	}
	path, err := installruntime.PhysicalPath(path)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(string(filepath.Separator))
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Dir(path), string(filepath.Separator)), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		info, e := root.Lstat(part)
		if os.IsNotExist(e) && create {
			e = root.Mkdir(part, 0700)
			if e == nil || os.IsExist(e) {
				info, e = root.Lstat(part)
			}
		}
		if e != nil {
			root.Close()
			return nil, e
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			root.Close()
			return nil, fmt.Errorf("config parent is not physical")
		}
		next, e := root.OpenRoot(part)
		if e != nil {
			root.Close()
			return nil, e
		}
		actual, e := next.Stat(".")
		if e != nil || !os.SameFile(info, actual) {
			next.Close()
			root.Close()
			return nil, fmt.Errorf("config parent changed")
		}
		root.Close()
		root = next
	}
	return root, nil
}

func readPrepared(root *os.Root, name string) ([]byte, os.FileInfo, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > globalConfigLimit {
		return nil, nil, errGlobalConfig
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return nil, nil, errGlobalConfig
	}
	raw, err := io.ReadAll(io.LimitReader(f, globalConfigLimit+1))
	if len(raw) > globalConfigLimit {
		return nil, nil, errGlobalConfig
	}
	return raw, info, err
}

func samePreparedParent(root *os.Root, path string) error {
	current, err := physicalParent(path, false)
	if err != nil {
		return err
	}
	defer current.Close()
	a, err := root.Stat(".")
	if err != nil {
		return err
	}
	b, err := current.Stat(".")
	if err != nil || !os.SameFile(a, b) {
		return fmt.Errorf("config parent changed")
	}
	return nil
}

// PrepareGlobalConfig explicitly prepares mutable user configuration, outside
// component/journal transactions and asset ledgers. Changed JSON may be
// reformatted; complete canonical bytes are never rewritten. Cooperating
// writers use canonicalPath+".lock", a permanent config-only lock. The final
// preimage check cannot fence a noncooperating writer after that check.
func PrepareGlobalConfig(ctx context.Context, canonicalPath, legacyPath, defaultsPath string) (result PrepareGlobalResult, err error) {
	if ctx == nil {
		return result, errors.New("nil preparation context")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, p := range []string{canonicalPath, legacyPath, defaultsPath} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || p == string(filepath.Separator) {
			return result, errors.New("config paths must be explicit clean absolute paths")
		}
	}
	if err = ctx.Err(); err != nil {
		return
	}
	root, err := physicalParent(canonicalPath, true)
	if err != nil {
		return result, err
	}
	defer root.Close()
	if err = samePreparedParent(root, canonicalPath); err != nil {
		return
	}
	unlock, err := installruntime.Lock(ctx, canonicalPath+".lock")
	if err != nil {
		return result, err
	}
	defer unlock()
	if err = samePreparedParent(root, canonicalPath); err != nil {
		return
	}
	name := filepath.Base(canonicalPath)
	before, info, err := readPrepared(root, name)
	absent := os.IsNotExist(err)
	result.Source = "canonical"
	raw := before
	if absent {
		for i, p := range []string{legacyPath, defaultsPath} {
			result.Source = []string{"legacy", "defaults"}[i]
			source, e := physicalParent(p, false)
			if e == nil {
				raw, _, e = readPrepared(source, filepath.Base(p))
				if e == nil {
					e = samePreparedParent(source, p)
				}
				source.Close()
			}
			if i == 0 && os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return result, e
			}
			break
		}
	} else if err != nil {
		return result, err
	}
	out, _, changed, err := completeDesktop(raw, true)
	if err != nil {
		return result, err
	}
	if !absent && !changed {
		if err = samePreparedParent(root, canonicalPath); err != nil {
			return result, err
		}
		result.Ready = true
		return result, nil
	}
	for attempt := 0; attempt < 100; attempt++ {
		temp := fmt.Sprintf(".config-prepare-%d-%d", os.Getpid(), attempt)
		f, e := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(e) {
			continue
		}
		if e != nil {
			return result, e
		}
		defer root.Remove(temp)
		_, e = f.Write(out)
		if e == nil && !absent {
			e = f.Chmod(info.Mode())
		}
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			return result, e
		}
		if e = ctx.Err(); e != nil {
			return result, e
		}
		if e = samePreparedParent(root, canonicalPath); e != nil {
			return result, e
		}
		if absent {
			e = root.Link(temp, name)
			if os.IsExist(e) {
				result.Source = "canonical"
			} else if e != nil {
				return result, e
			} else {
				result.Changed = true
			}
		} else {
			current, currentInfo, e := readPrepared(root, name)
			if e != nil || !os.SameFile(info, currentInfo) || !bytes.Equal(before, current) {
				return result, errors.New("global config preimage conflict")
			}
			if e = root.Rename(temp, name); e != nil {
				return result, e
			}
			result.Changed = true
		}
		dir, e := root.Open(".")
		if e != nil {
			return result, e
		}
		e = dir.Sync()
		dir.Close()
		if e != nil {
			return result, e
		}
		persisted, _, e := readPrepared(root, name)
		if e != nil {
			return result, e
		}
		if e = samePreparedParent(root, canonicalPath); e != nil {
			return result, e
		}
		_, _, _, e = ValidateGlobalDesktop(persisted)
		result.Ready = e == nil
		return result, e
	}
	return result, errors.New("global config staging busy")
}

// InspectGlobalConfig returns the exact strict preparation candidate without
// creating parents or locks. PrepareGlobalConfig rechecks the sources on commit.
func InspectGlobalConfig(ctx context.Context, paths ...string) ([]byte, error) {
	if ctx == nil || len(paths) != 3 {
		return nil, errGlobalConfig
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || p == string(filepath.Separator) {
			return nil, errGlobalConfig
		}
	}
	for i, p := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		root, err := physicalParent(p, false)
		var raw []byte
		if err == nil {
			raw, _, err = readPrepared(root, filepath.Base(p))
			if err == nil {
				err = samePreparedParent(root, p)
			}
			root.Close()
		}
		if os.IsNotExist(err) && i < 2 {
			continue
		}
		if err != nil {
			return nil, err
		}
		out, _, _, err := completeDesktop(raw, true)
		return out, err
	}
	return nil, errGlobalConfig
}
