//go:build linux || darwin

package installruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These integration tests intentionally use APIs present on 27ea072. A base
// failure must describe filesystem behavior, never a missing identity helper.
type renumberEnv struct {
	root, control, target, sender string
	ctx                           context.Context
}

func renumberSandbox(t *testing.T) *renumberEnv {
	t.Helper()
	// Changing HOME also changes Go's implicit cache paths. Preserve the
	// already pinned tool caches before isolating runtime/config state, so
	// offline subprocess builds work in ordinary CI without cache env overrides.
	cacheOutput, err := exec.Command("go", "env", "-json", "GOMODCACHE", "GOCACHE").Output()
	if err != nil {
		t.Fatal("resolve pinned Go caches:", err)
	}
	var caches map[string]string
	if err := json.Unmarshal(cacheOutput, &caches); err != nil {
		t.Fatal("decode pinned Go caches:", err)
	}
	for _, name := range []string{"GOMODCACHE", "GOCACHE"} {
		if !filepath.IsAbs(caches[name]) {
			t.Fatalf("invalid %s: %q", name, caches[name])
		}
		t.Setenv(name, caches[name])
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"HOME", "XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "TMPDIR", "CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(name, path)
		physical, err := filepath.EvalSymlinks(path)
		if err != nil || !strings.HasPrefix(physical, root+string(os.PathSeparator)) {
			t.Fatalf("unsafe %s: %s %v", name, physical, err)
		}
	}
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy"} {
		t.Setenv(name, "http://127.0.0.1:1")
	}
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GOTELEMETRY", "off")
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", "")
	// Empty is an invalid explicit override. Setenv registers restoration;
	// remove it while this fixture resolves its isolated default config.
	if err := os.Unsetenv("AGENT_NOTIFICATIONS_CONFIG"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	sender := filepath.Join(root, "sender")
	_, file, _, _ := runtime.Caller(0)
	cmd := exec.CommandContext(ctx, "go", "build", "-o", sender, "./cmd/claude-notifications")
	cmd.Dir = filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build sender: %v %s", err, out)
	}
	control := filepath.Join(root, "control")
	if err := os.Mkdir(control, 0700); err != nil {
		t.Fatal(err)
	}
	physical, err := filepath.EvalSymlinks(control)
	if err != nil || !strings.HasPrefix(physical, root+string(os.PathSeparator)) {
		t.Fatalf("unsafe control root: %s %v", physical, err)
	}
	return &renumberEnv{root: root, control: control, target: filepath.Join(root, "runtime", "bin"), sender: sender, ctx: ctx}
}
func renumberWrite(t *testing.T, path string, body []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatal(err)
	}
}
func (e *renumberEnv) command(args ...string) ([]byte, error) {
	return exec.CommandContext(e.ctx, e.sender, args...).CombinedOutput()
}
func (e *renumberEnv) run(t *testing.T, args ...string) []byte {
	t.Helper()
	out, err := e.command(args...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return out
}
func (e *renumberEnv) stage(t *testing.T, marker string) string {
	t.Helper()
	stage := filepath.Join(e.root, "release-"+marker)
	renumberWrite(t, filepath.Join(stage, "claude-notifications-linux-amd64"), renumberRead(t, e.sender), 0755)
	// The Linux basename permits legacy inert fixtures on both systems. Darwin
	// qualified CLI coverage separately supplies a signed native Mach-O bundle.
	renumberWrite(t, filepath.Join(stage, "ClaudeNotifier.app", "Contents", "MacOS", "terminal-notifier-modern"), []byte("#!/bin/sh\nexit 97\n# "+marker+"\n"), 0755)
	return stage
}
func (e *renumberEnv) install(t *testing.T, stage string, extra ...string) Ledger {
	t.Helper()
	entry := "claude-notifications-linux-amd64"
	if _, err := os.Stat(filepath.Join(stage, "claude-notifications-darwin-"+runtime.GOARCH)); err == nil {
		entry = "claude-notifications-darwin-" + runtime.GOARCH
	}
	args := []string{"internal-install-runtime", "--stage", stage, "--target", e.target, "--control-root", e.control, "--entry", entry}
	args = append(args, extra...)
	out := e.run(t, args...)
	l, err := readLedger(e.control)
	if err != nil || l.Native == nil {
		t.Fatalf("installed ledger: %+v %v", l, err)
	}
	want := fmt.Sprintf("managed-runtime committed generation=%d\n", l.Generation)
	for _, option := range extra {
		if option == "--print-native-path" {
			want = l.Native.Path + "\n"
		}
	}
	if string(out) != want {
		t.Fatalf("CLI successful commit output: %q; want %q", out, want)
	}
	return l
}
func e2eRenumberID(t *testing.T, id string) string {
	t.Helper()
	if id == "" {
		return id
	}
	parts := strings.Split(id, ":")
	if len(parts) != 2 {
		t.Fatalf("unexpected unix identity %q", id)
	}
	dev, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%d:%s", dev+1000000, parts[1])
}

// Rewrite serialized evidence, leaving fingerprints, paths, and inode numbers
// untouched. The consistent device mapping preserves ancestor mount groups.
func renumberPersisted(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err = json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for key, item := range x {
				switch key {
				case "DirectoryID", "PreviousDirectoryID", "StagedID", "Directory", "ObjectID", "Identity":
					if id, ok := item.(string); ok && id != "" {
						x[key] = e2eRenumberID(t, id)
						continue
					}
				}
				visit(item)
			}
		case []any:
			for _, item := range x {
				visit(item)
			}
		}
	}
	visit(doc)
	body, err = json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(body, '\n')
}
func renumberLedger(t *testing.T, root string) Ledger {
	t.Helper()
	l, err := readLedger(root)
	if err != nil {
		t.Fatal(err)
	}
	body := renumberPersisted(t, l)
	renumberWrite(t, filepath.Join(root, "ownership.json"), body, 0600)
	if err = json.Unmarshal(body, &l); err != nil {
		t.Fatal(err)
	}
	return l
}
func TestRenumberE2ENonDarwinExactness(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("non-Darwin exactness; Darwin has real renumber flows")
	}
	e := renumberSandbox(t)
	l := e.install(t, e.stage(t, "A"))
	renumberLedger(t, e.control)
	if _, err := ReadInstalledSnapshot(e.control); err == nil {
		t.Fatal("Linux accepted rewritten persisted dev")
	}
	if out, err := e.command("internal-install-runtime", "--stage", e.stage(t, "B"), "--target", e.target, "--control-root", e.control); err == nil {
		t.Fatalf("Linux update accepted dev mismatch: %s", out)
	}
	if got, err := treeFingerprint(l.Native.Path); err != nil || got != l.Native.SHA256 {
		t.Fatal("refusal changed old generation", err)
	}
}
func TestRenumberE2ECLIOutputContract(t *testing.T) {
	e := renumberSandbox(t)
	stage := e.stage(t, "output")
	l := e.install(t, stage)
	previousPath := l.Native.Path
	out := e.run(t, "internal-install-runtime", "--stage", e.stage(t, "printed"), "--target", e.target, "--control-root", e.control, "--print-native-path")
	l, err := readLedger(e.control)
	if err != nil || l.Native == nil || l.Native.Path == previousPath {
		t.Fatalf("flagged install did not commit a new durable generation: %+v %v", l, err)
	}
	if !bytes.Equal(out, []byte(l.Native.Path+"\n")) {
		t.Fatalf("CLI must output only durable path and newline: %q", out)
	}
	if _, err := os.Stat(l.Native.Path); err != nil {
		t.Fatal("printed unpublished native", err)
	}
	if out, err := e.command("internal-install-runtime", "--stage", filepath.Join(e.root, "absent"), "--target", e.target, "--control-root", e.control, "--print-native-path"); err == nil || bytes.Contains(out, []byte(l.Native.Path+"\n")) {
		t.Fatalf("failure printed success path: %q %v", out, err)
	}
	blank := filepath.Join(e.root, "plain-release")
	renumberWrite(t, filepath.Join(blank, "claude-notifications-linux-amd64"), renumberRead(t, e.sender), 0755)
	plain := filepath.Join(e.root, "plain-control")
	out = e.run(t, "internal-install-runtime", "--stage", blank, "--target", filepath.Join(e.root, "plain", "bin"), "--control-root", plain, "--print-native-path")
	if string(out) != "\n" {
		t.Fatalf("no native must print one newline: %q", out)
	}
}
func renumberRead(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
