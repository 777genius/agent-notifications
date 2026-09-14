// Package codexsetup registers this plugin's hooks with the Codex CLI.
//
// Setup explicitly registers user hooks in hooks.json, independently of native
// plugin-hook discovery. One Go implementation covers macOS, Linux, and Windows.
//
// The registered command must stay byte-identical across releases: Codex
// hashes the command string for its trust review, so a versioned path would
// force the user to re-approve the hook after every update. Setup therefore
// installs a self-contained copy of the plugin under a stable directory in
// the Codex home and points the hooks at that copy.
package codexsetup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// InstallDirName is the stable directory (inside the Codex home) that holds
// the plugin copy the hooks point at. Frozen: it is part of the hook command
// string and therefore of the Codex trust hash.
const InstallDirName = "claude-notifications-go"

// hookTimeoutSeconds matches the shipped hooks-codex.json contract.
const hookTimeoutSeconds = 30

// registeredEvents are the Codex events this plugin handles, in the order
// they are written. Kept in sync with hooks/hooks-codex.json.
var registeredEvents = []struct {
	event   string
	matcher string
}{
	{event: "PreToolUse", matcher: "^request_user_input$"},
	{event: "Stop"},
	{event: "SubagentStop"},
	{event: "PermissionRequest"},
}

// Options controls a setup run.
type Options struct {
	// RequireNative rejects an unqualified explicit notification install before hook commit.
	// Ordinary hook-only installation retains its existing behavior.
	RequireNative bool
	// Remove unregisters this consumer; last removal cleans only ledger-owned files.
	Remove bool
	// ControlRoot overrides shared runtime state for isolated tests.
	ControlRoot string
	// Context optionally supplies the complete setup deadline.
	Context context.Context
	// CodexHome overrides the Codex home directory (default: $CODEX_HOME,
	// then <user home>/.codex).
	CodexHome string
	// PluginRoot is the bundle to install from (default: the running
	// binary's plugin root).
	PluginRoot string
	// DryRun reports what would change without touching the filesystem.
	DryRun bool
}

// Result describes what a setup run did (or would do).
type Result struct {
	CodexHome   string
	InstallDir  string
	HooksPath   string
	BackupPath  string
	Events      []string
	Replaced    bool // an earlier registration was updated in place
	ForeignKept int  // hook entries owned by other tools that were preserved
}

// hookHandler is one handler entry in hooks.json.
//
// Handlers that came from the user's file are re-emitted byte-for-byte: a
// handler may be an entirely different kind (mcp_tool, prompt, agent) whose
// schema this package must not assume, and Codex rejects a handler carrying
// fields its variant does not define. Only handlers this package generates
// are rendered from typed fields.
type hookHandler struct {
	// raw is the verbatim source for handlers parsed from an existing file.
	raw json.RawMessage

	// Parsed view, used for ownership detection and for handlers we build.
	Type           string
	Command        string
	CommandWindows string
	Timeout        int
}

func (h hookHandler) MarshalJSON() ([]byte, error) {
	if len(h.raw) > 0 {
		return h.raw, nil
	}
	out := map[string]any{
		"type":    h.Type,
		"command": h.Command,
	}
	if h.CommandWindows != "" {
		out["commandWindows"] = h.CommandWindows
	}
	if h.Timeout != 0 {
		out["timeout"] = h.Timeout
	}
	return json.Marshal(out)
}

func (h *hookHandler) UnmarshalJSON(data []byte) error {
	h.raw = append(json.RawMessage(nil), data...)
	var parsed struct {
		Type            string `json:"type"`
		Command         string `json:"command"`
		CommandWindows  string `json:"commandWindows"`
		CommandWinSnake string `json:"command_windows"`
		Timeout         int    `json:"timeout"`
	}
	// A handler with an unexpected shape (for example a non-string command)
	// stays foreign and is preserved verbatim, so decode errors are ignored.
	_ = json.Unmarshal(data, &parsed)
	h.Type = parsed.Type
	h.Command = parsed.Command
	h.CommandWindows = parsed.CommandWindows
	if h.CommandWindows == "" {
		h.CommandWindows = parsed.CommandWinSnake
	}
	h.Timeout = parsed.Timeout
	return nil
}

// hookGroup is one matcher group in hooks.json. Unknown group-level keys are
// preserved so a user's own annotations survive a setup run.
type hookGroup struct {
	raw     json.RawMessage
	Matcher string
	Hooks   []hookHandler
	extra   map[string]json.RawMessage
}

