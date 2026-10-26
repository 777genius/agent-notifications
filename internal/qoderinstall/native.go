// Package qoderinstall plans native Qoder installations with the real CLI and
// publishes the resulting files through the shared managed-runtime transaction.
package qoderinstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

const PluginID = "agent-notifications-qoder@local"
const ConsumerID = "qoder-notifications"
const receiptName = "qoder-receipt.json"
const registryName = "plugins/installed_plugins_v2.json"

type document struct {
	data   []byte
	before installruntime.Identity
}
type nativePlan struct {
	documents   map[string]document
	desired     map[string][]byte
	packageRoot string
}

func readDocument(path string) (document, error) {
	before, err := installruntime.Fingerprint(path)
	if err != nil || !before.Exists {
		return document{before: before}, err
	}
	if before.Link != "" {
		return document{}, errors.New("symbolic link refused")
	}
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 32<<20 {
		return document{}, errors.New("bounded regular file required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return document{}, err
	}
	after, err := installruntime.Fingerprint(path)
	if err != nil || after != before {
		return document{}, errors.New("file changed while reading")
	}
	return document{data: data, before: before}, nil
}

func object(data []byte) (map[string]json.RawMessage, error) {
	if len(data) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(data, &result) != nil || result == nil {
		return nil, errors.New("invalid native Qoder JSON")
	}
	return result, nil
}
func nativeEntry(registry []byte) (json.RawMessage, error) {
	doc, err := object(registry)
	if err != nil {
		return nil, err
	}
	plugins, err := object(doc["plugins"])
	if err != nil {
		return nil, err
	}
	return plugins[PluginID], nil
}
func nativeEnabled(settings []byte) (json.RawMessage, error) {
	doc, err := object(settings)
	if err != nil {
		return nil, err
	}
	enabled, err := object(doc["enabledPlugins"])
	if err != nil {
		return nil, err
	}
	return enabled[PluginID], nil
}
func compact(data []byte) string {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return ""
	}
	out, _ := json.Marshal(value)
	return string(out)
}

