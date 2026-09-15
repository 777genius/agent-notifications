package codexsetup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/testenv"
)

// fakeBundle builds a minimal plugin bundle that Run accepts.
func fakeBundle(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	testenv.Set(t, home)
	canonical := filepath.Join(home, "fixture-config.json")
	if err := os.WriteFile(canonical, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", canonical)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sounds"), 0o755); err != nil {
		t.Fatalf("mkdir sounds: %v", err)
	}
	files := map[string]string{
		"bin/codex-hook-wrapper.sh":  "#!/bin/sh\nexit 0\n",
		"bin/codex-hook-wrapper.cmd": "@echo off\r\nexit /b 0\r\n",
		"bin/hook-wrapper.sh":        "#!/bin/sh\nexit 0\n",
		"sounds/task-complete.mp3":   "not-really-audio",
		"config/config.json":         `{"notifications":{}}`,
	}
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	// Source-only directories that must NOT be copied into the install dir.
	if err := os.MkdirAll(filepath.Join(root, "internal", "hooks"), 0o755); err != nil {
		t.Fatalf("mkdir internal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "hooks", "hooks.go"), []byte("package hooks"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return root
}

func readHooks(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read hooks: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse hooks: %v", err)
	}
	return parsed
}

func TestRunCreatesRegistrationAndInstallCopy(t *testing.T) {
	bundle := fakeBundle(t)
	codexHome := t.TempDir()

	res, err := Run(Options{CodexHome: codexHome, PluginRoot: bundle})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Install copy carries runtime assets but not sources.
	for _, rel := range []string{"bin/codex-hook-wrapper.sh", "bin/codex-hook-wrapper.cmd", "sounds/task-complete.mp3"} {
		if _, err := os.Stat(filepath.Join(res.InstallDir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("install copy missing %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(res.InstallDir, "internal")); !os.IsNotExist(err) {
		t.Errorf("install copy must not contain sources (err=%v)", err)
	}

	parsed := readHooks(t, res.HooksPath)
	hooks, ok := parsed["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks section missing: %v", parsed)
	}
	for _, event := range []string{"Stop", "SubagentStop", "PermissionRequest", "PreToolUse"} {
		if _, ok := hooks[event]; !ok {
			t.Errorf("event %s not registered", event)
		}
	}

	// The command must point at the stable install dir, never at the source
	// bundle: the trust hash covers the command string. Compare against the
	// platform-specific renderings rather than the raw path, which is spelled
	// differently in each command (forward slashes on POSIX, native
	// separators for cmd.exe).
	posix, windows := HookCommands(res.InstallDir, "Stop")
	rawJSON, err := os.ReadFile(res.HooksPath)
	if err != nil {
		t.Fatalf("read hooks: %v", err)
	}
	body := string(rawJSON)
	for _, want := range []string{jsonEscape(posix), jsonEscape(windows)} {
		if !strings.Contains(body, want) {
			t.Errorf("hooks.json does not contain the expected command %q", want)
		}
	}
	bundlePosix, bundleWindows := HookCommands(bundle, "Stop")
	for _, unwanted := range []string{jsonEscape(bundlePosix), jsonEscape(bundleWindows)} {
		if strings.Contains(body, unwanted) {
			t.Errorf("hooks.json references the source bundle path: %q", unwanted)
		}
	}
}

// jsonEscape renders a string the way it appears inside a JSON document.
func jsonEscape(s string) string {
	encoded, err := json.Marshal(s)
	if err != nil {
		return s
	}
	return strings.Trim(string(encoded), `"`)
}

func TestRunIsIdempotentAndStable(t *testing.T) {
	bundle := fakeBundle(t)
	codexHome := t.TempDir()

	first, err := Run(Options{CodexHome: codexHome, PluginRoot: bundle})
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	firstData, err := os.ReadFile(first.HooksPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	second, err := Run(Options{CodexHome: codexHome, PluginRoot: bundle})
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	secondData, err := os.ReadFile(second.HooksPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Byte-identical output keeps the Codex trust hash valid across re-runs.
	if string(firstData) != string(secondData) {
		t.Fatalf("re-running setup changed hooks.json:\nfirst:\n%s\nsecond:\n%s", firstData, secondData)
	}
	if !second.Replaced {
		t.Error("second run should report replacing the previous registration")
	}

	parsed := readHooks(t, second.HooksPath)
	hooks := parsed["hooks"].(map[string]any)
	stop := hooks["Stop"].([]any)
	if len(stop) != 1 {
		t.Fatalf("Stop has %d groups after re-run, want 1 (no duplicates)", len(stop))
	}
}

func TestRunPreservesForeignHooks(t *testing.T) {
	bundle := fakeBundle(t)
	codexHome := t.TempDir()
	hooksPath := filepath.Join(codexHome, "hooks.json")

	existing := `{
  "description": "my setup",
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "/usr/local/bin/my-own-tool", "timeout": 5, "customKey": true}]}],
    "SessionEnd": [{"hooks": [{"type": "command", "command": "echo bye"}]}]
  }
}`
	if err := os.WriteFile(hooksPath, []byte(existing), 0o600); err != nil {
		t.Fatalf("seed hooks: %v", err)
	}

	res, err := Run(Options{CodexHome: codexHome, PluginRoot: bundle})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if res.ForeignKept != 2 {
		t.Errorf("ForeignKept = %d, want 2", res.ForeignKept)
	}
	if res.BackupPath == "" {
		t.Error("existing file must be backed up")
	}
	if backup, err := os.ReadFile(res.BackupPath); err != nil || string(backup) != existing {
		t.Errorf("backup does not match the original file (err=%v)", err)
	}

	parsed := readHooks(t, hooksPath)
	if parsed["description"] != "my setup" {
		t.Errorf("unknown top-level key lost: %v", parsed["description"])
	}
	hooks := parsed["hooks"].(map[string]any)
	if _, ok := hooks["SessionEnd"]; !ok {
		t.Error("foreign event SessionEnd was dropped")
	}

	stop := hooks["Stop"].([]any)
	if len(stop) != 2 {
		t.Fatalf("Stop has %d groups, want 2 (foreign + ours)", len(stop))
	}
	foreign := stop[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if foreign["command"] != "/usr/local/bin/my-own-tool" {
		t.Errorf("foreign handler mangled: %v", foreign)
	}
	if foreign["customKey"] != true {
		t.Errorf("unknown handler key lost: %v", foreign)
	}
}

// TestBackupsNeverOverwriteTheOriginal guards the recovery path: with a fixed
// backup name, the second run would replace the user's original file with our
// own generated output, leaving nothing to restore from.
func TestBackupsNeverOverwriteTheOriginal(t *testing.T) {
	bundle := fakeBundle(t)
	codexHome := t.TempDir()
	hooksPath := filepath.Join(codexHome, "hooks.json")

	original := `{"note":"keep me","hooks":{"SessionEnd":[{"hooks":[{"type":"command","command":"echo bye"}]}]}}`
	if err := os.WriteFile(hooksPath, []byte(original), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	first, err := Run(Options{CodexHome: codexHome, PluginRoot: bundle})
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	second, err := Run(Options{CodexHome: codexHome, PluginRoot: bundle})
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}

	if first.BackupPath == second.BackupPath {
		t.Fatalf("both runs used the same backup path %q", first.BackupPath)
	}
	saved, err := os.ReadFile(first.BackupPath)
	if err != nil {
		t.Fatalf("read first backup: %v", err)
	}
	if string(saved) != original {
		t.Errorf("the user's original file is no longer recoverable:\ngot:  %s\nwant: %s", saved, original)
	}
}

// TestInstallCopyDropsStaleFiles guards macOS notification delivery: a file
// left inside ClaudeNotifier.app from an older release invalidates its code
// signature ("a sealed resource is missing or invalid").
func TestInstallCopyDropsStaleFiles(t *testing.T) {
	bundle := fakeBundle(t)
	codexHome := t.TempDir()

	res, err := Run(Options{CodexHome: codexHome, PluginRoot: bundle})
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}

	stale := filepath.Join(res.InstallDir, "bin", "stale-from-old-release.txt")
	if err := os.WriteFile(stale, []byte("left over"), 0o644); err != nil {
		t.Fatalf("plant stale file: %v", err)
	}
	// A file the install dir accumulates at its own root (a log) must survive.
	liveLog := filepath.Join(res.InstallDir, "notification-debug.log")
	if err := os.WriteFile(liveLog, []byte("log line"), 0o644); err != nil {
		t.Fatalf("plant log: %v", err)
	}

	if _, err := Run(Options{CodexHome: codexHome, PluginRoot: bundle}); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale file survived the refresh (err=%v)", err)
	}
	if _, err := os.Stat(liveLog); err != nil {
		t.Errorf("runtime file at the install root was deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(res.InstallDir, "bin", "codex-hook-wrapper.sh")); err != nil {
		t.Errorf("refresh lost a bundle file: %v", err)
	}
}

func TestRunRefusesCorruptedHooksFile(t *testing.T) {
	bundle := fakeBundle(t)
	codexHome := t.TempDir()
	hooksPath := filepath.Join(codexHome, "hooks.json")
	corrupted := "{not json"
	if err := os.WriteFile(hooksPath, []byte(corrupted), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := Run(Options{CodexHome: codexHome, PluginRoot: bundle}); err == nil {
		t.Fatal("expected an error instead of overwriting an unparseable file")
	}
	after, err := os.ReadFile(hooksPath)
	if err != nil || string(after) != corrupted {
		t.Fatalf("the user's file was modified: %q (err=%v)", after, err)
	}
}

func TestRunRejectsNonBundle(t *testing.T) {
	if _, err := Run(Options{CodexHome: t.TempDir(), PluginRoot: t.TempDir()}); err == nil {
		t.Fatal("expected an error for a directory that is not a plugin bundle")
	}
	if _, err := Run(Options{CodexHome: t.TempDir()}); err == nil {
		t.Fatal("expected an error for an empty plugin root")
	}
}

func TestRunRegistersInstalledBundle(t *testing.T) {
	testenv.Set(t, t.TempDir())
	codexHome := t.TempDir()
	installDir := filepath.Join(codexHome, InstallDirName)
	if err := os.MkdirAll(filepath.Join(installDir, "bin"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "bin", "codex-hook-wrapper.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Run(Options{CodexHome: codexHome, PluginRoot: installDir}); err != nil {
		t.Fatal(err)
	}
}

func TestDryRunTouchesNothing(t *testing.T) {
	bundle := fakeBundle(t)
	codexHome := t.TempDir()

	res, err := Run(Options{CodexHome: codexHome, PluginRoot: bundle, DryRun: true})
	if err != nil {
		t.Fatalf("Run(dry) error = %v", err)
	}
	if _, err := os.Stat(res.HooksPath); !os.IsNotExist(err) {
		t.Errorf("dry run created %s", res.HooksPath)
	}
	if _, err := os.Stat(res.InstallDir); !os.IsNotExist(err) {
		t.Errorf("dry run created %s", res.InstallDir)
	}
}

func TestHookCommandsCarryBothPlatforms(t *testing.T) {
	posix, windows := HookCommands(filepath.Join("/home/u/.codex", InstallDirName), "Stop")
	if !strings.HasPrefix(posix, "sh ") || !strings.Contains(posix, "codex-hook-wrapper.sh") ||
		!strings.HasSuffix(posix, "handle-hook Stop --product codex") {
		t.Errorf("posix command = %q", posix)
	}
	if !strings.HasPrefix(windows, "cmd.exe /d /v:off /s /c ") || !strings.Contains(windows, "codex-hook-wrapper.cmd") {
		t.Errorf("windows command = %q", windows)
	}
}

// TestHookCommandsQuotingIsLiteral guards the command shape that feeds the
// Codex trust hash. strconv-style quoting (%q) doubles backslashes, which
// cmd.exe does not unescape; correcting that after a release would cost every
// Windows user a re-approval.
// The quoting helpers are exercised directly: filepath.Join renders separators
// for the host, so a Windows path cannot be built portably inside a test.
func TestHookCommandsQuotingIsLiteral(t *testing.T) {
	windowsPath := `C:\Users\Test User\.codex\` + InstallDirName + `\bin\codex-hook-wrapper.cmd`
	quoted := windowsQuote(windowsPath)
	if strings.Contains(quoted, `\\`) {
		t.Errorf("windowsQuote doubled backslashes: %q", quoted)
	}
	if quoted != `"`+windowsPath+`"` {
		t.Errorf("windowsQuote(%q) = %q, want the path wrapped in plain double quotes", windowsPath, quoted)
	}

	// A POSIX path may legitimately contain characters a shell expands inside
	// double quotes; single quoting keeps it literal.
	if got := posixQuote(`/home/u$er/.codex/x`); got != `'/home/u$er/.codex/x'` {
		t.Errorf("posixQuote = %q, want a single-quoted literal", got)
	}
	// Paths containing a single quote must stay parseable by sh.
	if got := posixQuote(`/home/o'brien/x`); got != `'/home/o'\''brien/x'` {
		t.Errorf("posixQuote = %q, want the embedded quote escaped", got)
	}

	// End to end, both commands must carry the launcher and the argv.
	posix, windows := HookCommands(filepath.Join("/home/u/.codex", InstallDirName), "Stop")
	if !strings.Contains(posix, "codex-hook-wrapper.sh") || !strings.HasSuffix(posix, "handle-hook Stop --product codex") {
		t.Errorf("posix command = %q", posix)
	}
	if !strings.Contains(windows, "codex-hook-wrapper.cmd") || !strings.HasSuffix(windows, `handle-hook Stop --product codex"`) {
		t.Errorf("windows command = %q", windows)
	}
}

// TestRunPreservesForeignHandlerKindsVerbatim guards against the worst
// failure mode of editing someone else's config: a handler of another kind
// (mcp_tool, prompt, agent) must not gain fields its schema does not define,
// because Codex would then reject the whole file and the user would lose
// every hook they have.
func TestRunPreservesForeignHandlerKindsVerbatim(t *testing.T) {
	bundle := fakeBundle(t)
	codexHome := t.TempDir()
	hooksPath := filepath.Join(codexHome, "hooks.json")

	existing := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "^shell$",
        "description": "group annotation",
        "hooks": [
          {"type": "mcp_tool", "server": "audit", "tool": "log", "timeout": 0},
          {"type": "command", "command": "echo hi", "async": false, "statusMessage": "checking"}
        ]
      }
    ]
  }
}`
	if err := os.WriteFile(hooksPath, []byte(existing), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := Run(Options{CodexHome: codexHome, PluginRoot: bundle}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	parsed := readHooks(t, hooksPath)
	groups := parsed["hooks"].(map[string]any)["PreToolUse"].([]any)

	var foreignGroup map[string]any
	for _, g := range groups {
		group := g.(map[string]any)
		if group["matcher"] == "^shell$" {
			foreignGroup = group
		}
	}
	if foreignGroup == nil {
		t.Fatal("foreign group disappeared")
	}
	if foreignGroup["description"] != "group annotation" {
		t.Errorf("group-level key lost: %v", foreignGroup["description"])
	}

	handlers := foreignGroup["hooks"].([]any)
	if len(handlers) != 2 {
		t.Fatalf("foreign handlers = %d, want 2", len(handlers))
	}

	mcp := handlers[0].(map[string]any)
	if _, injected := mcp["command"]; injected {
		t.Errorf("a command field was injected into an mcp_tool handler: %v", mcp)
	}
	if mcp["server"] != "audit" || mcp["tool"] != "log" {
		t.Errorf("mcp_tool handler mangled: %v", mcp)
	}
	if timeout, ok := mcp["timeout"]; !ok || timeout.(float64) != 0 {
		t.Errorf("explicit zero timeout lost: %v", mcp)
	}

	cmd := handlers[1].(map[string]any)
	if cmd["statusMessage"] != "checking" {
		t.Errorf("statusMessage lost: %v", cmd)
	}
	if async, ok := cmd["async"]; !ok || async.(bool) {
		t.Errorf("explicit async=false lost: %v", cmd)
	}
}

// TestOwnsHandlerBoundaries checks both directions of ownership detection:
// claiming a foreign hook would delete a user's configuration, and failing to
// claim our own would duplicate registrations on every run.
func TestOwnsHandlerBoundaries(t *testing.T) {
	ourPosix, ourWindows := HookCommands("/home/u/.codex/"+InstallDirName, "Stop")

	owned := []hookHandler{
		{Type: "command", Command: ourPosix},
		{Type: "command", CommandWindows: ourWindows},
	}
	for _, h := range owned {
		if !ownsHandler(h, "/home/u/.codex/"+InstallDirName, "Stop") {
			t.Errorf("ownsHandler(%q/%q) = false, want true", h.Command, h.CommandWindows)
		}
	}

	foreign := []hookHandler{
		{Type: "command", Command: "echo " + ourPosix},
		{Type: "command", Command: ourPosix + " && echo other"},
		{Type: "command", Command: ourPosix, CommandWindows: "echo foreign"},
		{Command: "echo hi"},
		{Command: "/usr/local/bin/my-tool --product codex"},               // no launcher name
		{Command: "sh /opt/other/codex-hook-wrapper.sh handle-hook Stop"}, // no product flag
		{Type: "mcp_tool"}, // no command at all
	}
	for _, h := range foreign {
		if ownsHandler(h, "/home/u/.codex/"+InstallDirName, "Stop") {
			t.Errorf("ownsHandler(%q) = true, want false", h.Command)
		}
	}
}

func TestResolveCodexHomePrecedence(t *testing.T) {
	root := t.TempDir()
	explicit := filepath.Join(root, "explicit")
	envHome := filepath.Join(root, "env-codex")
	t.Setenv("CODEX_HOME", envHome)
	got, err := ResolveCodexHome(explicit)
	if err != nil || got != explicit {
		t.Fatalf("explicit override = %q, %v", got, err)
	}
	got, err = ResolveCodexHome("")
	if err != nil || got != envHome {
		t.Fatalf("env override = %q, %v", got, err)
	}

	t.Setenv("CODEX_HOME", "")
	got, err = ResolveCodexHome("")
	if err != nil {
		t.Fatalf("home fallback error = %v", err)
	}
	if !strings.HasSuffix(got, filepath.Join(".codex")) {
		t.Fatalf("home fallback = %q, want a path ending in .codex", got)
	}
}

func TestRenderHooksJSONMatchesWrittenFile(t *testing.T) {
	bundle := fakeBundle(t)
	codexHome := t.TempDir()

	res, err := Run(Options{CodexHome: codexHome, PluginRoot: bundle})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	written, err := os.ReadFile(res.HooksPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	rendered, err := RenderHooksJSON(res.InstallDir)
	if err != nil {
		t.Fatalf("RenderHooksJSON() error = %v", err)
	}
	if strings.TrimSpace(string(written)) != strings.TrimSpace(string(rendered)) {
		t.Errorf("--print output differs from the written file:\nwritten:\n%s\nprinted:\n%s", written, rendered)
	}
}