func (g hookGroup) MarshalJSON() ([]byte, error) {
	if g.raw != nil {
		return g.raw, nil
	}
	out := map[string]json.RawMessage{}
	for k, v := range g.extra {
		out[k] = v
	}
	if g.Matcher != "" {
		raw, err := json.Marshal(g.Matcher)
		if err != nil {
			return nil, err
		}
		out["matcher"] = raw
	}
	hooks, err := json.Marshal(g.Hooks)
	if err != nil {
		return nil, err
	}
	out["hooks"] = hooks
	return json.Marshal(out)
}

func (g *hookGroup) UnmarshalJSON(data []byte) error {
	g.raw = append(json.RawMessage(nil), data...)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	g.extra = map[string]json.RawMessage{}
	for k, v := range raw {
		switch k {
		case "matcher":
			g.extra[k] = v
		case "hooks":
			if err := json.Unmarshal(v, &g.Hooks); err != nil {
				return fmt.Errorf("hook group has a malformed hooks array: %w", err)
			}
		default:
			g.extra[k] = v
		}
	}
	return nil
}

// hooksFile is the subset of hooks.json this package understands. Top-level
// keys other than "hooks" are preserved verbatim.
type hooksFile struct {
	Hooks map[string][]hookGroup
	extra map[string]json.RawMessage
}

func (f hooksFile) MarshalJSON() ([]byte, error) {
	out := map[string]json.RawMessage{}
	for k, v := range f.extra {
		out[k] = v
	}
	raw, err := json.Marshal(f.Hooks)
	if err != nil {
		return nil, err
	}
	out["hooks"] = raw
	return json.Marshal(out)
}

func (f *hooksFile) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	f.extra = map[string]json.RawMessage{}
	f.Hooks = map[string][]hookGroup{}
	for k, v := range raw {
		if k == "hooks" {
			if err := json.Unmarshal(v, &f.Hooks); err != nil {
				return fmt.Errorf("hooks section is not an object of event arrays: %w", err)
			}
			continue
		}
		f.extra[k] = v
	}
	return nil
}

// ResolveCodexHome returns the Codex home directory for this machine.
func ResolveCodexHome(override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return filepath.Abs(override)
	}
	if env := strings.TrimSpace(os.Getenv("CODEX_HOME")); env != "" {
		return filepath.Abs(env)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".codex"), nil
}

// HookCommands renders the POSIX and Windows command strings for one event.
// installDir must be the stable install directory.
//
// Quoting is done by hand rather than with %q: strconv escaping doubles
// backslashes, which cmd.exe does not unescape, and it would also make the
// generated command diverge from the frozen contract shape. Because the
// command string feeds the Codex trust hash, that divergence would cost every
// user a re-approval once corrected.
func HookCommands(installDir, event string) (posix string, windows string) {
	posixLauncher := filepath.ToSlash(filepath.Join(installDir, "bin", "codex-hook-wrapper.sh"))
	windowsLauncher := filepath.FromSlash(filepath.Join(installDir, "bin", "codex-hook-wrapper.cmd"))
	posix = fmt.Sprintf("sh %s handle-hook %s --product codex", posixQuote(posixLauncher), event)
	windows = fmt.Sprintf(`cmd.exe /d /v:off /s /c "%s handle-hook %s --product codex"`, windowsQuote(windowsLauncher), event)
	return posix, windows
}

// posixQuote wraps a path for `sh -c`. Single quotes are used so nothing
// inside is expanded: an absolute path may legitimately contain `$` or a
// backslash, both of which are special inside double quotes.
func posixQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// windowsQuote wraps a path for cmd.exe. Backslashes are literal there and a
// double quote cannot appear in a Windows path, so no escaping is possible or
// needed.
func windowsQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, "") + `"`
}

// ownsHandler reports whether a handler was registered by this plugin. The
// check is intentionally narrow: it must never claim a hook a user wrote by
// hand for a different tool.
func ownsHandler(h hookHandler, installDir, event string) bool {
	if h.Type != "command" {
		return false
	}
	posix, windows := HookCommands(installDir, event)
	// Accept only complete generated commands, including the legacy Windows form.
	legacy := fmt.Sprintf("cmd.exe /d /s /c call %s handle-hook %s --product codex", windowsQuote(filepath.Join(installDir, "bin", "codex-hook-wrapper.cmd")), event)
	return (h.Command == posix || h.Command == "") &&
		(h.CommandWindows == windows || h.CommandWindows == legacy || h.CommandWindows == "") &&
		(h.Command != "" || h.CommandWindows != "")
}