// planNative deliberately runs Qoder in a disposable home and profile. Native
// output defines the registry contract; only our entry's installPath is rebased.
// The live profile is never exposed to an external installer mid-transaction.
func planNative(ctx context.Context, r Request, source string, remove bool, previous *receipt) (nativePlan, func(), error) {
	p := nativePlan{documents: map[string]document{}, desired: map[string][]byte{}}
	temp, err := os.MkdirTemp("", "TEST-agent-notifications-qoder-")
	if err != nil {
		return p, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(temp) }
	profile := filepath.Join(temp, "profile")
	home := filepath.Join(temp, "home")
	project := filepath.Join(temp, "project")
	for _, path := range []string{profile, home, project} {
		if err = os.MkdirAll(path, 0700); err != nil {
			cleanup()
			return p, func() {}, err
		}
	}
	for _, rel := range []string{registryName, "settings.json"} {
		d, e := readDocument(filepath.Join(r.ConfigRoot, filepath.FromSlash(rel)))
		if e != nil {
			cleanup()
			return p, func() {}, e
		}
		if _, e = object(d.data); e != nil {
			cleanup()
			return p, func() {}, e
		}
		p.documents[rel] = d
		if d.before.Exists {
			target := filepath.Join(profile, filepath.FromSlash(rel))
			if e = os.MkdirAll(filepath.Dir(target), 0700); e == nil {
				e = os.WriteFile(target, d.data, 0600)
			}
			if e != nil {
				cleanup()
				return p, func() {}, e
			}
		}
	}
	entry, err := nativeEntry(p.documents[registryName].data)
	if err != nil {
		cleanup()
		return p, func() {}, err
	}
	enabled, err := nativeEnabled(p.documents["settings.json"].data)
	if err != nil {
		cleanup()
		return p, func() {}, err
	}
	if previous == nil && len(entry) > 0 {
		cleanup()
		return p, func() {}, errors.New("native Qoder plugin already exists without managed ownership")
	}
	if previous != nil && (compact(entry) != compact(previous.NativeEntry) || compact(enabled) != compact(previous.NativeEnabled)) {
		cleanup()
		return p, func() {}, errors.New("managed Qoder native registration was edited")
	}
	if previous != nil {
		// Uninstall may mark its cached package as orphaned. Never let its staged
		// registry point at the live profile, even for these ancillary writes.
		reg, _ := object(p.documents[registryName].data)
		plugins, _ := object(reg["plugins"])
		var entries []map[string]json.RawMessage
		if json.Unmarshal(plugins[PluginID], &entries) != nil || len(entries) != 1 {
			cleanup()
			return p, func() {}, errors.New("unsupported prior native Qoder registration")
		}
		rel, e := filepath.Rel(r.ConfigRoot, previous.PackageRoot)
		if e != nil || !inside(r.ConfigRoot, previous.PackageRoot) {
			cleanup()
			return p, func() {}, errors.New("prior Qoder package is outside profile")
		}
		stagedPackage := filepath.Join(profile, rel)
		if e = os.MkdirAll(stagedPackage, 0700); e != nil {
			cleanup()
			return p, func() {}, e
		}
		entries[0]["installPath"] = mustJSON(stagedPackage)
		plugins[PluginID] = mustJSON(entries)
		reg["plugins"] = mustJSON(plugins)
		if e = os.WriteFile(filepath.Join(profile, filepath.FromSlash(registryName)), mustJSON(reg), 0600); e != nil {
			cleanup()
			return p, func() {}, e
		}
	}
	args := []string{"--config-dir", profile, "plugins", "install", source, "--scope", "user", "--json"}
	if remove {
		args = []string{"--config-dir", profile, "plugins", "uninstall", PluginID, "--scope", "user", "--json", "--keep-data"}
	}
	cmd := exec.CommandContext(ctx, r.QoderExecutable, args...)
	cmd.Dir = project
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "QODER_CONFIG_DIR=" + profile}
	for _, key := range []string{"PATH", "SystemRoot", "SYSTEMROOT", "TMPDIR", "TEMP", "TMP"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	output, err := cmd.Output()
	if err != nil {
		cleanup()
		return p, func() {}, fmt.Errorf("native Qoder %s failed: %w", args[3], err)
	}
	var result struct {
		OK           bool   `json:"ok"`
		PluginID     string `json:"pluginId"`
		Path         string `json:"path"`
		FullyRemoved bool   `json:"fullyRemoved"`
	}
	if json.Unmarshal(output, &result) != nil || !result.OK || result.PluginID != PluginID {
		cleanup()
		return p, func() {}, errors.New("native Qoder returned an invalid installation result")
	}
	for rel := range p.documents {
		data, e := os.ReadFile(filepath.Join(profile, filepath.FromSlash(rel)))
		if e != nil {
			cleanup()
			return p, func() {}, e
		}
		p.desired[rel] = data
	}
	if remove {
		entry, e := nativeEntry(p.desired[registryName])
		if e != nil {
			cleanup()
			return p, func() {}, e
		}
		enabled, e := nativeEnabled(p.desired["settings.json"])
		if e != nil {
			cleanup()
			return p, func() {}, e
		}
		if !result.FullyRemoved || len(entry) > 0 || len(enabled) > 0 {
			cleanup()
			return p, func() {}, errors.New("native Qoder did not fully remove the user registration")
		}
	}
	if !remove {
		expected := filepath.Join(profile, "plugins", "cache", "local", "agent-notifications-qoder", r.Version)
		if result.Path != expected {
			cleanup()
			return p, func() {}, errors.New("native Qoder package path differs from expected local identity")
		}
		p.packageRoot = filepath.Join(r.ConfigRoot, "plugins", "cache", "local", "agent-notifications-qoder", r.Version)
		reg, e := object(p.desired[registryName])
		if e != nil {
			cleanup()
			return p, func() {}, e
		}
		plugins, e := object(reg["plugins"])
		if e != nil {
			cleanup()
			return p, func() {}, e
		}
		var entries []map[string]json.RawMessage
		if json.Unmarshal(plugins[PluginID], &entries) != nil || len(entries) != 1 || compact(entries[0]["scope"]) != "\"user\"" || compact(entries[0]["installPath"]) != compact(mustJSON(expected)) {
			cleanup()
			return p, func() {}, errors.New("unsupported native Qoder registration")
		}
		entries[0]["installPath"] = mustJSON(p.packageRoot)
		plugins[PluginID] = mustJSON(entries)
		reg["plugins"] = mustJSON(plugins)
		p.desired[registryName] = mustJSON(reg)
		err = filepath.WalkDir(result.Path, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				return errors.New("native Qoder package contains symbolic link")
			}
			rel, e := filepath.Rel(result.Path, path)
			if e != nil {
				return e
			}
			data, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			p.desired["package/"+filepath.ToSlash(rel)] = data
			return nil
		})
		if err != nil {
			cleanup()
			return p, func() {}, err
		}
	}
	return p, cleanup, nil
}
func mustJSON(v any) []byte { data, _ := json.Marshal(v); return append(data, '\n') }
func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
