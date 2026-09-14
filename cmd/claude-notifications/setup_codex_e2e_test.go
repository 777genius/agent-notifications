package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/777genius/agent-notifications/internal/testenv"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// This offline fixture never starts Codex or reads credentials. Runtime children
// receive a shared allowlist. Builds retain only explicit Go toolchain settings.
type setupE2E struct {
	root, home, bundle string
	env                []string
}

func e2eWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
}
func e2eRead(t *testing.T, path string) []byte {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func newSetupE2E(t *testing.T) setupE2E {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := setupE2E{root: root}
	f.home = filepath.Join(f.root, "home space")
	f.bundle = filepath.Join(f.root, "bundle space")
	f.env = testenv.Env(t, f.home)
	// Setup assertions use the conventional Codex installation location.
	for i, value := range f.env {
		if strings.HasPrefix(value, "CODEX_HOME=") {
			f.env[i] = "CODEX_HOME=" + filepath.Join(f.home, ".codex")
		}
	}

	for _, name := range []string{"hook-wrapper.sh", "codex-hook-wrapper.sh", "codex-hook-wrapper.cmd"} {
		e2eWrite(t, filepath.Join(f.bundle, "bin", name), e2eRead(t, filepath.Join(repoRoot(t), "bin", name)))
	}
	e2eWrite(t, filepath.Join(f.bundle, ".claude-plugin/plugin.json"), []byte(`{"name":"claude-notifications-go","version":"9.9.9"}`))
	for _, name := range []string{"settings.json", "claude-notifications-go/plugin-root", "claude-notifications-go/state.json", "claude-notifications-go/dedup.json"} {
		e2eWrite(t, filepath.Join(f.home, ".claude", name), []byte("synthetic Claude canary: "+name))
	}
	e2eWrite(t, filepath.Join(f.home, ".claude/claude-notifications-go/config.json"), []byte(`{"notifications":{"desktop":{"enabled":false},"webhook":{"enabled":false}}}`))
	return f
}
func (f setupE2E) run(t *testing.T, input, binary string, args ...string) (string, error) {
	t.Helper()
	return f.runAt(t, f.root, input, binary, args...)
}
func (f setupE2E) runAt(t *testing.T, dir, input, binary string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, binary, args...)
	configureE2EShell(c, binary, args)
	c.Dir = dir
	c.Env = f.env
	c.Stdin = strings.NewReader(input)
	c.WaitDelay = 2 * time.Second
	out, err := c.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("process timeout: %v", args)
	}
	return string(out), err
}

func TestSetupCodexE2EDocumentedRelativeBundle(t *testing.T) {
	bin := buildCLIBinary(t)
	f := newSetupE2E(t)
	before := e2eSnapshot(t, f.root)
	out, err := f.runAt(t, f.bundle, "", bin, "setup-codex", "--plugin-root", ".", "--dry-run")
	if err != nil || !strings.Contains(out, "dry run") {
		t.Fatalf("documented dry run: %v %s", err, out)
	}
	if !reflect.DeepEqual(before, e2eSnapshot(t, f.root)) {
		t.Fatal("dry run changed sandbox")
	}
	for i := 0; i < 2; i++ {
		out, err = f.runAt(t, f.bundle, "", bin, "setup-codex", "--plugin-root", ".", "--skip-agent-notify")
		if err != nil || !strings.Contains(out, "Codex notifications registered") {
			t.Fatalf("documented setup run %d: %v %s", i, err, out)
		}
	}
	hooks := e2eRead(t, filepath.Join(f.home, ".codex", "hooks.json"))
	if !strings.Contains(string(hooks), "codex-hook-wrapper") {
		t.Fatal("documented setup did not register hooks")
	}
	e2eRead(t, filepath.Join(f.home, ".codex", "claude-notifications-go", "bin", "codex-hook-wrapper.sh"))
}
func e2eSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, i os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(root, p)
		if i.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unexpected symlink: %s", p)
		}
		if i.IsDir() {
			out[rel] = "dir"
		} else {
			out[rel] = string(e2eRead(t, p))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestSetupCodexE2EReadOnlyAndMalformed(t *testing.T) {
	bin := buildCLIBinary(t)
	for _, mode := range []string{"--print", "--dry-run", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			f := newSetupE2E(t)
			args := []string{"setup-codex", "--plugin-root", f.bundle}
			if mode == "malformed" {
				e2eWrite(t, filepath.Join(f.home, ".codex/hooks.json"), []byte(`{"hooks": broken`))
			} else {
				args = append(args, mode)
			}
			before := e2eSnapshot(t, f.root)
			out, err := f.run(t, "", bin, args...)
			if mode == "malformed" {
				if err == nil || !strings.Contains(out, "setup-codex:") {
					t.Fatalf("expected diagnostic failure: %v %s", err, out)
				}
			} else if err != nil || out == "" {
				t.Fatalf("%v %s", err, out)
			}
			if !reflect.DeepEqual(before, e2eSnapshot(t, f.root)) {
				t.Fatal("read-only/failure path changed sandbox")
			}
		})
	}
}

