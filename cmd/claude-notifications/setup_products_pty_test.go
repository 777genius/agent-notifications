//go:build linux || darwin

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/creack/pty"
)

// This harness never builds, downloads, or runs an agent. Root must supply the
// native, release-mode, integrated N1/N2 candidate; an older supplied binary is
// a failure, not an excuse to substitute a stub helper.
type bootstrapFixture struct {
	t                                                  *testing.T
	root, project, home, tools, assets, binary, script string
	env                                                []string
	allowMissingFeature                                bool
	publicLoader                                       bool
}

func newBootstrapFixture(t *testing.T) *bootstrapFixture {
	t.Helper()
	binary := os.Getenv("SELECTOR_TEST_BINARY")
	if binary == "" {
		t.Skip("pending: root must supply SELECTOR_TEST_BINARY built from the integrated candidate")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("SELECTOR_TEST_BINARY must be an absolute native candidate path")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	f := &bootstrapFixture{t: t, root: root, binary: binary}
	// t.TempDir follows the job's absolute TEST TMPDIR, never a real project.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.project = filepath.Join(base, "new TEST project")
	f.home = filepath.Join(base, "TEST home")
	f.tools = filepath.Join(base, "TEST tools")
	f.assets = filepath.Join(base, "TEST assets")
	tmp := filepath.Join(base, "TEST tmp")
	for _, dir := range []string{f.project, f.home, f.tools, f.assets, tmp} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	// Resolve only this finite utility allowlist from the trusted parent. Agent
	// names, profiles, auth, notification endpoints and ambient env never cross.
	for _, name := range strings.Fields("bash sh env cp cat chmod mkdir rm mktemp uname tr wc cmp grep sed awk head dirname basename tar gzip unzip sha256sum shasum python3 node sleep tail sort cut mv ln touch stat readlink setsid rmdir codesign xattr") {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		path, err = filepath.Abs(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.Symlink(path, filepath.Join(f.tools, name)); err != nil {
			t.Fatal(err)
		}
	}
	f.env = []string{"PATH=" + f.tools, "HOME=" + f.home, "USERPROFILE=" + f.home, "TMPDIR=" + tmp, "TMP=" + tmp, "TEMP=" + tmp,
		"XDG_CONFIG_HOME=" + filepath.Join(f.home, "config"), "XDG_CACHE_HOME=" + filepath.Join(f.home, "cache"),
		"XDG_DATA_HOME=" + filepath.Join(f.home, "data"), "XDG_STATE_HOME=" + filepath.Join(f.home, "state"),
		"CLAUDE_CONFIG_DIR=" + filepath.Join(f.home, "claude"), "CODEX_HOME=" + filepath.Join(f.home, "codex"),
		"OPENCODE_CONFIG_DIR=" + filepath.Join(f.home, "opencode"), "GEMINI_CLI_HOME=" + filepath.Join(f.home, "gemini"),
		"TERM=xterm-256color", "NO_COLOR=1", "BOOTSTRAP_RELEASE_COMMIT=0123456789abcdef0123456789abcdef01234567",
		"INSTALL_SCRIPT_URL=https://TEST.invalid/install.sh", "BOOTSTRAP_RELEASES_BASE_URL=https://TEST.invalid/releases"}
	version := f.command(binary, "--version")
	if version.code != 0 || !strings.HasPrefix(version.out, "claude-notifications v") {
		t.Fatalf("candidate version: %+v", version)
	}
	tag := strings.TrimSpace(strings.TrimPrefix(version.out, "claude-notifications "))
	f.env = append(f.env, "BOOTSTRAP_RELEASE_TAG="+tag)
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	t.Logf("candidate=%s sha256=%x tag=%s native=%s/%s root=%s", binary, digest, tag, runtime.GOOS, runtime.GOARCH, root)
	asset := "claude-notifications-" + runtime.GOOS + "-" + runtime.GOARCH
	f.write(filepath.Join(f.assets, asset), data, 0700)
	f.write(filepath.Join(f.assets, "checksums.txt"), []byte(hex.EncodeToString(digest[:])+"  "+asset+"\n"), 0600)
	nativeDownload := ""
	if runtime.GOOS == "darwin" {
		// Reuse the private signed, inert capability fixture. This qualifies the
		// install transaction, never a real notification or permission prompt.
		stage := filepath.Join(f.assets, "TEST native stage")
		installerNativeFixture(t, stage)
		nativeArchive := filepath.Join(f.assets, "ClaudeNotifier.app.zip")
		nativeSum, err := portableasset.Zip(stage, nativeArchive)
		if err != nil {
			t.Fatal(err)
		}
		f.write(filepath.Join(f.assets, "checksums.txt"), []byte(hex.EncodeToString(digest[:])+"  "+asset+"\n"+nativeSum+"  ClaudeNotifier.app.zip\n"), 0600)
		nativeDownload = " https://TEST.invalid/releases/download/" + tag + "/ClaudeNotifier.app.zip) cp " + shellQuote(nativeArchive) + " \"$out\" ;;\n"
	}
	install, err := os.ReadFile(filepath.Join(root, "bin", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	f.write(filepath.Join(f.assets, "install.sh"), install, 0600)
	// Acquisition canaries prove pure-parse/noTTY outcomes stopped before even
	// temporary downloads. There is deliberately no public-network fallback.
	f.write(filepath.Join(f.tools, "curl"), []byte(`#!/usr/bin/env bash
set -eu
if [ "$#" -eq 2 ] && [ "$1" = --help ] && [ "$2" = all ]; then printf '%s\n' --http1.1; exit 0; fi
out=""; url=""
while [ "$#" -gt 0 ]; do
 case "$1" in -o) out=$2; shift 2 ;; -H|-w) shift 2 ;; -*) shift ;; *) url=$1; shift ;; esac
done
printf '%s\n' "$url" >> `+shellQuote(filepath.Join(base, "acquisitions"))+`
case "$url" in
 https://github.com/777genius/agent-notifications/releases/latest) printf '%s' 'https://github.com/777genius/agent-notifications/releases/tag/`+tag+`' ;;
 https://api.github.com/repos/777genius/agent-notifications/commits/`+tag+`) printf '%s' 0123456789abcdef0123456789abcdef01234567 > "$out" ;;
 https://777genius.github.io/agent-notifications/install.sh) cat `+shellQuote(filepath.Join(f.assets, "loader.sh"))+` ;;
 https://raw.githubusercontent.com/777genius/agent-notifications/0123456789abcdef0123456789abcdef01234567/bin/bootstrap.sh) cp `+shellQuote(filepath.Join(f.assets, "bootstrap.sh"))+` "$out" ;;
 https://TEST.invalid/releases/download/`+tag+`/checksums.txt|https://TEST.invalid/releases/download/`+tag+`/`+asset+`|https://TEST.invalid/install.sh|https://raw.githubusercontent.com/777genius/agent-notifications/0123456789abcdef0123456789abcdef01234567/bin/install.sh|https://raw.githubusercontent.com/777genius/agent-notifications/main/bin/install.sh) cp `+shellQuote(f.assets)+`/"${url##*/}" "$out" ;;
 https://TEST.invalid/releases/download/`+tag+`/sound-preview-`+runtime.GOOS+`-`+runtime.GOARCH+`|https://TEST.invalid/releases/download/`+tag+`/list-devices-`+runtime.GOOS+`-`+runtime.GOARCH+`|https://TEST.invalid/releases/download/`+tag+`/list-sounds-`+runtime.GOOS+`-`+runtime.GOARCH+`) exit 22 ;;
`+nativeDownload+` # Fixture asset extensions
 *) printf '%s\n' "$url" >> `+shellQuote(filepath.Join(f.project, "unexpected-acquisition"))+`; exit 99 ;;
esac
`), 0700)
	for _, product := range []string{"claude", "codex"} {
		f.write(filepath.Join(f.tools, product), []byte("#!/usr/bin/env bash\nset -eu\nprintf '%s\\n' "+shellQuote(product)+" >> "+shellQuote(filepath.Join(f.project, "probes"))+"\nexit 99\n"), 0700)
	}
	for _, product := range []string{"opencode", "gemini"} {
		version := "1.18.33"
		if product == "gemini" {
			version = "0.62.0"
		}
		// The stubs only implement --version; executing anything else is a failure.
		f.write(filepath.Join(f.tools, product), []byte("#!/usr/bin/env bash\nset -eu\n[ \"$#\" -eq 1 ] && [ \"$1\" = --version ] || exit 99\npython3 -c 'import errno, os, sys\ntry:\n os.fstat(3)\nexcept OSError as err:\n sys.exit(0 if err.errno == errno.EBADF else 98)\nsys.exit(98)' || exit 98\n[ \"$PWD\" -ef \"$HOME\" ] && [ \"$HOME\" -ef \"$USERPROFILE\" ] || exit 97\ncase \"$PWD\" in */bootstrap-"+product+"-TEST-*/profile) ;; *) exit 96 ;; esac\nprintf '%s\\n' "+shellQuote(product)+" >> "+shellQuote(filepath.Join(f.project, "probes"))+"\nprintf '%s\\n' "+shellQuote(version)+"\n"), 0700)
	}
	script, err := os.ReadFile(filepath.Join(root, "bin", "bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	f.script = string(script)
	t.Cleanup(func() {
		if data, err := os.ReadFile(filepath.Join(f.project, "unexpected-acquisition")); err == nil {
			t.Errorf("unexpected acquisition URL: %s", data)
		}
	})
	return f
}
func (f *bootstrapFixture) publicJourney() {
	f.t.Helper()
	loader, err := os.ReadFile(filepath.Join(f.root, "bin", "setup.sh"))
	if err != nil {
		f.t.Fatal(err)
	}
	f.write(filepath.Join(f.assets, "loader.sh"), loader, 0600)
	f.write(filepath.Join(f.assets, "bootstrap.sh"), []byte(f.script), 0600)
	f.publicLoader = true
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func (f *bootstrapFixture) write(path string, data []byte, mode os.FileMode) {
	f.t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		f.t.Fatal(err)
	}
}

type bootstrapResult struct {
	code        int
	out, screen string
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if e, ok := err.(*exec.ExitError); ok {
		return e.ExitCode()
	}
	return -1
}
func (f *bootstrapFixture) command(path string, args ...string) bootstrapResult {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, path, args...)
	c.Env = f.env
	c.Dir = f.project
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var out, errout bytes.Buffer
	c.Stdout = &out
	c.Stderr = &errout
	err := c.Run()
	if ctx.Err() != nil {
		f.t.Fatalf("TEST command timeout: %s %v: %s", path, args, errout.String())
	}
	return bootstrapResult{exitCode(err), out.String(), errout.String()}
}
func (f *bootstrapFixture) noTTY(args ...string) bootstrapResult {
	path := filepath.Join(f.project, "bootstrap.sh")
	f.write(path, []byte(f.script), 0600)
	return f.command(filepath.Join(f.tools, "bash"), append([]string{path}, args...)...)
}

// Prompt steps wait for observable rendered controls, never a timing guess.
// bytes may intentionally queue a second answer at the selection boundary.
type promptStep struct {
	visible, answer string
	before          func()
}

func (f *bootstrapFixture) terminal(args []string, steps ...promptStep) bootstrapResult {
	f.t.Helper()
	feature := f.command(f.binary, "setup-products", "features")
	if !f.allowMissingFeature && (feature.code != 0 || feature.out != "terminal-selector-v1\n") {
		f.t.Fatalf("supplied candidate lacks accepted N1 features: %+v", feature)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, filepath.Join(f.tools, "bash"), append([]string{"--noprofile", "--norc", "-s", "--"}, args...)...)
	if f.publicLoader {
		c = exec.CommandContext(ctx, filepath.Join(f.tools, "bash"), append([]string{"--noprofile", "--norc", "-c", `set -o pipefail; curl -fsSL https://777genius.github.io/agent-notifications/install.sh | bash -s -- "$@"`, "TEST-curl"}, args...)...)
	}
	c.Env = f.env
	c.Dir = f.project
	// Script stdin and data stdout are pipes; FD2 is the controlling terminal.
	if f.publicLoader {
		c.Stdin = strings.NewReader("")
	} else {
		c.Stdin = strings.NewReader(f.script)
	}
	var stdout bytes.Buffer
	c.Stdout = &stdout
	terminal, err := pty.StartWithAttrs(c, &pty.Winsize{Rows: 30, Cols: 110}, &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 2})
	if err != nil {
		f.t.Fatal(err)
	}
	chunks := make(chan string, 64)
	var reader sync.WaitGroup
	reader.Add(1)
	go func() {
		defer reader.Done()
		defer close(chunks)
		buf := make([]byte, 4096)
		for {
			n, err := terminal.Read(buf)
			if n > 0 {
				select {
				case chunks <- string(buf[:n]):
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	finished := false
	defer func() {
		cancel()
		if !finished {
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
		_ = terminal.Close()
		reader.Wait()
		if !finished {
			<-done
		}
	}()
	var screen strings.Builder
	next := 0
	since := 0
	var runErr error
loop:
	for {
		select {
		case chunk, open := <-chunks:
			if !open {
				chunks = nil
				continue
			}
			screen.WriteString(chunk)
			if next < len(steps) && strings.Contains(strings.ToLower(screen.String()[since:]), strings.ToLower(steps[next].visible)) {
				step := steps[next]
				if step.before != nil {
					step.before()
				}
				if _, err := io.WriteString(terminal, step.answer); err != nil {
					f.t.Fatal(err)
				}
				since = screen.Len()
				next++
			}
		case runErr = <-done:
			finished = true
			break loop
		case <-ctx.Done():
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
			_ = terminal.Close()
			reader.Wait()
			f.t.Fatalf("candidate TEST journey timed out at prompt %d/%d:\n%s", next, len(steps), screen.String())
		}
	}
	// The child has closed its slave; drain its final status/recovery bytes
	// before closing the master. A bounded context still protects the reader.
	if chunks != nil {
		for chunk := range chunks {
			screen.WriteString(chunk)
		}
	}
	_ = terminal.Close()
	cancel()
	reader.Wait()
	if next != len(steps) {
		f.t.Fatalf("journey stopped before requested prompt %d/%d: status=%d\n%s\n%s", next, len(steps), exitCode(runErr), stdout.String(), screen.String())
	}
	return bootstrapResult{exitCode(runErr), stdout.String(), screen.String()}
}
func (f *bootstrapFixture) snapshot() map[string]string {
	f.t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(f.home, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			result[path] = hex.EncodeToString(sum[:])
		}
		return nil
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return result
}
func (f *bootstrapFixture) samePersistentState(before map[string]string) {
	f.t.Helper()
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(f.snapshot())
	if !bytes.Equal(a, b) {
		f.t.Fatalf("pre-consent product writes: before=%s after=%s", a, b)
	}
}
func (f *bootstrapFixture) unchanged(before map[string]string) {
	f.t.Helper()
	f.samePersistentState(before)
	if data, err := os.ReadFile(filepath.Join(f.project, "probes")); err == nil {
		f.t.Fatalf("agent version ran before consent: %s", data)
	}
}

// Regression: curl script stdin was consumed as answers or redirected data
// stdout disabled the rich prompt pair. Cancel must not start any host probe.
func TestBootstrapSelectorPTY(t *testing.T) {
	for _, row := range []struct {
		mode, key, control string
		public             bool
	}{{"plain", "cancel\n", "comma-separated", false}, {"rich", "\x1b", "enter", false}, {"plain", "cancel\n", "comma-separated", true}, {"rich", "\x1b", "enter", true}} {
		t.Run(fmt.Sprintf("%s-public-%v", row.mode, row.public), func(t *testing.T) {
			f := newBootstrapFixture(t)
			if row.public {
				f.publicJourney()
			}
			if row.mode == "rich" && runtime.GOOS == "darwin" {
				// A stuck Go reader should preserve its stack in native evidence
				// before the outer watchdog kills the whole TEST process group.
				wrapper := filepath.Join(f.project, "TEST selector watchdog.py")
				f.write(wrapper, []byte("import signal, subprocess, sys\np = subprocess.Popen(sys.argv[1:])\ntry:\n try:\n  code = p.wait(timeout=5)\n except subprocess.TimeoutExpired:\n  p.send_signal(signal.SIGQUIT)\n  code = p.wait(timeout=5)\nfinally:\n if p.poll() is None:\n  p.kill()\n p.wait()\nsys.exit(code)\n"), 0600)
				f.script = strings.Replace(f.script, `"$_CONFIG_HELPER" setup-products "$operation" "$@" <&3`,
					`python3 `+shellQuote(wrapper)+` "$_CONFIG_HELPER" setup-products "$operation" "$@" <&3`, 1)
				if row.public {
					f.write(filepath.Join(f.assets, "bootstrap.sh"), []byte(f.script), 0600)
				}
			}
			before := f.snapshot()
			r := f.terminal([]string{"--ui=" + row.mode}, promptStep{row.control, row.key, nil})
			if r.code != 0 {
				t.Fatalf("cancel status %d: %s", r.code, r.screen)
			}
			f.unchanged(before)
			if row.mode == "plain" && strings.Contains(r.screen, "\x1b") {
				t.Fatalf("plain accessibility output contains ANSI: %q", r.screen)
			}
			if r.out != "" {
				t.Fatalf("UI leaked to data stdout: %q", r.out)
			}
		})
	}
	// Regression: a same-tag helper with missing/malformed additive features
	// entered the old menu instead of failing closed before any prompt/probe.
	t.Run("feature refusal before prompts", func(t *testing.T) {
		f := newBootstrapFixture(t)
		f.allowMissingFeature = true
		before := f.snapshot()
		f.corruptResult("features", []byte("terminal-selector-v1\n\n"), 0)
		r := f.terminal([]string{"--plain"})
		if r.code == 0 {
			t.Fatalf("feature mismatch entered installation: %+v", r)
		}
		f.unchanged(before)
	})
	t.Run("missing controlling tty", func(t *testing.T) {
		f := newBootstrapFixture(t)
		r := f.noTTY("--plain")
		if r.code != 1 || !strings.Contains(r.screen, "No controlling TTY") {
			t.Fatalf("missing tty: %+v", r)
		}
		f.noAcquisition()
	})
	// Regression: a UI presentation flag incorrectly required a selector feature
	// on a complete explicit request, despite no product/channel question pending.
	t.Run("complete explicit UI needs no selector feature", func(t *testing.T) {
		f := newBootstrapFixture(t)
		f.script = strings.Replace(f.script, "printf '%s\\n' terminal-selector-v1; return 0", "return 1", 1)
		f.publicJourney()
		r := f.command(filepath.Join(f.tools, "bash"), "--noprofile", "--norc", "-c", `set -o pipefail; curl -fsSL https://777genius.github.io/agent-notifications/install.sh | bash -s -- "$@"`, "TEST-curl", "--product", "opencode", "--webhook", "--plain")
		requireBootstrapSuccess(t, r)
		f.assertConsumer("opencode-notifications", true)
	})
}
func (f *bootstrapFixture) noAcquisition() {
	f.t.Helper()
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.project), "acquisitions")); !os.IsNotExist(err) {
		f.t.Fatal("pure parse/noTTY acquired release bytes")
	}
}

// Regression: an Enter/y queued behind selecting a product granted Yes at the
// next process's consent question. The fresh default-No must still be needed.
func TestBootstrapSelectionDoesNotApproveConsent(t *testing.T) {
	for _, queued := range []string{"\n", "y\n"} {
		t.Run(fmt.Sprintf("queued %q", queued), func(t *testing.T) {
			f := newBootstrapFixture(t)
			before := f.snapshot()
			r := f.terminal([]string{"--plain", "--webhook"}, promptStep{"comma-separated", "opencode\n" + queued, nil}, promptStep{"[y/n]", "n\n", nil})
			if r.code != 0 {
				t.Fatalf("No: %+v", r)
			}
			f.unchanged(before)
		})
	}
	t.Run("rich space arrow Enter is selection only", func(t *testing.T) {
		f := newBootstrapFixture(t)
		before := f.snapshot()
		r := f.terminal([]string{"--ui=rich", "--webhook"}, promptStep{"enter", " \x1b[B \r\ry", nil})
		if r.code != 0 {
			t.Fatalf("rich cancel: %+v", r)
		}
		f.unchanged(before)
	})
}

// Regression: singleton channels used a shell read and skipped final consent;
// incomplete --products asked questions instead of failing before acquisition.
func TestBootstrapInteractiveChannelConsent(t *testing.T) {
	for _, answer := range []string{"n\n", "cancel\n", "\x04"} {
		t.Run(fmt.Sprintf("final %q", answer), func(t *testing.T) {
			f := newBootstrapFixture(t)
			before := f.snapshot()
			r := f.terminal([]string{"--product", "opencode", "--plain"}, promptStep{"comma-separated", "webhook\n", nil}, promptStep{"[y/n]", answer, nil})
			if answer == "\x04" {
				if r.code != 1 {
					t.Fatalf("EOF: %+v", r)
				}
			} else if r.code != 0 {
				t.Fatalf("No/cancel: %+v", r)
			}
			f.unchanged(before)
		})
	}
	t.Run("incomplete multi", func(t *testing.T) {
		f := newBootstrapFixture(t)
		r := f.noTTY("--products", "opencode,gemini")
		if r.code != 1 {
			t.Fatalf("incomplete multi: %+v", r)
		}
		f.noAcquisition()
	})
}

// Regression: trailing LF was erased by $(), NUL was dropped by read, and a
// nonzero child's prefix could be dispatched. Inject only transport faults
// after the actual candidate selection has run through its terminal cleanup.
func TestBootstrapStrictResultBytes(t *testing.T) {
	for _, data := range []string{"opencode\n\n", "opencode\r\n", "opencode\x00\n", "\x1b[31mopencode\n", "opencode,opencode\n", "unknown\n", strings.Repeat("a", 65) + "\n"} {
		t.Run(fmt.Sprintf("record %q", data), func(t *testing.T) {
			f := newBootstrapFixture(t)
			before := f.snapshot()
			f.corruptResult("select", []byte(data), 0)
			r := f.terminal([]string{"--plain", "--webhook"}, promptStep{"comma-separated", "opencode\n", nil})
			if r.code == 0 {
				t.Fatalf("malformed result accepted: %+v", r)
			}
			f.unchanged(before)
		})
	}
	t.Run("nonzero prefix exact status", func(t *testing.T) {
		f := newBootstrapFixture(t)
		before := f.snapshot()
		f.corruptResult("select", []byte("opencode\n"), 143)
		r := f.terminal([]string{"--plain", "--webhook"}, promptStep{"comma-separated", "opencode\n", nil})
		if r.code != 143 {
			t.Fatalf("signal status lost: %+v", r)
		}
		f.unchanged(before)
	})
	for _, row := range []struct {
		name, operation, data string
		status                int
		probes                bool
	}{
		{"approval extra LF", "confirm", "approved\n\n", 0, false},
		{"approval NUL", "confirm", "approved\x00\n", 0, false},
		{"approval nonzero prefix", "confirm", "approved\n", 143, false},
		{"preflight nonempty stdout", "preflight", "unexpected\n", 0, true},
		{"preflight nonzero stdout prefix", "preflight", "unexpected\n", 1, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			f := newBootstrapFixture(t)
			before := f.snapshot()
			f.corruptResult(row.operation, []byte(row.data), row.status)
			r := f.terminal([]string{"--product", "opencode", "--plain"}, promptStep{"comma-separated", "webhook\n", nil}, promptStep{"[y/n]", "y\n", nil})
			if r.code == 0 || (row.status != 0 && r.code != row.status) {
				t.Fatalf("unsafe approval/checkpoint: %+v", r)
			}
			if row.probes {
				f.samePersistentState(before)
			} else {
				f.unchanged(before)
			}
		})
	}
	t.Run("getter nonzero prefix", func(t *testing.T) {
		f := newBootstrapFixture(t)
		before := f.snapshot()
		f.corruptProjection([]byte("home\x00/TEST\x00"), 143)
		r := f.terminal([]string{"--product", "opencode", "--plain"}, promptStep{"comma-separated", "webhook\n", nil}, promptStep{"[y/n]", "y\n", nil})
		if r.code != 143 {
			t.Fatalf("getter child status/prefix accepted: %+v", r)
		}
		f.unchanged(before)
	})
	for _, data := range []string{"home\x00/TEST\x00home\x00/duplicate\x00", "unknown\x00/TEST\x00", "home\x00/TEST", "home\x00/TEST\x00odd\x00", "home\x00/TEST\x00tail", strings.Repeat("x", 65537)} {
		t.Run(fmt.Sprintf("projection %x", sha256.Sum256([]byte(data))), func(t *testing.T) {
			f := newBootstrapFixture(t)
			before := f.snapshot()
			f.corruptProjection([]byte(data), 0)
			r := f.terminal([]string{"--product", "opencode", "--plain"}, promptStep{"comma-separated", "webhook\n", nil}, promptStep{"[y/n]", "y\n", nil})
			if r.code == 0 {
				t.Fatalf("invalid frozen projection accepted: %+v", r)
			}
			f.unchanged(before)
		})
	}
}
func (f *bootstrapFixture) corruptResult(operation string, data []byte, status int) {
	f.t.Helper()
	path := filepath.Join(f.project, "TEST corrupt result")
	f.write(path, data, 0600)
	f.script = strings.Replace(f.script, "selector_collect() {", "fixture_selector_collect() {", 1)
	patch := `selector_collect() {
 fixture_selector_collect "$@" || return $?
 if [ "$1" = ` + shellQuote(operation) + ` ]; then cp ` + shellQuote(path) + ` "$_SELECTOR_RESULT"; return ` + fmt.Sprint(status) + `; fi
}
`
	f.script = strings.Replace(f.script, "main \"$@\"", patch+"\nmain \"$@\"", 1)
}
func (f *bootstrapFixture) corruptProjection(data []byte, status int) {
	f.t.Helper()
	path := filepath.Join(f.project, "TEST corrupt projection")
	f.write(path, data, 0600)
	// Interpose the child process only for the readonly getter. All selection,
	// confirmation/record creation and executable staging remain the real binary.
	patch := `fixture_helper() {
 if [ "${1:-}" = setup-products ] && [ "${2:-}" = intent-args ]; then cat ` + shellQuote(path) + `; return ` + fmt.Sprint(status) + `; fi
 ` + shellQuote(f.binary) + ` "$@"
}
`
	f.script = strings.Replace(f.script, "load_frozen_dispatch() {", "load_frozen_dispatch() {\n    _CONFIG_HELPER=fixture_helper", 1)
	f.script = strings.Replace(f.script, "main \"$@\"", patch+"\nmain \"$@\"", 1)
}

// Regression: executable disappearance or alias A->B after displayed consent
// caused fallback to a different agent/profile. B must remain byte-identical.
func TestBootstrapStaleSelectedFacts(t *testing.T) {
	t.Run("selected executable disappears", func(t *testing.T) {
		f := newBootstrapFixture(t)
		before := f.snapshot()
		r := f.terminal([]string{"--product", "opencode", "--plain"}, promptStep{"comma-separated", "webhook\n", nil}, promptStep{"[y/n]", "y\n", func() {
			if err := os.Remove(filepath.Join(f.tools, "opencode")); err != nil {
				t.Fatal(err)
			}
		}})
		if r.code == 0 {
			t.Fatalf("stale executable installed: %+v", r)
		}
		f.unchanged(before)
	})
	t.Run("canonical Gemini alias", func(t *testing.T) {
		f := newBootstrapFixture(t)
		a := filepath.Join(f.home, "A")
		b := filepath.Join(f.home, "B")
		alias := filepath.Join(f.home, "alias")
		for _, path := range []string{a, b} {
			if err := os.MkdirAll(filepath.Join(path, ".gemini"), 0700); err != nil {
				t.Fatal(err)
			}
			f.write(filepath.Join(path, ".gemini", "settings.json"), []byte("{\"TEST\":true}\n"), 0600)
		}
		if err := os.Symlink(a, alias); err != nil {
			t.Fatal(err)
		}
		for i, value := range f.env {
			if strings.HasPrefix(value, "GEMINI_CLI_HOME=") {
				f.env[i] = "GEMINI_CLI_HOME=" + alias
			}
		}
		before, err := os.ReadFile(filepath.Join(b, ".gemini", "settings.json"))
		if err != nil {
			t.Fatal(err)
		}
		r := f.terminal([]string{"--product", "gemini", "--plain"}, promptStep{"comma-separated", "webhook\n", nil}, promptStep{"[y/n]", "y\n", func() {
			if err := os.Remove(alias); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(b, alias); err != nil {
				t.Fatal(err)
			}
		}})
		after, err := os.ReadFile(filepath.Join(b, ".gemini", "settings.json"))
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("alias redirected effects to B: %v %+v", err, r)
		}
		if r.code == 0 {
			f.assertConsumer("gemini-notifications", true)
		}
	})
	// Regression: fresh Claude/Codex approval intentionally has no existing
	// ledger runtime. Requiring or passing an empty runtime override prevents
	// hooks preparation from supplying its actual runtime to portable admission.
	t.Run("fresh-Claude-frozen-runtime-authority", func(t *testing.T) {
		f := newBootstrapFixture(t)
		f.releaseAssets()
		f.captureIntent()
		r := f.terminal([]string{"--plain", "--agent-notify"}, promptStep{"comma-separated", "claude\n", nil}, promptStep{"[y/n]", "y\n", nil})
		var scopes map[string][]byte
		if err := json.Unmarshal(f.confirmedIntent()["scopes"], &scopes); err != nil {
			t.Fatal(err)
		}
		if _, present := scopes["runtime-root"]; present {
			t.Fatal("fresh approval fabricated a runtime before hooks preparation")
		}
		requireBootstrapSuccess(t, r)
		f.assertPortable("claude")
		f.assertNoPortable("codex")
	})

	// Regression: our own hooks generation must not invalidate unchanged MCP
	// identity; an outside keep-off decision must stop that same admission.
	for _, changed := range []bool{false, true} {
		name := "unchanged-projection-own-generation-success"
		if changed {
			name = "own-hooks-generation-then-portable-admission"
		}
		t.Run(name, func(t *testing.T) {
			f := newBootstrapFixture(t)
			f.releaseAssets()
			// Existing Claude binding plus absent Codex qualifies the auto keep/off
			// window; own hooks commit for both must preserve the MCP subset.
			requireBootstrapSuccess(t, f.noTTY("--product", "claude", "--agent-notify", "--navigation", "none", "--allow-unknown-caller", "true", "--allow-caller-asserted", "false"))
			beforeBindings := mustFixtureJSON(t, f.portableBindings())
			f.captureIntent()
			visible, release := f.barrier("install_claude")
			var hooksGeneration uint64
			r := f.terminal([]string{"--plain"}, promptStep{"comma-separated", "claude,codex\n", nil}, promptStep{"[y/n]", "y\n", nil}, promptStep{visible, "", func() {
				l := f.ownership()
				if _, ok := l.Consumers["claude-hooks"]; !ok || l.Generation == 0 {
					t.Fatal("actual hook commit missing")
				}
				hooksGeneration = l.Generation
				if changed {
					f.policyEnabled(false)
				}
				release()
			}})
			if changed {
				if r.code == 0 || !strings.Contains(r.screen+r.out, "concurrent_change") {
					t.Fatalf("outside opt-out absorbed: %+v", r)
				}
				if strings.Contains(r.screen, "claude: completed") {
					t.Fatalf("failed portable phase reported complete: %+v", r)
				}
				// Regression: partial recovery guidance must not promise delivery or
				// instruct a whole-bootstrap rerun after actual hooks have committed.
				if strings.Contains(r.screen+r.out, "rerun bootstrap") || strings.Contains(r.screen+r.out, "Desktop/hook notifications still work") {
					t.Fatalf("unsafe partial recovery guidance: %+v", r)
				}
				if mustFixtureJSON(t, f.portableBindings()) != beforeBindings {
					t.Fatal("refused admission changed the existing binding")
				}
			} else {
				if r.code != 0 {
					// Report the actual admission error and projections only from
					// this disposable fixture; no user profile is observed here.
					for _, entry := range f.env {
						key, value, ok := strings.Cut(entry, "=")
						if ok {
							t.Setenv(key, value)
						}
					}
					raw, _ := json.Marshal(f.confirmedIntent())
					var intent confirmedBootstrapIntent
					if err := json.Unmarshal(raw, &intent); err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					current, generation, err := setupwizard.ObserveBootstrapMCP(ctx, intentWizardRequest(intent))
					cancel()
					t.Logf("TEST MCP admission generation=%d error=%v expected=%+v current=%+v", generation, err, intent.MCP.Projection, current)
				}
				requireBootstrapSuccess(t, r)
				f.assertPortable("claude")
				if f.ownership().Generation <= hooksGeneration {
					t.Fatal("portable phase did not commit")
				}
			}
			f.assertNoPortable("codex")
			if !strings.Contains(string(f.confirmedIntent()["mcp"]), `"skipped":["codex"]`) {
				t.Fatalf("fixture did not freeze Codex off: %s", f.confirmedIntent()["mcp"])
			}
			f.assertConsumer("codex:"+filepath.Join(f.home, "codex", "hooks.json"), true)
		})
	}
	// Regression: external keep-off after Yes must be compared to the confirmed
	// observation, before even the next marketplace/runtime product effect.
	t.Run("last-binding-or-keep-off-before-first-effect", func(t *testing.T) {
		f := newBootstrapFixture(t)
		f.releaseAssets()
		requireBootstrapSuccess(t, f.noTTY("--product", "claude", "--skip-agent-notify"))
		f.policyEnabled(true)
		_ = os.Remove(filepath.Join(f.project, "probes"))
		visible, release := f.barrier("load_frozen_dispatch")
		var afterOutsideChange map[string]string
		r := f.terminal([]string{"--plain", "--agent-notify"}, promptStep{"comma-separated", "claude\n", nil}, promptStep{"[y/n]", "y\n", nil}, promptStep{visible, "", func() {
			f.policyEnabled(false)
			afterOutsideChange = f.snapshot()
			release()
		}})
		if r.code == 0 || !strings.Contains(r.screen+r.out, "concurrent_change") {
			t.Fatalf("stale first-effect checkpoint: %+v", r)
		}
		f.unchanged(afterOutsideChange)
		f.assertNoPortable("claude")
		f.assertNoPortable("codex")
	})
	t.Run("post-admission-existing-CAS", testBootstrapPostAdmissionCAS)

}

// Regression: observers were reported wholly failed after an actual managed
// registration commit followed by config failure; a successful sibling reran.
func TestBootstrapPartialProgress(t *testing.T) {
	t.Run("mixed actual observer registration", func(t *testing.T) {
		f := newBootstrapFixture(t)
		f.script = strings.Replace(f.script, "initialize_config() {", "fixture_initialize_config() {", 1)
		patch := `initialize_config() { if [ "$PRODUCT" = opencode ]; then return 1; fi; fixture_initialize_config; }
`
		f.script = strings.Replace(f.script, "main \"$@\"", patch+"\nmain \"$@\"", 1)
		r := f.noTTY("--products", "opencode,gemini", "--webhook")
		if r.code == 0 || !strings.Contains(r.screen, "opencode: registration_committed; follow_up_failed:config") || !strings.Contains(r.screen, "gemini: not_started") {
			t.Fatalf("untruthful partial result: %+v", r)
		}
		f.assertConsumer("opencode-notifications", true)
		f.assertConsumer("gemini-notifications", false)
		if _, err := os.Stat(filepath.Join(f.home, "opencode", "plugins", "agent-notifications.js")); err != nil {
			t.Fatal("registration missing:", err)
		}
		if _, err := os.Stat(filepath.Join(f.home, "gemini", ".gemini", "settings.json")); !os.IsNotExist(err) {
			t.Fatal("unstarted Gemini wrote settings")
		}
	})
	t.Run("mixed actual completion", func(t *testing.T) {
		f := newBootstrapFixture(t)
		r := f.noTTY("--products", "opencode,gemini", "--webhook")
		if r.code != 0 || !strings.Contains(r.screen, "opencode: completed") || !strings.Contains(r.screen, "gemini: completed") {
			t.Fatalf("mixed completion: %+v", r)
		}
		f.assertConsumer("opencode-notifications", true)
		f.assertConsumer("gemini-notifications", true)
	})
	t.Run("Claude completed Codex committed followup failure Gemini remainder", func(t *testing.T) {
		testBootstrapLegacyPartial(t)
	})
}
func (f *bootstrapFixture) assertConsumer(id string, present bool) {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.control(), "ownership.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	var ledger struct{ Consumers map[string]json.RawMessage }
	if err := json.Unmarshal(data, &ledger); err != nil {
		f.t.Fatal(err)
	}
	_, got := ledger.Consumers[id]
	if got != present {
		f.t.Fatalf("actual owned consumer %s: present=%v want=%v ledger=%s", id, got, present, data)
	}
}

// Regression: normalization silently replaced caller false/false at the shell
// handoff or allowed partial/repeated/observer-only route flags to acquire UI.
func TestBootstrapNormalizedRouteConsent(t *testing.T) {
	for _, args := range [][]string{{"--navigation", "none"}, {"--navigation", "none", "--allow-unknown-caller", "false", "--allow-caller-asserted", "false", "--allow-unknown-caller", "false"}, {"--product", "opencode", "--webhook", "--agent-notify"}, {"--skip-agent-notify", "--request-permission"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := newBootstrapFixture(t)
			r := f.noTTY(args...)
			if r.code != 1 {
				t.Fatalf("invalid route: %+v", r)
			}
			f.noAcquisition()
		})
	}
	t.Run("valid false false No", func(t *testing.T) {
		f := newBootstrapFixture(t)
		before := f.snapshot()
		r := f.terminal([]string{"--plain", "--agent-notify", "--navigation", "none", "--allow-unknown-caller", "false", "--allow-caller-asserted", "false"}, promptStep{"comma-separated", "claude\n", nil}, promptStep{"[y/n]", "n\n", nil})
		if r.code != 0 {
			t.Fatalf("supported normalized route refused: %+v", r)
		}
		f.unchanged(before)
		if !strings.Contains(r.screen, "allow-unknown-caller=false allow-caller-asserted=false") {
			t.Fatalf("caller decision missing from visible consent: %s", r.screen)
		}
	})
	t.Run("false-false-and-preserved-disabled-on-disk", func(t *testing.T) {
		testBootstrapAppliedPolicy(t)
	})
}

// Regression: --json with pending product/channel UI downloaded or probed
// before an eventual error, or silently chose detection defaults.
func TestBootstrapPendingJSON(t *testing.T) {
	for _, args := range [][]string{{"--json"}, {"--product", "opencode", "--json"}, {"--products", "opencode,gemini", "--json"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := newBootstrapFixture(t)
			before := f.snapshot()
			r := f.noTTY(args...)
			if r.code != 1 {
				t.Fatalf("pending JSON: %+v", r)
			}
			f.noAcquisition()
			f.unchanged(before)
		})
	}
}

// Regression boundaries below require real release assets and actual owned
// registrations. Only the external Claude CLI protocol is simulated; acquisition,
// helper, installer, portable builder and transactions are the candidate code.
func (f *bootstrapFixture) releaseAssets() {
	f.t.Helper()
	version := strings.TrimSpace(strings.TrimPrefix(f.command(f.binary, "--version").out, "claude-notifications v"))
	archive := filepath.Join(f.assets, "agent-notify-portable-"+runtime.GOOS+"-"+runtime.GOARCH+".zip")
	pkg, err := portableasset.Build(portableasset.BuildRequest{Version: version, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Executable: f.binary, OutputRoot: filepath.Join(f.assets, "portable"), Archive: archive})
	if err != nil {
		f.t.Fatal(err)
	}
	checksums, err := os.OpenFile(filepath.Join(f.assets, "checksums.txt"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		f.t.Fatal(err)
	}
	_, err = fmt.Fprintf(checksums, "%s  %s\n", pkg.ArchiveSHA256, filepath.Base(archive))
	closeErr := checksums.Close()
	if err != nil || closeErr != nil {
		f.t.Fatalf("checksums: %v %v", err, closeErr)
	}
	f.t.Logf("same-release portable sha256=%s", pkg.ArchiveSHA256)
	source := filepath.Join(f.assets, "source.tar.gz")
	file, err := os.Create(source)
	if err != nil {
		f.t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	// Finite source roots include the real manifest, config defaults, wrappers and
	// skills used by existing Codex/Claude installers. Never archive .git/cache.
	for _, dir := range []string{".claude-plugin", "bin", "config", "hooks", "commands", "skills", "portable-package", "sounds"} {
		err = filepath.WalkDir(filepath.Join(f.root, dir), func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() && !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				return fmt.Errorf("unexpected source type: %s", path)
			}
			rel, err := filepath.Rel(f.root, path)
			if err != nil {
				return err
			}
			link := ""
			if info.Mode()&os.ModeSymlink != 0 {
				link, err = os.Readlink(path)
				if err != nil {
					return err
				}
				if filepath.IsAbs(link) || strings.HasPrefix(filepath.Clean(link), "..") {
					return fmt.Errorf("source link outside fixture: %s", path)
				}
			}
			hdr, err := tar.FileInfoHeader(info, link)
			if err != nil {
				return err
			}
			hdr.Name = "candidate/" + filepath.ToSlash(rel)
			if err = tw.WriteHeader(hdr); err != nil {
				return err
			}
			if entry.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return nil
			}
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, in)
			closeErr := in.Close()
			if err != nil {
				return err
			}
			return closeErr
		})
		if err != nil {
			f.t.Fatal(err)
		}
	}
	for _, err := range []error{tw.Close(), gz.Close(), file.Close()} {
		if err != nil {
			f.t.Fatal(err)
		}
	}
	curlPath := filepath.Join(f.tools, "curl")
	curl, err := os.ReadFile(curlPath)
	if err != nil {
		f.t.Fatal(err)
	}
	// Every URL is finite and pinned; unknown requests still abort with 99.
	curlText := strings.Replace(string(curl), " # Fixture asset extensions", " https://TEST.invalid/source/0123456789abcdef0123456789abcdef01234567.tar.gz) cp "+shellQuote(source)+" \"$out\" ;;\n https://TEST.invalid/releases/download/v"+version+"/"+filepath.Base(archive)+") cp "+shellQuote(archive)+" \"$out\" ;;", 1)
	f.write(curlPath, []byte(curlText), 0700)
	f.env = append(f.env, "BOOTSTRAP_SOURCE_BASE_URL=https://TEST.invalid/source", "AGENT_NOTIFICATIONS_CONFIG="+filepath.Join(f.home, "config", "TEST-global.json"))
	// The fixture agent accepts only the install/list protocol. It never runs an
	// interactive session, network, auth or notification. Unexpected argv aborts.
	stub := `#!/usr/bin/env python3
import os, sys, json, pathlib, shutil
args=sys.argv[1:]
project=pathlib.Path(` + fmt.Sprintf("%q", f.project) + `)
with (project/'probes').open('a') as log: log.write('claude '+json.dumps(args)+'\n')
if pathlib.Path('/proc/self/fd/3').exists(): sys.exit(98)
root=pathlib.Path(os.environ.get('CLAUDE_CONFIG_DIR',''))
if not str(root).startswith(` + fmt.Sprintf("%q", f.home+string(os.PathSeparator)) + `): sys.exit(97)
assets=pathlib.Path(` + fmt.Sprintf("%q", f.assets) + `)
version=` + fmt.Sprintf("%q", version) + `
key='claude-notifications-go@claude-notifications-go'
if args==['plugin','marketplace','add','777genius/agent-notifications']:
 dest=root/'plugins'/'marketplaces'/'claude-notifications-go'
 if dest.exists(): shutil.rmtree(dest)
 dest.mkdir(parents=True)
 import tarfile
 with tarfile.open(assets/'source.tar.gz') as tar:
  for member in tar.getmembers():
   member.name=member.name.removeprefix('candidate/')
   tar.extract(member,dest,filter='data')
elif args in (['plugin','install',key],['plugin','update',key]):
 dest=root/'plugins'/'cache'/'claude-notifications-go'/'claude-notifications-go'/version
 if not dest.exists(): shutil.copytree(root/'plugins'/'marketplaces'/'claude-notifications-go',dest,symlinks=True)
 path=root/'plugins'/'installed_plugins.json'
 path.write_text(json.dumps({'version':2,'plugins':{key:[{'scope':'user','version':version,'installPath':str(dest)}]}})+'\n')
elif args==['plugin','list','--json']:
 rows=[]
 for path in sorted((root/'skills').glob('*/.claude-plugin/plugin.json')):
  manifest=json.loads(path.read_text()); name=manifest['name']
  rows.append({'id':name+'@skills-dir','version':manifest['version'],'scope':'user','enabled':True,'installPath':str(path.parent.parent)})
 print(json.dumps(rows))
else:
 (project/'unexpected-agent-call').write_text(json.dumps(args))
 sys.exit(99)
`
	f.write(filepath.Join(f.tools, "claude"), []byte(stub), 0700)
	f.t.Cleanup(func() {
		if data, err := os.ReadFile(filepath.Join(f.project, "unexpected-agent-call")); err == nil {
			f.t.Errorf("unexpected agent protocol: %s", data)
		}
	})
}

