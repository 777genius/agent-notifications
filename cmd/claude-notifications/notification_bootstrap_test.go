//go:build linux || darwin

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func notificationRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("missing caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func retryArgv(t *testing.T, output, needle string) []string {
	t.Helper()
	clean := ansiEscape.ReplaceAllString(output, "")
	var line string
	for _, candidate := range strings.Split(clean, "\n") {
		if strings.Contains(candidate, needle) {
			line = strings.TrimSpace(candidate)
		}
	}
	if line == "" {
		t.Fatalf("missing %q retry in %s", needle, output)
	}
	if i := strings.Index(line, "Retry: "); i >= 0 {
		line = strings.TrimSpace(line[i+len("Retry: "):])
	}
	cmd := exec.Command("bash", "-c", `eval "set -- $1"; printf '%s\0' "$@"`, "_", line)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("eval retry %q: %v", line, err)
	}
	parts := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	if len(parts) == 0 || (len(parts) == 1 && parts[0] == "") {
		t.Fatalf("empty retry argv from %q", line)
	}
	return parts
}

func flagValue(argv []string, name string) string {
	for i, arg := range argv {
		if arg == name && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

func TestNotificationBootstrapOffline(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(notificationRepoRoot(t), "bin", "bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "cli_has_setup_codex_skip_agent_notify") {
		t.Fatal("install_codex must only pass --skip-agent-notify when the published CLI advertises it")
	}
	if !strings.Contains(string(source), "cli_has_setup_wizard") {
		t.Fatal("bootstrap must probe wizard before calling configure")
	}
	prefix := strings.TrimSuffix(strings.TrimSpace(string(source)), `main "$@"`)
	for _, test := range []struct {
		name, args    string
		fail          bool
		wantConfigure bool
	}{
		{"ordinary", "--product both", false, true},
		{"both", "--product both --agent-notify --navigation none --allow-unknown-caller true --allow-caller-asserted false", false, true},
		{"codex_home", "", false, true},
		{"skip", "--product both --skip-agent-notify", false, false},
		{"configure_failed", "--product both", false, true},
		{"last_failed", "--product both --agent-notify --navigation none --allow-unknown-caller true --allow-caller-asserted false", true, false},
		{"bad_none", "--product both --agent-notify --navigation none", false, false},
		{"bad_app", "--product both --agent-notify --app /Applications/../Codex.app --team-id TEAM123456 --allow-unknown-caller true --allow-caller-asserted false", false, false},
		{"bad_route", "--product both --agent-notify --navigation invalid", false, false},
		{"bad_alias", "--product both --configure-notifications", false, false},
		{"bad_conflict", "--product both --agent-notify --skip-agent-notify", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
			t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			binary := filepath.Join(home, "fake-binary")
			helper := "#!/bin/sh\nif [ \"$1\" = --help ] || [ \"$1\" = help ]; then printf '%s\\n' 'setup-notifications' '--skip-agent-notify'; exit 0; fi\nprintf '%s\\n' \"$*\" >> \"$HOME/calls\"\n"
			if test.name == "configure_failed" {
				helper += "exit 1\n"
			}
			if err := os.WriteFile(binary, []byte(helper), 0700); err != nil {
				t.Fatal(err)
			}
			script := prefix + `
print_header() { :; }
abort_if_wsl_environment() { :; }
check_prerequisites() { :; }
detect_platform() { :; }
install_cleanup_traps() { :; }
resolve_bootstrap_release() { :; }
stage_config_helper() { :; }
stage_historical_baselines() { :; }
config_preflight() { :; }
initialize_config() { :; }
install_claude() { echo claude >> "$HOME/installs"; PLUGIN_ROOT="$HOME/bundle"; }
install_codex() { echo codex >> "$HOME/installs"; CONFIGURE_BINARY="$HOME/fake-binary"; return `
			if test.fail {
				script += "1"
			} else {
				script += "0"
			}
			args := test.args
			if test.name == "codex_home" {
				args = "--product both --codex-home " + home
			}
			script += "; }\nmain " + args + "\n"
			command := exec.Command("bash", "-c", script)
			command.Dir = home
			output, err := command.CombinedOutput()
			if (test.fail || test.name == "configure_failed" || strings.HasPrefix(test.name, "bad_")) != (err != nil) {
				t.Fatalf("%v: %s", err, output)
			}
			calls, _ := os.ReadFile(filepath.Join(home, "calls"))
			if test.wantConfigure {
				if test.name == "codex_home" {
					if !strings.Contains(string(calls), "--codex-home "+home) || !strings.Contains(string(calls), "setup-notifications configure --provider both") || !strings.Contains(string(calls), "--navigation none --allow-unknown-caller true --allow-caller-asserted false") {
						t.Fatal(string(calls))
					}
				} else {
					want := "setup-notifications configure --provider both --navigation none --allow-unknown-caller true --allow-caller-asserted false"
					if strings.Count(string(calls), want) != 1 {
						t.Fatal(string(calls))
					}
				}
			} else if len(calls) != 0 {
				t.Fatal("unexpected configure", string(calls))
			}
			if test.name == "configure_failed" {
				if !strings.Contains(string(output), "Agent-notify setup failed") {
					t.Fatal("missing configure warning", string(output))
				}
				installs, err := os.ReadFile(filepath.Join(home, "installs"))
				if err != nil || !strings.Contains(string(installs), "claude") || !strings.Contains(string(installs), "codex") {
					t.Fatal("configure failure rolled back install", err, string(installs))
				}
			}
			if strings.HasPrefix(test.name, "bad_") {
				if _, err := os.Stat(filepath.Join(home, "installs")); !os.IsNotExist(err) {
					t.Fatal("bad input installed")
				}
			}
		})
	}
}

func TestNotificationBootstrapWizard(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(notificationRepoRoot(t), "bin", "bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.TrimSuffix(strings.TrimSpace(string(source)), `main "$@"`)
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	claudeHome := filepath.Join(home, ".claude")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_HOME", "")
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("title = 'keep'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", "codex"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	binary := filepath.Join(home, "fake-binary")
	helper := "#!/bin/sh\nif [ \"$1\" = --help ] || [ \"$1\" = help ]; then printf '%s\\n' 'setup-notifications wizard' 'setup-notifications' '--skip-agent-notify'; exit 0; fi\nprintf '%s\\n' \"$*\" >> \"$HOME/calls\"\n"
	if err := os.WriteFile(binary, []byte(helper), 0700); err != nil {
		t.Fatal(err)
	}
	script := prefix + `
print_header() { :; }
abort_if_wsl_environment() { :; }
check_prerequisites() { :; }
detect_platform() { :; }
install_cleanup_traps() { :; }
resolve_bootstrap_release() { :; }
stage_config_helper() { :; }
stage_historical_baselines() { :; }
config_preflight() { :; }
initialize_config() { :; }
install_claude() {
  echo claude >> "$HOME/installs"
  PLUGIN_ROOT="$HOME/bundle"
  mkdir -p "$PLUGIN_ROOT/portable-package"
  printf '%s\n' '{"name":"agent-notify","version":"1.0.0"}' > "$PLUGIN_ROOT/portable-package/plugin.json"
}
install_codex() { echo codex >> "$HOME/installs"; CONFIGURE_BINARY="$HOME/fake-binary"; return 0; }
main --product both
`
	command := exec.Command("bash", "-c", script)
	command.Dir = home
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	calls, err := os.ReadFile(filepath.Join(home, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(calls)
	if !strings.Contains(body, "setup-notifications configure --provider both --navigation none --allow-unknown-caller true --allow-caller-asserted false") {
		t.Fatal("wizard must enable accepted policy", body)
	}
	if strings.Index(body, "setup-notifications configure") > strings.Index(body, "setup-notifications wizard") {
		t.Fatal("policy must precede portable registration", body)
	}
	if !strings.Contains(body, "setup-notifications wizard --action install --agents claude,codex --hooks false --agent-notify true --yes") {
		t.Fatal(body)
	}
	if !strings.Contains(body, "--package "+filepath.Join(home, "bundle", "portable-package")) {
		t.Fatal(body)
	}
	if !strings.Contains(body, "--claude-executable "+filepath.Join(binDir, "claude")) || !strings.Contains(body, "--codex-executable "+filepath.Join(binDir, "codex")) {
		t.Fatal(body)
	}
	if !strings.Contains(body, "--codex-home "+codexHome) {
		t.Fatal(body)
	}
	if !strings.Contains(body, "--claude-config "+claudeHome) {
		t.Fatal(body)
	}
	if strings.Contains(body, "--mcp-config") || strings.Contains(body, "--claude-mcp-config") {
		t.Fatal("bootstrap must not invent MCP config paths", body)
	}
}

func TestNotificationBootstrapWizardRetryQuotesCustomRoots(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(notificationRepoRoot(t), "bin", "bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.TrimSuffix(strings.TrimSpace(string(source)), `main "$@"`)
	base := t.TempDir()
	home := filepath.Join(base, "home space")
	bundle := filepath.Join(home, "bundle space")
	codexHome := filepath.Join(home, "codex home")
	claudeConfig := filepath.Join(home, "claude config")
	for _, dir := range []string{home, bundle, filepath.Join(bundle, "portable-package"), codexHome, claudeConfig, filepath.Join(home, "bin"), filepath.Join(home, "xdg")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bundle, "portable-package", "plugin.json"), []byte(`{"name":"agent-notify"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("title = 'keep'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfig)
	binDir := filepath.Join(home, "bin")
	for _, name := range []string{"claude", "codex"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	binary := filepath.Join(home, "fake binary")
	helper := "#!/bin/sh\nif [ \"$1\" = --help ] || [ \"$1\" = help ]; then printf '%s\\n' 'setup-notifications wizard' 'setup-notifications' '--skip-agent-notify'; exit 0; fi\nprintf '%s\\n' \"$*\" >> \"$HOME/calls\"\n[ \"$2\" = configure ] && exit 0\nexit 1\n"
	if err := os.WriteFile(binary, []byte(helper), 0700); err != nil {
		t.Fatal(err)
	}
	script := prefix + `
print_header() { :; }
abort_if_wsl_environment() { :; }
check_prerequisites() { :; }
detect_platform() { :; }
install_cleanup_traps() { :; }
resolve_bootstrap_release() { :; }
stage_config_helper() { :; }
stage_historical_baselines() { :; }
config_preflight() { :; }
initialize_config() { :; }
install_claude() { echo claude >> "$HOME/installs"; PLUGIN_ROOT="$HOME/bundle space"; }
install_codex() { echo codex >> "$HOME/installs"; CONFIGURE_BINARY="$HOME/fake binary"; return 0; }
main --product both --codex-home "$HOME/codex home"
`
	command := exec.Command("bash", "-c", script)
	command.Dir = home
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("expected wizard failure: %s", output)
	}
	if !strings.Contains(string(output), "Agent-notify setup failed") {
		t.Fatal("missing configure warning", string(output))
	}
	argv := retryArgv(t, string(output), "setup-notifications wizard")
	if argv[0] != binary {
		t.Fatalf("binary: %#v", argv)
	}
	if flagValue(argv, "--package") != filepath.Join(bundle, "portable-package") {
		t.Fatalf("package: %#v", argv)
	}
	if flagValue(argv, "--plugin-root") != bundle {
		t.Fatalf("plugin-root: %#v", argv)
	}
	if flagValue(argv, "--helper") != binary {
		t.Fatalf("helper: %#v", argv)
	}
	if flagValue(argv, "--claude-config") != claudeConfig {
		t.Fatalf("claude-config: %#v", argv)
	}
	if flagValue(argv, "--codex-home") != codexHome {
		t.Fatalf("codex-home: %#v", argv)
	}
	if flagValue(argv, "--mcp-config") != "" || flagValue(argv, "--claude-mcp-config") != "" {
		t.Fatalf("invented mcp-config: %#v", argv)
	}
}

func TestNotificationBootstrapConfigureRetryQuotesCustomRoots(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(notificationRepoRoot(t), "bin", "bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.TrimSuffix(strings.TrimSpace(string(source)), `main "$@"`)
	base := t.TempDir()
	home := filepath.Join(base, "home space")
	codexHome := filepath.Join(home, "codex home")
	for _, dir := range []string{home, codexHome, filepath.Join(home, "xdg")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	binary := filepath.Join(home, "fake binary")
	helper := "#!/bin/sh\nif [ \"$1\" = --help ] || [ \"$1\" = help ]; then printf '%s\\n' 'setup-notifications' '--skip-agent-notify'; exit 0; fi\nprintf '%s\\n' \"$*\" >> \"$HOME/calls\"\nexit 1\n"
	if err := os.WriteFile(binary, []byte(helper), 0700); err != nil {
		t.Fatal(err)
	}
	script := prefix + `
print_header() { :; }
abort_if_wsl_environment() { :; }
check_prerequisites() { :; }
detect_platform() { :; }
install_cleanup_traps() { :; }
resolve_bootstrap_release() { :; }
stage_config_helper() { :; }
stage_historical_baselines() { :; }
config_preflight() { :; }
initialize_config() { :; }
install_claude() { echo claude >> "$HOME/installs"; PLUGIN_ROOT="$HOME/bundle space"; }
install_codex() { echo codex >> "$HOME/installs"; CONFIGURE_BINARY="$HOME/fake binary"; return 0; }
main --product both --codex-home "$HOME/codex home"
`
	command := exec.Command("bash", "-c", script)
	command.Dir = home
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("expected configure failure: %s", output)
	}
	argv := retryArgv(t, string(output), "setup-notifications configure")
	if argv[0] != binary {
		t.Fatalf("binary: %#v", argv)
	}
	if flagValue(argv, "--codex-home") != codexHome {
		t.Fatalf("codex-home: %#v", argv)
	}
	if flagValue(argv, "--provider") != "both" {
		t.Fatalf("provider: %#v", argv)
	}
}

func TestNotificationBootstrapWizardReleaseZip(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(notificationRepoRoot(t), "bin", "bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.TrimSuffix(strings.TrimSpace(string(source)), `main "$@"`)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", "codex"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	binary := filepath.Join(home, "fake-binary")
	helper := "#!/bin/sh\nif [ \"$1\" = --help ] || [ \"$1\" = help ]; then printf '%s\\n' 'setup-notifications wizard' 'setup-notifications' '--skip-agent-notify'; exit 0; fi\nprintf '%s\\n' \"$*\" >> \"$HOME/calls\"\n"
	if err := os.WriteFile(binary, []byte(helper), 0700); err != nil {
		t.Fatal(err)
	}
	asset := fmt.Sprintf("agent-notify-portable-%s-%s.zip", runtime.GOOS, runtime.GOARCH)
	release := filepath.Join(home, "release")
	if err := os.Mkdir(release, 0700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("portable-zip-fixture")
	if err := os.WriteFile(filepath.Join(release, asset), payload, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	checksums := hex.EncodeToString(sum[:]) + "  " + asset + "\n"
	if err := os.WriteFile(filepath.Join(release, "checksums.txt"), []byte(checksums), 0600); err != nil {
		t.Fatal(err)
	}
	script := prefix + `
print_header() { :; }
abort_if_wsl_environment() { :; }
check_prerequisites() { :; }
detect_platform() { :; }
install_cleanup_traps() { :; }
resolve_bootstrap_release() { BOOTSTRAP_TAG=v1.43.0; }
stage_config_helper() { :; }
stage_historical_baselines() { :; }
config_preflight() { :; }
initialize_config() { :; }
fetch_bootstrap_file() {
  case "$1" in
    *checksums.txt) cp "$HOME/release/checksums.txt" "$2" ;;
    *agent-notify-portable-*) cp "$HOME/release/$(basename "$1")" "$2" ;;
    *) echo "unexpected fetch $1" >&2; return 1 ;;
  esac
}
install_claude() { echo claude >> "$HOME/installs"; PLUGIN_ROOT="$HOME/bundle"; mkdir -p "$PLUGIN_ROOT"; }
install_codex() { echo codex >> "$HOME/installs"; CONFIGURE_BINARY="$HOME/fake-binary"; return 0; }
main --product both
`
	command := exec.Command("bash", "-c", script)
	command.Dir = home
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	calls, err := os.ReadFile(filepath.Join(home, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(calls)
	if !strings.Contains(body, "setup-notifications configure --provider both --navigation none --allow-unknown-caller true --allow-caller-asserted false") {
		t.Fatal("wizard must enable accepted policy", body)
	}
	if strings.Index(body, "setup-notifications configure") > strings.Index(body, "setup-notifications wizard") {
		t.Fatal("policy must precede portable registration", body)
	}
	if !strings.Contains(body, "--package ") || !strings.Contains(body, asset) {
		t.Fatal(body)
	}
	if strings.Contains(body, "portable-package") {
		t.Fatal("used git template instead of release zip", body)
	}
}

func TestNotificationBootstrapWizardMissingPortable(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(notificationRepoRoot(t), "bin", "bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.TrimSuffix(strings.TrimSpace(string(source)), `main "$@"`)
	for _, test := range []struct {
		name, args string
		wantErr    bool
	}{
		{name: "auto", args: "--product both"},
		{name: "explicit", args: "--product both --agent-notify --navigation none --allow-unknown-caller true --allow-caller-asserted false", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
			t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			binary := filepath.Join(home, "fake-binary")
			helper := "#!/bin/sh\nif [ \"$1\" = --help ] || [ \"$1\" = help ]; then printf '%s\\n' 'setup-notifications wizard' 'setup-notifications' '--skip-agent-notify'; exit 0; fi\nprintf '%s\\n' \"$*\" >> \"$HOME/calls\"\n"
			if err := os.WriteFile(binary, []byte(helper), 0700); err != nil {
				t.Fatal(err)
			}
			script := prefix + `
print_header() { :; }
abort_if_wsl_environment() { :; }
check_prerequisites() { :; }
detect_platform() { :; }
install_cleanup_traps() { :; }
resolve_bootstrap_release() { BOOTSTRAP_TAG=v9.9.9; }
stage_config_helper() { :; }
stage_historical_baselines() { :; }
config_preflight() { :; }
initialize_config() { :; }
fetch_bootstrap_file() { echo curl: 404 >&2; return 1; }
install_claude() { echo claude >> "$HOME/installs"; PLUGIN_ROOT="$HOME/bundle"; mkdir -p "$PLUGIN_ROOT"; }
install_codex() { echo codex >> "$HOME/installs"; CONFIGURE_BINARY="$HOME/fake-binary"; return 0; }
main ` + test.args + `
`
			command := exec.Command("bash", "-c", script)
			command.Dir = home
			output, err := command.CombinedOutput()
			if test.wantErr != (err != nil) {
				t.Fatalf("%v: %s", err, output)
			}
			if !strings.Contains(string(output), "portable package is missing") {
				t.Fatal(string(output))
			}
			calls, _ := os.ReadFile(filepath.Join(home, "calls"))
			if len(calls) != 0 {
				t.Fatal("missing portable still invoked wizard", string(calls))
			}
			installs, err := os.ReadFile(filepath.Join(home, "installs"))
			if err != nil || !strings.Contains(string(installs), "claude") || !strings.Contains(string(installs), "codex") {
				t.Fatal("missing portable rolled back install", err, string(installs))
			}
		})
	}
}

func TestNotificationInitOfflineBranch(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(notificationRepoRoot(t), "commands", "init.md"))
	if err != nil {
		t.Fatal(err)
	}
	blocks := strings.Split(string(source), "```bash\n")
	body := ""
	for _, block := range blocks[1:] {
		chunk := strings.SplitN(block, "```", 2)[0]
		if strings.Contains(chunk, "setup-notifications configure") {
			body = chunk
			break
		}
	}
	if body == "" {
		t.Fatal("missing init configure script")
	}
	for _, test := range []struct {
		name                      string
		args                      []string
		failHelper, fail, install bool
	}{
		{name: "default", install: true},
		{name: "skip", args: []string{"--skip-agent-notify"}, install: true},
		{name: "configure", args: []string{"--agent-notify", "--navigation", "none", "--allow-unknown-caller", "true", "--allow-caller-asserted", "false"}, install: true},
		{name: "failed", failHelper: true, fail: true, install: true},
		{name: "incomplete_none", args: []string{"--agent-notify", "--navigation", "none"}, fail: true},
		{name: "bad_alias", args: []string{"--configure-notifications"}, fail: true},
		{name: "bad_route", args: []string{"--navigation", "invalid"}, fail: true},
		{name: "bad_conflict", args: []string{"--agent-notify", "--skip-agent-notify"}, fail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("TMPDIR", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
			t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
			bundle := filepath.Join(home, "bundle")
			t.Setenv("CLAUDE_PLUGIN_ROOT", bundle)
			if err := os.MkdirAll(filepath.Join(bundle, "bin"), 0700); err != nil {
				t.Fatal(err)
			}
			helper := "#!/bin/sh\nif [ \"$1\" = --help ] || [ \"$1\" = help ]; then exit 0; fi\nprintf '%s\\n' \"$*\" >> \"$HOME/calls\"\n"
			if test.failHelper {
				helper += "exit 1\n"
			}
			if err := os.WriteFile(filepath.Join(bundle, "bin", "claude-notifications"), []byte(helper), 0700); err != nil {
				t.Fatal(err)
			}
			script := `curl() { printf '#!/bin/sh\necho installed >> "$HOME/installs"\n' > "$4"; }
` + body
			args := append([]string{"-c", script, "init"}, test.args...)
			command := exec.Command("bash", args...)
			command.Dir = home
			output, err := command.CombinedOutput()
			if test.fail != (err != nil) {
				t.Fatalf("%v: %s", err, output)
			}
			if !test.install {
				if _, err := os.Stat(filepath.Join(home, "installs")); !os.IsNotExist(err) {
					t.Fatal("bad input installed", string(output))
				}
				return
			}
			installs, err := os.ReadFile(filepath.Join(home, "installs"))
			if err != nil || strings.TrimSpace(string(installs)) != "installed" {
				t.Fatal("installer not exercised", err)
			}
			calls, _ := os.ReadFile(filepath.Join(home, "calls"))
			if test.name == "skip" {
				if len(calls) != 0 {
					t.Fatal("skip configured", string(calls))
				}
				return
			}
			want := "setup-notifications configure --provider claude --navigation none --allow-unknown-caller true --allow-caller-asserted false"
			if strings.TrimSpace(string(calls)) != want {
				t.Fatal(string(calls))
			}
			if test.failHelper && !strings.Contains(string(output), "agent-notify setup failed") {
				t.Fatal("missing configure warning", string(output))
			}
		})
	}
}

func TestNotificationInitWizard(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(notificationRepoRoot(t), "commands", "init.md"))
	if err != nil {
		t.Fatal(err)
	}
	blocks := strings.Split(string(source), "```bash\n")
	body := ""
	for _, block := range blocks[1:] {
		chunk := strings.SplitN(block, "```", 2)[0]
		if strings.Contains(chunk, "setup-notifications wizard") {
			body = chunk
			break
		}
	}
	if body == "" {
		t.Fatal("missing init wizard script")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TMPDIR", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	bundle := filepath.Join(home, "bundle")
	t.Setenv("CLAUDE_PLUGIN_ROOT", bundle)
	if err := os.MkdirAll(filepath.Join(bundle, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bundle, "portable-package"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "portable-package", "plugin.json"), []byte(`{"name":"agent-notify"}`), 0600); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	helper := "#!/bin/sh\nif [ \"$1\" = --help ] || [ \"$1\" = help ]; then printf '%s\\n' 'setup-notifications wizard'; exit 0; fi\nprintf '%s\\n' \"$*\" >> \"$HOME/calls\"\n"
	if err := os.WriteFile(filepath.Join(bundle, "bin", "claude-notifications"), []byte(helper), 0700); err != nil {
		t.Fatal(err)
	}
	script := `curl() { printf '#!/bin/sh\necho installed >> "$HOME/installs"\n' > "$4"; }
` + body
	command := exec.Command("bash", "-c", script, "init")
	command.Dir = home
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	calls, err := os.ReadFile(filepath.Join(home, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(calls)
	if strings.Contains(got, "setup-notifications configure") {
		t.Fatal("wizard path called configure", got)
	}
	if !strings.Contains(got, "setup-notifications wizard --action install --agents claude --hooks false --agent-notify true --yes") {
		t.Fatal(got)
	}
	if !strings.Contains(got, "--package "+filepath.Join(bundle, "portable-package")) {
		t.Fatal(got)
	}
	if strings.Contains(got, "--mcp-config") || strings.Contains(got, "--claude-mcp-config") {
		t.Fatal("init must not invent MCP config paths", got)
	}
}

func TestNotificationInitWizardRetryQuotesCustomRoots(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(notificationRepoRoot(t), "commands", "init.md"))
	if err != nil {
		t.Fatal(err)
	}
	blocks := strings.Split(string(source), "```bash\n")
	body := ""
	for _, block := range blocks[1:] {
		chunk := strings.SplitN(block, "```", 2)[0]
		if strings.Contains(chunk, "setup-notifications wizard") {
			body = chunk
			break
		}
	}
	if body == "" {
		t.Fatal("missing init wizard script")
	}
	base := t.TempDir()
	home := filepath.Join(base, "home space")
	bundle := filepath.Join(home, "plugin root")
	claudeConfig := filepath.Join(home, "claude config")
	for _, dir := range []string{
		home,
		filepath.Join(bundle, "bin"),
		filepath.Join(bundle, "portable-package"),
		filepath.Join(home, "bin"),
		filepath.Join(home, "xdg"),
		claudeConfig,
	} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bundle, "portable-package", "plugin.json"), []byte(`{"name":"agent-notify"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("TMPDIR", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex home"))
	t.Setenv("CLAUDE_PLUGIN_ROOT", bundle)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfig)
	if err := os.WriteFile(filepath.Join(home, "bin", "claude"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(home, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	helper := "#!/bin/sh\nif [ \"$1\" = --help ] || [ \"$1\" = help ]; then printf '%s\\n' 'setup-notifications wizard'; exit 0; fi\nprintf '%s\\n' \"$*\" >> \"$HOME/calls\"\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bundle, "bin", "claude-notifications"), []byte(helper), 0700); err != nil {
		t.Fatal(err)
	}
	script := `curl() { printf '#!/bin/sh\necho installed >> "$HOME/installs"\n' > "$4"; }
` + body
	command := exec.Command("bash", "-c", script, "init")
	command.Dir = home
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("expected wizard failure: %s", output)
	}
	if !strings.Contains(string(output), "agent-notify setup failed") {
		t.Fatal("missing configure warning", string(output))
	}
	argv := retryArgv(t, string(output), "setup-notifications wizard")
	notifyBin := filepath.Join(bundle, "bin", "claude-notifications")
	if argv[0] != notifyBin {
		t.Fatalf("binary: %#v", argv)
	}
	if flagValue(argv, "--package") != filepath.Join(bundle, "portable-package") {
		t.Fatalf("package: %#v", argv)
	}
	if flagValue(argv, "--plugin-root") != bundle {
		t.Fatalf("plugin-root: %#v", argv)
	}
	if flagValue(argv, "--helper") != notifyBin {
		t.Fatalf("helper: %#v", argv)
	}
	if flagValue(argv, "--claude-config") != claudeConfig {
		t.Fatalf("claude-config: %#v", argv)
	}
	if flagValue(argv, "--mcp-config") != "" || flagValue(argv, "--claude-mcp-config") != "" {
		t.Fatalf("invented mcp-config: %#v", argv)
	}
}

// Only transport/acquisition is inert; installer shell, setup adapter and kernel
// execute their production code in fresh HOME/XDG/CODEX_HOME directories.
func TestNotificationBootstrapRealInstaller(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux inert assets; Darwin native qualification belongs to controller")
	}
	home := t.TempDir()
	for key, value := range map[string]string{"HOME": home, "TMPDIR": home, "XDG_CONFIG_HOME": filepath.Join(home, "xdg"), "CODEX_HOME": filepath.Join(home, "codex"), "CLAUDE_CONFIG_DIR": ""} {
		t.Setenv(key, value)
	}
	t.Setenv("NOTIFICATION_SHELL_HELPER", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOTIFICATION_TEST_EXECUTABLE", executable)
	bundle := filepath.Join(home, "source")
	for _, dir := range []string{"bin", ".claude-plugin", "config"} {
		if err := os.MkdirAll(filepath.Join(bundle, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	version := config.ConsumerVersion
	write(filepath.Join(bundle, ".claude-plugin", "plugin.json"), fmt.Sprintf(`{"name":"claude-notifications-go","version":%q}`, version))
	packagedConfig, err := os.ReadFile(filepath.Join(notificationRepoRoot(t), "config", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(bundle, "config", "config.json"), string(packagedConfig))
	for _, name := range []string{"codex-hook-wrapper.sh", "codex-hook-wrapper.cmd"} {
		write(filepath.Join(bundle, "bin", name), "inert hook")
	}
	asset := filepath.Join(home, "asset")
	write(asset, "#!/bin/sh\n# agent-notifications-managed-writer-protocol-v1\nexec \"$NOTIFICATION_TEST_EXECUTABLE\" -test.run=^TestNotificationShellHelper$ -- \"$@\"\n")
	t.Setenv("NOTIFICATION_TEST_ASSET", asset)
	installer, err := os.ReadFile(filepath.Join(notificationRepoRoot(t), "bin", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	installerPrefix := strings.TrimSuffix(strings.TrimSpace(string(installer)), `main "$@"`)
	stagedInstaller := installerPrefix + `
abort_if_wsl_environment() { :; }
check_required_tools() { :; }
check_write_permissions() { :; }
acquire_lock() { :; }
pin_release_urls() { :; }
download_and_verify_binary() { cp "$NOTIFICATION_TEST_ASSET" "$BINARY_PATH"; chmod +x "$BINARY_PATH"; }
download_utilities() { :; }
configure_windows_native_hooks() { :; }
create_claude_notifications_app() { :; }
setup_iterm2_venv() { :; }
install_linux_notification_desktop_entry() { :; }
install_gnome_activate_window_extension() { :; }
main "$@"
`
	write(filepath.Join(bundle, "bin", "install.sh"), stagedInstaller)
	configStage := filepath.Join(home, "config-stage")
	if err := os.Mkdir(configStage, 0700); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(configStage, "install.sh"), stagedInstaller)
	archiveName := "agent-notifications-" + version
	archiveRoot := filepath.Join(home, "archive", archiveName)
	if err := os.MkdirAll(archiveRoot, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("cp", "-R", bundle+"/.", archiveRoot)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("copy archive: %v: %s", err, output)
	}
	tarball := filepath.Join(home, "source.tar.gz")
	cmd = exec.Command("tar", "-czf", tarball, "-C", filepath.Join(home, "archive"), archiveName)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tar: %v: %s", err, output)
	}
	t.Setenv("NOTIFICATION_TEST_SOURCE_TAR", tarball)
	t.Setenv("NOTIFICATION_TEST_CONFIG_STAGE", configStage)
	bootstrap, err := os.ReadFile(filepath.Join(notificationRepoRoot(t), "bin", "bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.TrimSuffix(strings.TrimSpace(string(bootstrap)), `main "$@"`)
	cmd = exec.Command("bash", "-c", prefix+`
_CONFIG_STAGE="$NOTIFICATION_TEST_CONFIG_STAGE"
_CONFIG_HELPER="$NOTIFICATION_TEST_ASSET"
chmod +x "$_CONFIG_HELPER"
BOOTSTRAP_TAG=v`+version+`
PRODUCT=codex
CONFIGURE_NOTIFICATIONS=true
fetch_bootstrap_file() { cp "$NOTIFICATION_TEST_SOURCE_TAR" "$2"; }
install_cleanup_traps
install_codex || exit 1
case "$CONFIGURE_BINARY" in "$CODEX_HOME/claude-notifications-go/bin/claude-notifications") ;; *) exit 2 ;; esac
[ -z "$_BOOTSTRAP_STAGE" ] || exit 3
"$CONFIGURE_BINARY" --version
`)
	cmd.Dir = home
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	control, err := installruntime.ControlRoot()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(home, "codex", "claude-notifications-go")
	if snapshot.Ledger.RuntimeRoot != installed || len(snapshot.Ledger.Consumers) != 1 {
		t.Fatal(snapshot.Ledger)
	}
	for _, consumer := range snapshot.Ledger.Consumers {
		if consumer.RuntimeRoot != installed {
			t.Fatal("temporary consumer", consumer)
		}
	}
}

func TestNotificationShellHelper(t *testing.T) {
	if os.Getenv("NOTIFICATION_SHELL_HELPER") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) < 2 {
		os.Exit(2)
	}
	args = args[1:]
	switch args[0] {
	case "--version":
		fmt.Println("claude-notifications v" + config.ConsumerVersion)
	case "setup-codex":
		runSetupCodex(args[1:])
	case "config":
		os.Exit(configCommand(args[1:], os.Stdin, os.Stdout, os.Stderr))
	case "internal-install-runtime":
		if err := installRuntime(args[1:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func TestNotificationAcquisitionRefusesExistingOutput(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(notificationRepoRoot(t), "bin", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.TrimSuffix(strings.TrimSpace(string(source)), `main "$@"`)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("INSTALL_TARGET_DIR", home)
	for _, kind := range []string{"runtime", "empty", "symlink", "relative", "unclean", "missing"} {
		t.Run(kind, func(t *testing.T) {
			output := filepath.Join(home, kind)
			switch kind {
			case "runtime", "empty":
				if err := os.Mkdir(output, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(home, output); err != nil {
					t.Fatal(err)
				}
			case "relative":
				output = "relative"
			case "unclean":
				output = home + "/../unclean"
			case "missing":
				output = ""
			}
			sentinel := filepath.Join(home, "claude-notifications")
			if kind == "runtime" {
				sentinel = filepath.Join(output, "claude-notifications")
			}
			if err := os.WriteFile(sentinel, []byte("existing-runtime"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TEST_ACQUIRE_OUTPUT", output)
			cmd := exec.Command("bash", "-c", prefix+`
ACQUIRE_ONLY=true
ACQUIRE_OUTPUT="$TEST_ACQUIRE_OUTPUT"
CN_PRODUCT=codex
abort_if_wsl_environment() { :; }
check_required_tools() { :; }
pin_release_urls() { :; }
download_and_verify_binary() { echo download-must-not-run >&2; exit 99; }
main
`)
			cmd.Dir = home
			result, err := cmd.CombinedOutput()
			if err == nil || strings.Contains(string(result), "download-must-not-run") {
				t.Fatalf("%v: %s", err, result)
			}
			data, err := os.ReadFile(sentinel)
			if err != nil || string(data) != "existing-runtime" {
				t.Fatalf("overwritten: %s %v", data, err)
			}
		})
	}
}

func TestNotificationBootstrapWindowsLaunchers(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(notificationRepoRoot(t), "bin", "bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.TrimSuffix(strings.TrimSpace(string(source)), `main "$@"`)
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			home := t.TempDir()
			root := filepath.Join(home, "runtime space")
			if err := os.MkdirAll(filepath.Join(root, "bin"), 0700); err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(root, "bin", "claude-notifications-windows-"+arch+".exe")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo \"claude-notifications v1.42.0\"; exit 0; fi\nprintf '%s\\n' \"$*\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			script := prefix + `
uname() { case "$1" in -s) echo MINGW64_NT ;; -m) echo "$FIXTURE_ARCH" ;; esac; }
config_preflight() { :; }
fetch_bootstrap_file() { :; }
tar() { mkdir -p "$bundle/bin"; touch "$bundle/bin/install.sh"; }
get_manifest_version() { echo 1.42.0; }
install_runtime() { cp "$FIXTURE_ROOT/bin/"*.exe "$bundle/bin/"; }
cli_has_setup_codex_skip_agent_notify() { return 1; }
BOOTSTRAP_TAG=v1.42.0
TMPDIR="$FIXTURE_HOME"
CONFIGURE_ARGS=(--codex-home "$FIXTURE_HOME")
setup_marketplace() { :; }
install_plugin() { :; }
sync_marketplace_checkout() { :; }
find_plugin_root() { :; }
download_binary() { :; }
setup_iterm2_venv() { :; }
setup_codex_home="$FIXTURE_HOME"
mkdir -p "$setup_codex_home/claude-notifications-go"
cp -R "$FIXTURE_ROOT/bin" "$setup_codex_home/claude-notifications-go/"
PRODUCT=codex
install_codex || exit 1
"$CONFIGURE_BINARY" codex-launch || exit 1
PRODUCT=claude
PLUGIN_ROOT="$FIXTURE_ROOT"
install_claude || exit 1
"$CONFIGURE_BINARY" claude-launch || exit 1
`
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(), "HOME="+home, "FIXTURE_HOME="+home, "FIXTURE_ROOT="+root, "FIXTURE_ARCH="+arch)
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), "codex-launch") || !strings.Contains(string(out), "claude-launch") {
				t.Fatalf("native Windows launcher selection: %v\n%s", err, out)
			}
		})
	}
}
