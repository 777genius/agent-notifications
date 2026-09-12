//go:build linux || darwin

package main

import (
	"fmt"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func notificationRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("missing caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

func TestNotificationBootstrapOffline(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(notificationRepoRoot(t), "bin", "bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), `setup-codex --plugin-root "$bundle" --skip-agent-notify`) {
		t.Fatal("install_codex must keep setup-codex hooks-only so bootstrap configures once")
	}
	prefix := strings.TrimSuffix(strings.TrimSpace(string(source)), `main "$@"`)
	for _, test := range []struct {
		name, args    string
		fail          bool
		wantConfigure bool
	}{
		{"ordinary", "--product both", false, true},
		{"both", "--product both --agent-notify --navigation none", false, true},
		{"codex_home", "", false, true},
		{"skip", "--product both --skip-agent-notify", false, false},
		{"configure_failed", "--product both", false, true},
		{"last_failed", "--product both --agent-notify --navigation none", true, false},
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
			helper := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$HOME/calls\"\n"
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
			if (test.fail || strings.HasPrefix(test.name, "bad_")) != (err != nil) {
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
					if strings.Contains(args, "--navigation") {
						want = "setup-notifications configure --provider both --navigation none"
					}
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
		{name: "configure", args: []string{"--agent-notify", "--navigation", "none"}, install: true},
		{name: "failed", failHelper: true, install: true},
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
			helper := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$HOME/calls\"\n"
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
			for _, arg := range test.args {
				if arg == "--navigation" {
					want = "setup-notifications configure --provider claude --navigation none"
					break
				}
			}
			if strings.TrimSpace(string(calls)) != want {
				t.Fatal(string(calls))
			}
			if test.failHelper && !strings.Contains(string(output), "agent-notify setup failed") {
				t.Fatal("missing configure warning", string(output))
			}
		})
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
	write(filepath.Join(bundle, ".claude-plugin", "plugin.json"), `{"name":"claude-notifications-go","version":"1.42.0"}`)
	write(filepath.Join(bundle, "config", "config.json"), `{}`)
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
	write(filepath.Join(bundle, "bin", "install.sh"), installerPrefix+`
abort_if_wsl_environment() { :; }
check_required_tools() { :; }
check_write_permissions() { :; }
acquire_lock() { :; }
pin_release_urls() { :; }
download_and_verify_binary() { cp "$NOTIFICATION_TEST_ASSET" "$BINARY_PATH"; }
main "$@"
`)
	bootstrap, err := os.ReadFile(filepath.Join(notificationRepoRoot(t), "bin", "bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.TrimSuffix(strings.TrimSpace(string(bootstrap)), `main "$@"`)
	t.Setenv("NOTIFICATION_TEST_BUNDLE", bundle)
	cmd := exec.Command("bash", "-c", prefix+`
PLUGIN_ROOT="$NOTIFICATION_TEST_BUNDLE"
BOOTSTRAP_TAG=v1.42.0
PRODUCT=codex
CONFIGURE_NOTIFICATIONS=true
install_codex || exit 1
case "$CONFIGURE_BINARY" in "$CODEX_HOME/claude-notifications-go/bin/claude-notifications") ;; *) exit 2 ;; esac
rm -rf "$_BOOTSTRAP_STAGE"
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
		fmt.Println("claude-notifications v1.42.0")
	case "setup-codex":
		runSetupCodex(args[1:])
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