// Run performs (or simulates) the registration.
func Run(opts Options) (Result, error) {
	codexHome, err := ResolveCodexHome(opts.CodexHome)
	if err != nil {
		return Result{}, err
	}
	if opts.Remove {
		opts.PluginRoot = filepath.Join(codexHome, InstallDirName)
	}
	if opts.PluginRoot == "" {
		return Result{}, fmt.Errorf("plugin root is required")
	}
	pluginRoot, err := filepath.Abs(opts.PluginRoot)
	if err != nil {
		return Result{}, fmt.Errorf("resolve plugin root: %w", err)
	}
	if _, err := os.Stat(filepath.Join(pluginRoot, "bin", "codex-hook-wrapper.sh")); err != nil && !opts.Remove {
		return Result{}, fmt.Errorf("plugin root %q does not look like a claude-notifications bundle: %w", pluginRoot, err)
	}

	installDir := filepath.Join(codexHome, InstallDirName)
	hooksPath := filepath.Join(codexHome, "hooks.json")

	result := Result{
		CodexHome:  codexHome,
		InstallDir: installDir,
		HooksPath:  hooksPath,
	}
	for _, e := range registeredEvents {
		result.Events = append(result.Events, e.event)
	}

	physicalHome, err := canonicalPath(codexHome)
	if err != nil {
		return Result{}, err
	}
	hooksPath = filepath.Join(physicalHome, "hooks.json")
	result.HooksPath = hooksPath

	if err := validateInstallPath(installDir); err != nil {
		return Result{}, err
	}
	source, err := canonicalPath(pluginRoot)
	if err != nil {
		return Result{}, err
	}
	destination, err := canonicalPath(installDir)
	if err != nil {
		return Result{}, err
	}
	self := sameDir(source, destination)
	if info, err := os.Lstat(installDir); err != nil && !os.IsNotExist(err) {
		return Result{}, err
	} else if err == nil && info.Mode()&os.ModeSymlink != 0 && !self {
		return Result{}, fmt.Errorf("destination root is a symlink to another bundle")
	}
	if !self && (within(source, destination) || within(destination, source)) {
		return Result{}, fmt.Errorf("source and destination overlap")
	}
	if _, err := readHooksFile(hooksPath); err != nil {
		return Result{}, err
	}
	if !opts.DryRun {
		if err := os.MkdirAll(physicalHome, 0700); err != nil {
			return Result{}, err
		}
		lock := filepath.Join(physicalHome, ".claude-notifications-setup.lock")
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return Result{}, fmt.Errorf("setup lock (remove only after confirming no setup is running): %w", err)
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(lock)
			return Result{}, fmt.Errorf("close setup lock: %w", err)
		}
		defer func() { _ = os.Remove(lock) }()
	}
	var native *installruntime.NativeChange
	if !opts.DryRun && !opts.Remove {
		for _, name := range []string{"ClaudeNotifier.app", "terminal-notifier.app"} {
			candidate := filepath.Join(source, "bin", name)
			if _, e := os.Stat(candidate); os.IsNotExist(e) {
				continue
			} else if e != nil {
				return Result{}, e
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			native, err = installruntime.StageNative(ctx, opts.ControlRoot, candidate)
			cancel()
			if err != nil {
				return Result{}, err
			}
			break
		}
	}
	if native != nil {
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = installruntime.DiscardNative(cleanup, opts.ControlRoot, native)
		}()
	}
	if opts.RequireNative && !opts.DryRun && !opts.Remove {
		if native == nil || native.After.DecoderFloor < 1 || len(native.After.Attestation) == 0 {
			return Result{}, fmt.Errorf("qualified native notification runtime required")
		}
	}
	if !opts.DryRun && !opts.Remove {
		if err := preflightConfig(source, destination, !self, hooksPath); err != nil {
			return Result{}, err
		}
	}
	var files []installruntime.File
	if !opts.DryRun && !self && !opts.Remove {
		files, err = stageRuntimeFiles(source, destination)
		if err != nil {
			return Result{}, err
		}
		files, err = appendBundleLauncherFiles(destination, files)
		if err != nil {
			return Result{}, err
		}
	}
	if native != nil {
		aliases, e := installruntime.NativeAlias(native, filepath.Join(destination, "bin"))
		if e != nil {
			return Result{}, e
		}
		files = append(files, aliases...)
	}
	prepare := func() ([]installruntime.File, error) {
		snapshot, _ := installruntime.ReadInstalledSnapshot(opts.ControlRoot)
		for p := range snapshot.Ledger.Files {
			if filepath.Base(p) == "config.json" && filepath.Base(filepath.Dir(p)) == "config" {
				return nil, fmt.Errorf("legacy user config is recorded as immutable; a compatible kernel must migrate its ownership to mutable config before repair/removal (user bytes preserved)")
			}
		}
		before, err := installruntime.Fingerprint(hooksPath)
		if err != nil {
			return nil, err
		}
		existing, err := readHooksFile(hooksPath)
		if err != nil {
			return nil, err
		}
		merged, replaced, foreign := mergeHooksMode(existing, installDir, !opts.Remove)
		result.Replaced, result.ForeignKept = replaced, foreign
		data, err := json.MarshalIndent(merged, "", "  ")
		if err != nil {
			return nil, err
		}
		if !opts.DryRun && before.Exists {
			old, err := os.ReadFile(hooksPath)
			if err != nil {
				return nil, err
			}
			backup, err := os.CreateTemp(filepath.Dir(hooksPath), "hooks.json.backup-*")
			if err != nil {
				return nil, err
			}
			result.BackupPath = backup.Name()
			_, err = backup.Write(old)
			if err == nil {
				err = backup.Sync()
			}
			closeErr := backup.Close()
			if err != nil {
				return nil, err
			}
			if closeErr != nil {
				return nil, closeErr
			}
		}
		return []installruntime.File{{Path: hooksPath, Before: before, Data: append(data, '\n'), Mode: 0600}}, nil
	}
	if opts.DryRun {
		_, err = prepare()
		return result, err
	}
	ctx := opts.Context
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
	}
	commands := []string{}
	for _, event := range registeredEvents {
		posix, windows := HookCommands(installDir, event.event)
		commands = append(commands, posix, windows)
	}
	_, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: opts.ControlRoot, Owner: "existing-installer", RuntimeRoot: destination,
		RemoveConsumer: opts.Remove, ConsumerID: "codex:" + hooksPath, Consumer: installruntime.Consumer{Registration: hooksPath, Commands: commands},
		Native: native, Files: files, ConfigPaths: []string{hooksPath}, Prepare: prepare,
	})
	if err != nil {
		return Result{}, err
	}
	if !opts.Remove && !self {
		if err := dropStaleBinFiles(destination, files); err != nil {
			return Result{}, err
		}
		if err := createLegacyDefaults(ctx, source, destination); err != nil {
			return Result{}, err
		}
		retryBinary := filepath.Join(destination, "bin", "claude-notifications-"+runtime.GOOS+"-"+runtime.GOARCH)
		if runtime.GOOS == "windows" {
			retryBinary += ".exe"
		}
		if err := initializeConfig(source, retryBinary); err != nil {
			return result, err
		}
	}
	return result, nil
}