func TestSetupCodexE2ERegistrationAndDelivery(t *testing.T) {
	bin := buildCLIBinary(t)
	f := newSetupE2E(t)
	received := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, e := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if e != nil {
			t.Error(e)
		}
		select {
		case received <- string(b):
		default:
			t.Error("unexpected extra delivery")
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	cfg := fmt.Sprintf(`{"notifications":{"desktop":{"enabled":false},"webhook":{"enabled":true,"preset":"slack","url":%q}}}`, srv.URL)
	e2eWrite(t, filepath.Join(f.home, ".claude/claude-notifications-go/config.json"), []byte(cfg))
	claudeBefore := e2eSnapshot(t, filepath.Join(f.home, ".claude"))
	foreign := `{"note":"foreign canary","hooks":{"Stop":[{"matcher":"foreign","extra":17,"hooks":[{"type":"command","command":"foreign-command-never-executed","timeout":7,"custom":true}]}]}}`
	hooksPath := filepath.Join(f.home, ".codex/hooks.json")
	e2eWrite(t, hooksPath, []byte(foreign))
	// Explicit fixture version shim: only the launcher's version probe is faked.
	// All hook invocations forward unchanged to the real built binary. This is
	// registration/launcher proof, NOT proof that the shipped 1.41.0 passes 1.42.0.
	shimSource := `package main
import("fmt";"os";"os/exec")
func main(){
if os.Getenv("AGENT_NOTIFICATIONS_WRITER_PROTOCOL_PROBE")=="1"{fmt.Print("agent-notifications-managed-writer-protocol-v1");return}
if len(os.Args)==2&&os.Args[1]=="version"{fmt.Println("claude-notifications v9.9.9");return}
c:=exec.Command(os.Getenv("E2E_REAL_BINARY"),os.Args[1:]...);c.Stdin=os.Stdin;c.Stdout=os.Stdout;c.Stderr=os.Stderr;c.Env=os.Environ();if c.Run()!=nil{os.Exit(1)}
}`
	src := filepath.Join(f.root, "version_fixture.go")
	e2eWrite(t, src, []byte(shimSource))
	name := "claude-notifications"
	if runtime.GOOS == "windows" {
		name = "claude-notifications-windows-" + runtime.GOARCH + ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "go", "build", "-p", "2", "-buildvcs=false", "-o", filepath.Join(f.bundle, "bin", name), src)
	c.Env = testenv.Build(t, filepath.Join(f.root, "build-home"))
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("fixture build: %v %s", err, out)
	}
	f.env = append(f.env, "E2E_REAL_BINARY="+bin)
	for i := 0; i < 2; i++ {
		if out, err := f.run(t, "", bin, "setup-codex", "--plugin-root", f.bundle, "--skip-agent-notify"); err != nil {
			t.Fatalf("setup: %v %s", err, out)
		}
		if i == 0 {
			e2eWrite(t, filepath.Join(f.root, "first-hooks.json"), e2eRead(t, hooksPath))
		} else if string(e2eRead(t, hooksPath)) != string(e2eRead(t, filepath.Join(f.root, "first-hooks.json"))) {
			t.Fatal("registration not idempotent")
		}
	}
	// Store initialization may publish a persistent coordination sidecar.
	// Existing Claude data must remain byte-identical; hook invocations below
	// must not add even sidecars after setup has finished.
	afterSetup := e2eSnapshot(t, filepath.Join(f.home, ".claude"))
	lockName := filepath.Join("claude-notifications-go", "config.json.lock")
	if _, existed := claudeBefore[lockName]; !existed {
		if value, exists := afterSetup[lockName]; exists && value != "" {
			t.Fatal("unexpected lock contents")
		}
		delete(afterSetup, lockName)
	}
	if !reflect.DeepEqual(claudeBefore, afterSetup) {
		t.Fatal("setup changed existing Claude data")
	}
	claudeBefore = e2eSnapshot(t, filepath.Join(f.home, ".claude"))
	var doc struct {
		Note  string `json:"note"`
		Hooks map[string][]struct {
			Matcher string           `json:"matcher"`
			Extra   int              `json:"extra"`
			Hooks   []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(e2eRead(t, hooksPath), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Note != "foreign canary" || len(doc.Hooks["Stop"]) != 2 {
		t.Fatalf("foreign metadata/group lost: %+v", doc)
	}
	g := doc.Hooks["Stop"][0]
	if g.Matcher != "foreign" || g.Extra != 17 || len(g.Hooks) != 1 || g.Hooks[0]["command"] != "foreign-command-never-executed" || g.Hooks[0]["custom"] != true || g.Hooks[0]["timeout"] != float64(7) {
		t.Fatal("foreign handler mutated")
	}
	for _, event := range []string{"Stop", "PermissionRequest", "PreToolUse", "SubagentStop"} {
		want := 1
		if event == "Stop" {
			want = 2
		}
		if len(doc.Hooks[event]) != want {
			t.Fatalf("%s registration count", event)
		}
	}
	commandKey := "command"
	if runtime.GOOS == "windows" {
		commandKey = "commandWindows"
	}
	command, ok := doc.Hooks["Stop"][1].Hooks[0][commandKey].(string)
	if !ok {
		t.Fatal("missing platform command")
	}
	for _, mode := range []string{"generated-launcher-version-fixture", "real-pipeline"} {
		marker := "sandbox-" + mode
		payload, _ := json.Marshal(map[string]any{"session_id": marker, "turn_id": marker, "cwd": f.root, "transcript_path": filepath.Join(f.root, "absent.jsonl"), "hook_event_name": "Stop", "last_assistant_message": "Completed " + marker, "stop_hook_active": false})
		var out string
		var err error
		if mode == "real-pipeline" {
			out, err = f.run(t, string(payload), bin, "handle-hook", "Stop", "--product", "codex")
		} else if runtime.GOOS == "windows" {
			out, err = f.run(t, string(payload), "cmd.exe", "/d", "/s", "/c", command)
		} else {
			out, err = f.run(t, string(payload), "sh", "-c", command)
		}
		if err != nil || out != "" {
			t.Fatalf("%s: %v %q", mode, err, out)
		}
		select {
		case body := <-received:
			if !strings.Contains(body, marker) {
				t.Fatalf("wrong payload: %s", body)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s produced no delivery (silent guard no-op is not success)", mode)
		}
	}
	if !reflect.DeepEqual(claudeBefore, e2eSnapshot(t, filepath.Join(f.home, ".claude"))) {
		t.Fatal("Codex setup/launch changed Claude files")
	}
}

func TestSetupCodexE2EPartialInitializationExitStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix directory mode fixture")
	}
	binary := buildCLIBinary(t)
	f := newSetupE2E(t)
	parent := filepath.Join(f.root, "public")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0777); err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(parent, "config.json")
	f.env = append(f.env, "AGENT_NOTIFICATIONS_CONFIG="+canonical)
	out, err := f.run(t, "", binary, "setup-codex", "--plugin-root", f.bundle, "--skip-agent-notify")
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 3 || !strings.Contains(out, "config init") {
		t.Fatalf("partial setup status: %v %s", err, out)
	}
	hooks := filepath.Join(f.home, ".codex", "hooks.json")
	before := e2eRead(t, hooks)
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		out, err = f.run(t, "", binary, "config", "init", "--json")
		if err != nil {
			t.Fatalf("config-only retry: %v %s", err, out)
		}
	}
	if string(e2eRead(t, hooks)) != string(before) {
		t.Fatal("retry changed registration")
	}
}