// A test-only shell transport barrier follows an actual production function's
// successful return. The Go callback observes/mutates real disk before releasing
// it; there is no production hook, helper substitution or timing-based race.
func (f *bootstrapFixture) barrier(function string) (string, func()) {
	f.t.Helper()
	release := filepath.Join(f.project, "release-"+function)
	visible := "TEST boundary " + function
	f.script = strings.Replace(f.script, function+"() {", "fixture_"+function+"() {", 1)
	wrapper := function + `() {
 fixture_` + function + ` "$@" || return $?
 printf '%s\n' ` + shellQuote(visible) + ` >&2
 while [ ! -f ` + shellQuote(release) + ` ]; do sleep 0.01; done
}
`
	f.script = strings.Replace(f.script, "main \"$@\"", wrapper+"\nmain \"$@\"", 1)
	return visible, func() { f.write(release, []byte("release\n"), 0600) }
}
func (f *bootstrapFixture) control() string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(f.home, "Library", "Application Support", "agent-notifications")
	}
	return filepath.Join(f.home, "config", "agent-notifications")
}
func (f *bootstrapFixture) ownership() installruntime.Ledger {
	f.t.Helper()
	l, recovery, err := installruntime.ReadOwnership(f.control())
	if err != nil || recovery {
		f.t.Fatalf("ownership/recovery: %v %v", err, recovery)
	}
	return l
}
func (f *bootstrapFixture) policyEnabled(enabled bool) {
	f.t.Helper()
	l := f.ownership()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Preserve all unrelated registration and route fields using the real CAS.
	_, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: f.control(), RuntimeRoot: l.RuntimeRoot, Owner: l.Owner, ConsumerID: "claude-hooks", RefreshOnly: true, PolicyOnly: true, ExpectedGeneration: &l.Generation, PolicyEnabled: &enabled})
	if err != nil {
		f.t.Fatal(err)
	}
}
func (f *bootstrapFixture) policy() map[string]json.RawMessage {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.control(), "agent-notifications.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err = json.Unmarshal(data, &got); err != nil {
		f.t.Fatal(err)
	}
	return got
}
func requireBootstrapSuccess(t *testing.T, r bootstrapResult) {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("candidate install: code=%d stdout=%s screen=%s", r.code, r.out, r.screen)
	}
}