// RenderHooksJSON returns the hooks.json fragment this plugin registers, for
// users who prefer to merge it themselves.
func RenderHooksJSON(installDir string) ([]byte, error) {
	if err := validateInstallPath(installDir); err != nil {
		return nil, err
	}
	empty := hooksFile{Hooks: map[string][]hookGroup{}, extra: map[string]json.RawMessage{}}
	merged, _, _ := mergeHooks(empty, installDir)
	return json.MarshalIndent(merged, "", "  ")
}

func mergeHooks(existing hooksFile, installDir string) (hooksFile, bool, int) {
	return mergeHooksMode(existing, installDir, true)
}

func mergeHooksMode(existing hooksFile, installDir string, register bool) (hooksFile, bool, int) {
	out := hooksFile{Hooks: map[string][]hookGroup{}, extra: map[string]json.RawMessage{}}
	for k, v := range existing.extra {
		out.extra[k] = v
	}
	replaced := false
	foreign := 0

	for event, groups := range existing.Hooks {
		var kept []hookGroup
		if groups != nil {
			kept = []hookGroup{}
		}
		for _, g := range groups {
			var keptHandlers []hookHandler
			for _, h := range g.Hooks {
				if ownsHandler(h, installDir, event) {
					replaced = true
					continue
				}
				keptHandlers = append(keptHandlers, h)
				foreign++
			}
			if len(keptHandlers) == 0 && len(g.Hooks) > 0 {
				continue
			}
			if len(g.Hooks) != len(keptHandlers) {
				g.raw = nil
				g.Hooks = keptHandlers
			}
			kept = append(kept, g)
		}
		out.Hooks[event] = kept
	}

	if !register {
		return out, replaced, foreign
	}
	for _, spec := range registeredEvents {
		posix, windows := HookCommands(installDir, spec.event)
		group := hookGroup{
			Matcher: spec.matcher,
			Hooks: []hookHandler{{
				Type:           "command",
				Command:        posix,
				CommandWindows: windows,
				Timeout:        hookTimeoutSeconds,
			}},
		}
		out.Hooks[spec.event] = append(out.Hooks[spec.event], group)
	}
	return out, replaced, foreign
}