func TestSetupCodexE2EInstalledLaunchersSurviveReplacement(t *testing.T) {
	binary := buildCLIBinary(t)
	if info, err := os.Stat(binary); err != nil {
		t.Fatal(err)
	} else if info.Size() > 32<<20 {
		t.Skip("coverage-instrumented test executable exceeds the 32 MiB managed staging cap")
	}
	f := newSetupE2E(t)
	platformName := "claude-notifications-" + runtime.GOOS + "-" + runtime.GOARCH
	extension := ""
	if runtime.GOOS == "windows" {
		platformName += ".exe"
		extension = ".bat"
	}
	// Model an older source bundle with only a platform executable and no aliases.
	e2eWrite(t, filepath.Join(f.bundle, "bin", platformName), e2eRead(t, binary))
	installed := filepath.Join(f.home, ".codex", "claude-notifications-go", "bin")
	for pass := 0; pass < 2; pass++ {
		output, err := f.run(t, "", binary, "setup-codex", "--plugin-root", f.bundle, "--skip-agent-notify")
		if err != nil {
			t.Fatalf("setup pass %d: %s %v", pass, output, err)
		}
		for _, name := range []string{"agent-notifications", "claude-notifications"} {
			launcher := filepath.Join(installed, name+extension)
			if runtime.GOOS == "windows" {
				output, err = f.run(t, "", "cmd.exe", "/d", "/s", "/c", `"`+launcher+`" version`)
			} else {
				output, err = f.run(t, "", launcher, "version")
			}
			if err != nil || !strings.HasPrefix(output, name+" v") {
				t.Fatalf("installed %s pass %d: %s %v", name, pass, output, err)
			}
			if runtime.GOOS != "windows" {
				target, err := os.Readlink(launcher)
				if err != nil || target != platformName {
					t.Fatalf("different platform target: %s %v", target, err)
				}
			}
		}
		// Replacing bin during update must recreate a deleted primary launcher too.
		if pass == 0 {
			if err := os.Remove(filepath.Join(installed, "agent-notifications"+extension)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestSetupCodexE2EConfigureNotifications(t *testing.T) {
	bin := buildCLIBinary(t)
	if info, err := os.Stat(bin); err != nil {
		t.Fatal(err)
	} else if info.Size() > 32<<20 {
		t.Skip("coverage-instrumented test executable exceeds the 32 MiB managed staging cap")
	}
	f := newSetupE2E(t)
	body, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	e2eWrite(t, filepath.Join(f.bundle, "bin", "claude-notifications"), body)
	if err := os.Chmod(filepath.Join(f.bundle, "bin", "claude-notifications"), 0700); err != nil {
		t.Fatal(err)
	}
	e2eWrite(t, filepath.Join(f.bundle, "config", "config.json"), []byte(`{"notifications":{"desktop":{"enabled":false,"sound":false,"clickToFocus":false}}}`))
	e2eWrite(t, filepath.Join(f.bundle, "skills", "agent-notify", "SKILL.md"), []byte("canonical test skill"))
	if runtime.GOOS == "darwin" {
		installerNativeFixture(t, filepath.Join(f.bundle, "bin"))
	}
	source := f.bundle
	out, err := f.run(t, "", bin, "setup-codex", "--plugin-root", source, "--agent-notify", "--navigation", "none", "--allow-unknown-caller", "true", "--allow-caller-asserted", "false")
	installDir := filepath.Join(f.home, ".codex", "claude-notifications-go")
	command := filepath.Join(installDir, "bin", "claude-notifications")
	if runtime.GOOS != "darwin" {
		if err != nil {
			t.Fatalf("hooks install should survive agent-notify failure: %v %s", err, out)
		}
		if !strings.Contains(out, "Codex notifications registered") {
			t.Fatal("hooks not registered", out)
		}
		if !strings.Contains(out, "agent-notify setup failed") {
			t.Fatal("missing agent-notify warning", out)
		}
		hooks := e2eRead(t, filepath.Join(f.home, ".codex", "hooks.json"))
		if !strings.Contains(string(hooks), "codex-hook-wrapper") {
			t.Fatal("hooks missing after agent-notify failure")
		}
		if strings.Contains(out, "installed_bundle_required") {
			t.Fatal("configure used the source plugin root", out)
		}
		return
	}
	if err != nil {
		t.Fatalf("setup-codex configure: %v %s", err, out)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	raw := string(e2eRead(t, filepath.Join(f.home, ".codex", "config.toml")))
	if !strings.Contains(raw, command) || strings.Contains(raw, source) {
		t.Fatalf("configure command is not the committed runtime: %s", raw)
	}
	if string(e2eRead(t, filepath.Join(f.home, ".codex", "skills", "agent-notify", "SKILL.md"))) != "canonical test skill" {
		t.Fatal("skill missing after source bundle removal")
	}
}