func (f *bootstrapFixture) portableBindings() []portable.Binding {
	f.t.Helper()
	var result []portable.Binding
	for id, c := range f.ownership().Consumers {
		if strings.HasPrefix(id, "portable:") {
			var b portable.Binding
			if err := json.Unmarshal([]byte(c.Registration), &b); err != nil {
				f.t.Fatal(err)
			}
			result = append(result, b)
		}
	}
	return result
}
func (f *bootstrapFixture) assertPortable(client string) {
	f.t.Helper()
	for _, b := range f.portableBindings() {
		if string(b.Integration) == client {
			return
		}
	}
	f.t.Fatalf("%s owned portable consumer missing: %+v", client, f.ownership().Consumers)
}
func (f *bootstrapFixture) assertNoPortable(client string) {
	f.t.Helper()
	for _, b := range f.portableBindings() {
		if string(b.Integration) == client {
			f.t.Fatalf("unexpected %s binding %+v", client, b)
		}
	}
	if client == "codex" {
		if _, err := os.Stat(filepath.Join(f.home, "codex", "skills", "agent-notify")); !os.IsNotExist(err) {
			f.t.Fatalf("unselected Codex skill appeared: %v", err)
		}
	}
}

// Regression: false/false survived display but configure replaced it with its
// defaults; a rerun with preserve-policy silently enabled a saved disabled route.
func testBootstrapAppliedPolicy(t *testing.T) {
	f := newBootstrapFixture(t)
	f.releaseAssets()
	requireBootstrapSuccess(t, f.noTTY("--product", "claude", "--skip-agent-notify"))
	f.captureIntent()
	r := f.terminal([]string{"--plain", "--agent-notify", "--navigation", "none", "--allow-unknown-caller", "false", "--allow-caller-asserted", "false"}, promptStep{"comma-separated", "claude\n", nil}, promptStep{"[y/n]", "y\n", nil})
	requireBootstrapSuccess(t, r)
	t.Logf("visible consent/apply: %s", r.screen)
	intent := f.confirmedIntent()
	var request map[string]json.RawMessage
	if err := json.Unmarshal(intent["request"], &request); err != nil {
		t.Fatal(err)
	}
	t.Logf("immutable typed request: %s", mustFixtureJSON(t, request))
	if !strings.Contains(string(request["Configure"]), `"allowUnknownCaller":false`) || !strings.Contains(string(request["Configure"]), `"allowCallerAsserted":false`) {
		t.Fatalf("frozen caller booleans lost: %s", mustFixtureJSON(t, request))
	}
	p := f.policy()
	t.Logf("actual policy: %s", mustFixtureJSON(t, p))
	var route map[string]json.RawMessage
	if err := json.Unmarshal(p["route"], &route); err != nil {
		t.Fatal(err)
	}
	if string(route["allowUnknownCaller"]) != "false" || string(route["allowCallerAsserted"]) != "false" || string(route["localRouting"]) != "false" {
		t.Fatalf("caller booleans lost: %s", mustFixtureJSON(t, p))
	}
	f.policyEnabled(false)
	before := f.policy()
	r = f.terminal([]string{"--plain"}, promptStep{"comma-separated", "claude\n", nil}, promptStep{"[y/n]", "y\n", nil})
	requireBootstrapSuccess(t, r)
	after := f.policy()
	for _, key := range []string{"enabled", "route"} {
		if string(before[key]) != string(after[key]) {
			t.Fatalf("preserved %s changed: %s -> %s", key, before[key], after[key])
		}
	}
}
func mustFixtureJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func testBootstrapLegacyPartial(t *testing.T) {
	f := newBootstrapFixture(t)
	f.releaseAssets()
	// After real Codex registration returned, an outside config replacement
	// makes the real config initializer fail. No permissions or actual agent.
	f.script = strings.Replace(f.script, "initialize_config() {", "fixture_initialize_config() {", 1)
	wrapper := `initialize_config() {
 if [ "${_FIXTURE_INIT_COUNT:-0}" = 1 ]; then
  mv "$AGENT_NOTIFICATIONS_CONFIG" "$AGENT_NOTIFICATIONS_CONFIG.TEST-retained"
  mkdir "$AGENT_NOTIFICATIONS_CONFIG"
 fi
 _FIXTURE_INIT_COUNT=$((${_FIXTURE_INIT_COUNT:-0}+1))
 fixture_initialize_config "$@"
}
`
	f.script = strings.Replace(f.script, "main \"$@\"", wrapper+"\nmain \"$@\"", 1)
	// Explicit global config is existing legacy grammar, not a selector override.
	f.env = append(f.env, "AGENT_NOTIFICATIONS_CONFIG="+filepath.Join(f.home, "config", "TEST-global.json"))
	r := f.noTTY("--products", "claude,codex,gemini", "--skip-agent-notify", "--webhook")
	if r.code == 0 || !strings.Contains(r.screen, "claude: completed") || !strings.Contains(r.screen, "codex: registration_committed; follow_up_failed:config") || !strings.Contains(r.screen, "gemini: not_started") {
		t.Fatalf("untruthful singleton partial: %+v", r)
	}
	f.assertConsumer("claude-hooks", true)
	f.assertConsumer("codex:"+filepath.Join(f.home, "codex", "hooks.json"), true)
	f.assertConsumer("gemini-notifications", false)
	if _, err := os.Stat(filepath.Join(f.home, "codex", "hooks.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.home, "claude", "plugins", "installed_plugins.json")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(f.project, "probes"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), `claude ["plugin", "install"`) != 1 || strings.Contains(r.screen, "failed:both") {
		t.Fatalf("completed Claude rerun or collapsed report: %s %+v", data, r)
	}
}

// Implemented at the actual portable Plan/Run + kernel boundary below; no
// additional shell protocol or production-only timing hook is required.
func testBootstrapPostAdmissionCAS(t *testing.T) {
	f := newBootstrapFixture(t)
	f.releaseAssets()
	requireBootstrapSuccess(t, f.noTTY("--product", "claude", "--skip-agent-notify"))
	testFixtureProtectedPortableMutation(t, f)
}

// Regression: a ready portable plan captured an admitted snapshot, but an
// external change was silently adopted between that admission and mutation.
// Plan/Run and CommitBinding are the existing protected boundaries. Shell has
// no timing hook at the writer's lock, so use these actual exported transports.
func testFixtureProtectedPortableMutation(t *testing.T, f *bootstrapFixture) {
	for _, entry := range f.env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			t.Setenv(key, value)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	off, on := false, true
	req := setupwizard.Request{Action: setupwizard.ActionInstall, Agents: []string{"claude"}, Hooks: &off, AgentNotify: &on, Yes: true,
		ControlRoot: f.control(), RuntimeRoot: f.ownership().RuntimeRoot, GlobalConfig: filepath.Join(f.home, "config", "TEST-global.json"),
		ClaudeConfig: filepath.Join(f.home, "claude"), CodexHome: filepath.Join(f.home, "codex"), ClientExecutable: filepath.Join(f.tools, "claude"),
		PackageRoot: filepath.Join(f.assets, "portable"), Helper: f.binary, ScopeRoot: f.home}
	plan, err := setupwizard.Plan(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("actual portable admission: %+v %v", plan, err)
	}
	projection, generation, err := setupwizard.ObserveBootstrapMCP(ctx, plan.Request)
	if err != nil {
		t.Fatal(err)
	}
	frozen := setupwizard.BootstrapMCPSelection{Selected: []string{"claude"}, Projection: projection, AllowPolicySeed: !projection.PolicyPresent, SeedEnabled: true}
	plan.Request.BootstrapMCP = &frozen
	plan.Request.BootstrapExpectedGeneration = &generation
	f.policyEnabled(false)
	before := f.snapshot()
	got, err := setupwizard.Run(ctx, plan.Request)
	if err == nil || got.Reason != "concurrent_change" {
		t.Fatalf("admitted stale Plan/Run mutated: %+v %v", got, err)
	}
	f.samePersistentState(before)
	f.assertNoPortable("claude")
	// Exercise the late writer's generation CAS independently of the command's
	// early refusal, using the real ready plan's reserved binding identity.
	l := f.ownership()
	dataRoot := filepath.Join(f.project, "TEST admitted portable data")
	if err = os.Mkdir(dataRoot, 0700); err != nil {
		t.Fatal(err)
	}
	b := portable.Binding{Version: 1, Integration: portable.Claude, InstallationID: plan.Request.InstallationID, BindingID: plan.Request.BindingIDs["claude"], ScopeID: "user", ComponentID: l.ID, Owner: l.Owner, ScopeRoot: f.home, DataRoot: dataRoot, ControlRoot: f.control(), RuntimeRoot: l.RuntimeRoot, GlobalConfig: req.GlobalConfig, Primary: portable.PlatformPrimary()}
	_, err = (portablesetup.Service{}).CommitBinding(ctx, portablesetup.Request{Binding: b, ExpectedGeneration: generation})
	if err == nil || !strings.Contains(err.Error(), "stale installation generation") {
		t.Fatalf("late binding CAS accepted drift or failed before CAS: %v", err)
	}
	f.samePersistentState(before)
	f.assertNoPortable("claude")
	f.assertNoPortable("codex")
	t.Log("executed real portable Plan/Run admission and late CommitBinding generation CAS; no shell-only production hook")
}

func (f *bootstrapFixture) captureIntent() {
	f.t.Helper()
	f.script = strings.Replace(f.script, "load_frozen_dispatch() {", "fixture_load_frozen_dispatch() {", 1)
	wrapper := `load_frozen_dispatch() {
 fixture_load_frozen_dispatch "$@" || return $?
 cp "$_SELECTOR_INTENT" ` + shellQuote(filepath.Join(f.project, "TEST confirmed intent.json")) + `
}
`
	f.script = strings.Replace(f.script, "main \"$@\"", wrapper+"\nmain \"$@\"", 1)
}
func (f *bootstrapFixture) confirmedIntent() map[string]json.RawMessage {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.project, "TEST confirmed intent.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	var intent map[string]json.RawMessage
	if err = json.Unmarshal(data, &intent); err != nil {
		f.t.Fatal(err)
	}
	return intent
}