func readHooksFile(path string) (hooksFile, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return hooksFile{Hooks: map[string][]hookGroup{}, extra: map[string]json.RawMessage{}}, nil
	}
	if err != nil {
		return hooksFile{}, fmt.Errorf("cannot read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return hooksFile{Hooks: map[string][]hookGroup{}, extra: map[string]json.RawMessage{}}, nil
	}
	var parsed hooksFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		// Never overwrite a file we cannot understand: the user would lose
		// their own hooks.
		return hooksFile{}, fmt.Errorf("%s exists but is not valid JSON (%w); fix or move it, then run setup again", path, err)
	}
	if parsed.Hooks == nil {
		parsed.Hooks = map[string][]hookGroup{}
	}
	return parsed, nil
}

// writeHooksFile writes atomically, backing up any existing file first.
func writeHooksFile(path string, content hooksFile, expected []byte, existed bool) (string, error) {
	if err := checkHooksSnapshot(path, expected, existed); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(content, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}

	// Backups have unique names. A fixed ".backup" name would be overwritten on
	// the second run with our own generated file, destroying the only copy of
	// what the user originally had.
	backup := ""
	if prev, err := os.ReadFile(path); err == nil {
		f, err := os.CreateTemp(dir, "hooks.json.backup-*")
		if err != nil {
			return "", err
		}
		backup = f.Name()
		_, writeErr := f.Write(prev)
		closeErr := f.Close()
		if writeErr != nil {
			return "", writeErr
		}
		if closeErr != nil {
			return "", closeErr
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}

	tmp, err := os.CreateTemp(dir, "hooks-*.json.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return "", err
	}
	if err := checkHooksSnapshot(path, expected, existed); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", err
	}
	return backup, nil
}

// Only runtime assets belong in the installed bundle. User config and logs
// at the installation root are never refreshed from the source.
func runtimeEntry(name string) bool {
	return name == "bin" || name == "sounds" || name == "config" || name == "skills" || name == "claude_icon.png" || name == ".claude-plugin"
}

func validateInstallPath(path string) error {
	if runtime.GOOS == "windows" && strings.ContainsAny(path, "%!\"\r\n") {
		return fmt.Errorf("windows install path contains unsupported shell expansion characters")
	}
	return nil
}

func canonicalPath(path string) (string, error) {
	return installruntime.CanonicalPath(path)
}

func stageRuntimeFiles(source, destination string) ([]installruntime.File, error) {
	source, err := canonicalPath(source)
	if err != nil {
		return nil, err
	}
	destination, err = canonicalPath(destination)
	if err != nil {
		return nil, err
	}
	if within(source, destination) || within(destination, source) {
		return nil, fmt.Errorf("source and destination overlap")
	}
	return installruntime.StageFiles(source, destination, func(rel string) bool {
		parts := strings.Split(rel, string(filepath.Separator))
		if !runtimeEntry(parts[0]) {
			return false
		}
		if parts[0] == "config" {
			return false
		}
		if parts[0] == "skills" {
			return len(parts) == 1 || (parts[1] == "agent-notify" && (len(parts) == 2 || (len(parts) == 3 && parts[2] == "SKILL.md")))
		}
		if parts[0] == "bin" && len(parts) > 1 {
			return !strings.HasSuffix(parts[1], ".app") && runtimeBinary(parts[1])
		}
		return parts[0] != ".claude-plugin" || len(parts) == 1 || parts[1] == "plugin.json"
	})
}

func appendBundleLauncherFiles(destination string, files []installruntime.File) ([]installruntime.File, error) {
	destination, err := canonicalPath(destination)
	if err != nil {
		return nil, err
	}
	bin := filepath.Join(destination, "bin")
	binary := "claude-notifications-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	found := false
	present := map[string]bool{}
	for _, file := range files {
		present[file.Path] = true
		if filepath.Dir(file.Path) == bin && filepath.Base(file.Path) == binary && file.Link == "" && !file.Remove {
			found = true
		}
	}
	if !found {
		return files, nil
	}
	for _, name := range []string{"claude-notifications", "agent-notifications"} {
		path := filepath.Join(bin, name)
		launcher := installruntime.File{Link: binary, Mode: 0755}
		if runtime.GOOS == "windows" {
			path += ".bat"
			launcher.Link = ""
			launcher.Data = installruntime.WindowsLauncherScript(name, binary)
		}
		if present[path] {
			continue
		}
		before, err := installruntime.Fingerprint(path)
		if err != nil {
			return nil, err
		}
		launcher.Path = path
		launcher.Before = before
		files = append(files, launcher)
	}
	return files, nil
}

func dropStaleBinFiles(destination string, files []installruntime.File) error {
	bin := filepath.Join(destination, "bin")
	entries, err := os.ReadDir(bin)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, file := range files {
		if filepath.Dir(file.Path) == bin {
			keep[filepath.Base(file.Path)] = true
		}
	}
	for _, entry := range entries {
		name := entry.Name()
		if keep[name] || strings.HasSuffix(name, ".app") || runtimeBinary(name) {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if err := os.Remove(filepath.Join(bin, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func within(parent, child string) bool {
	if runtime.GOOS == "windows" {
		parent, child = strings.ToLower(parent), strings.ToLower(child)
	}
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func copyBundle(src, dst string) error {
	_, finish, err := stageBundle(src, dst)
	if err == nil {
		finish()
	}
	return err
}

// Stage every asset before moving any live entry. Retain old entries until
// hooks have been committed, and restore them on a reported failure.
func stageBundle(src, dst string) (func() error, func(), error) {
	source, err := canonicalPath(src)
	if err != nil {
		return nil, nil, err
	}
	destination, err := canonicalPath(dst)
	if err != nil {
		return nil, nil, err
	}
	if within(source, destination) || within(destination, source) {
		return nil, nil, fmt.Errorf("source and destination overlap")
	}
	if err := os.MkdirAll(dst, 0755); err != nil {
		return nil, nil, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dst), ".codex-bundle-*")
	if err != nil {
		return nil, nil, err
	}
	retain := false
	finish := func() {
		if !retain {
			_ = os.RemoveAll(stage)
		}
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		finish()
		return nil, nil, err
	}
	var names []string
	for _, entry := range entries {
		if !runtimeEntry(entry.Name()) {
			continue
		}
		name := entry.Name()
		if err := copyPath(filepath.Join(src, name), filepath.Join(stage, "new", name)); err != nil {
			finish()
			return nil, nil, err
		}
		names = append(names, name)
	}
	if err := installBundleLaunchers(filepath.Join(stage, "new", "bin"), runtime.GOOS, runtime.GOARCH); err != nil {
		finish()
		return nil, nil, err
	}
	var moved, installed []string
	rollback := func() error {
		retain = true
		for _, name := range installed {
			if err := os.RemoveAll(filepath.Join(dst, name)); err != nil {
				return fmt.Errorf("%w; recovery files: %s", err, stage)
			}
		}
		for _, name := range moved {
			if err := os.Rename(filepath.Join(stage, "old", name), filepath.Join(dst, name)); err != nil {
				return fmt.Errorf("%w; recovery files: %s", err, stage)
			}
		}
		retain = false
		return nil
	}
	if err := os.MkdirAll(filepath.Join(stage, "old"), 0700); err != nil {
		finish()
		return nil, nil, err
	}
	for _, name := range names {
		target := filepath.Join(dst, name)
		if _, err = os.Lstat(target); err == nil {
			err = os.Rename(target, filepath.Join(stage, "old", name))
			if err == nil {
				moved = append(moved, name)
			}
		} else if os.IsNotExist(err) {
			err = nil
		}
		if err == nil {
			err = os.Rename(filepath.Join(stage, "new", name), target)
			if err == nil {
				installed = append(installed, name)
			}
		}
		if err != nil {
			if restoreErr := rollback(); restoreErr != nil {
				return nil, nil, fmt.Errorf("%w; restore failed: %v; recovery files: %s", err, restoreErr, stage)
			}
			finish()
			return nil, nil, err
		}
	}
	return rollback, finish, nil
}

func copyPath(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		// Resolve symlinks (the shipped binary is one) so the install copy
		// never depends on the source tree.
		link, err := filepath.EvalSymlinks(src)
		if err != nil {
			return err
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(src))
		if err != nil {
			return err
		}
		if !within(parent, link) {
			return fmt.Errorf("asset symlink escapes its directory: %s", src)
		}
		resolved, err := os.Stat(src)
		if err != nil {
			return err
		}
		if !resolved.Mode().IsRegular() {
			return fmt.Errorf("symlink to non-regular asset is unsupported: %s", src)
		}
		return copyFile(src, dst, resolved.Mode())
	case info.IsDir():
		return copyDir(src, dst)
	default:
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular asset: %s", src)
		}
		return copyFile(src, dst, info.Mode())
	}
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if filepath.Base(src) == "bin" && !runtimeBinary(entry.Name()) {
			continue
		}
		// The shared launcher reads the release version from this manifest.
		// Marketplace metadata and unrelated plugin files are not runtime inputs.
		if filepath.Base(src) == ".claude-plugin" && entry.Name() != "plugin.json" {
			continue
		}
		if err := copyPath(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// Replace rather than truncate: the destination may be a running binary.
	_ = os.Remove(dst)
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func sameDir(a, b string) bool {
	ra, err1 := filepath.Abs(a)
	rb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(ra, rb)
	}
	return ra == rb
}

// SortedEvents returns the registered event names in a stable order, for
// human-facing output.
func SortedEvents() []string {
	out := make([]string, 0, len(registeredEvents))
	for _, e := range registeredEvents {
		out = append(out, e.event)
	}
	sort.Strings(out)
	return out
}

func runtimeBinary(name string) bool {
	switch name {
	case "terminal-notifier.app", "codex-hook-wrapper.sh", "codex-hook-wrapper.cmd", "hook-wrapper.sh", "install.sh", "claude-notifications", "agent-notifications", "claude-notifications.bat", "agent-notifications.bat", "claude-notifications.cmd", "agent-notifications.cmd", "ClaudeNotifier.app":
		return true
	}
	for _, platform := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			expected := "claude-notifications-" + platform + "-" + arch
			if platform == "windows" {
				// Toast clicks use the GUI helper to avoid opening a console window.
				if name == expected+"-focus.exe" {
					return true
				}
				expected += ".exe"
			}
			if name == expected {
				return true
			}
		}
	}
	return false
}

// The lock serializes installers. Snapshot checks also detect edits by tools
// that do not honor it; an external write in the final check/rename window
// cannot be excluded by portable filesystem APIs.
func checkHooksSnapshot(path string, expected []byte, existed bool) error {
	info, err := os.Lstat(path)
	if err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("hooks file must be a regular file: %s", path)
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	current, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if existed != (err == nil) || !bytes.Equal(current, expected) {
		return fmt.Errorf("hooks.json changed during setup; retry")
	}
	return nil
}

// Launchers are generated in the private staging tree before any live entry moves.
// This also upgrades older bundles containing only the platform executable.
func installBundleLaunchers(bin, platform, arch string) error {
	binary := "claude-notifications-" + platform + "-" + arch
	if platform == "windows" {
		binary += ".exe"
	}
	info, err := os.Lstat(filepath.Join(bin, binary))
	if os.IsNotExist(err) {
		return nil
	} // Lazy-install bundles may have no executable yet.
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("platform executable is not regular")
	}
	for _, name := range []string{"claude-notifications", "agent-notifications"} {
		target := filepath.Join(bin, name)
		if platform == "windows" {
			target += ".bat"
		}
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return err
		}
		if platform == "windows" {
			if err := os.WriteFile(target, installruntime.WindowsLauncherScript(name, binary), 0755); err != nil {
				return err
			}
		} else if err := os.Symlink(binary, target); err != nil {
			return err
		}
	}
	return nil
}
