//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Public acquisition, selection and confirmation must dispatch the frozen
// Qoder profile/CLI/channels to setup-qoder. No must stop before a native write.
func TestBootstrapPublicQoderConsentAndDispatch(t *testing.T) {
	for _, approved := range []bool{false, true} {
		name := "cancel"
		if approved {
			name = "approve"
		}
		t.Run(name, func(t *testing.T) {
			f := newBootstrapFixture(t)
			received := make(chan []byte, 2)
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				received <- body
				w.WriteHeader(http.StatusOK)
			}))
			defer endpoint.Close()
			config := filepath.Join(f.project, "TEST-qoder-config.json")
			configData := []byte(fmt.Sprintf(`{"schemaVersion":2,"notifications":{"desktop":{"enabled":false},"webhook":{"enabled":false}},"agents":{"qoder":{"notifications":{"webhook":{"enabled":true,"url":%q,"preset":"slack"}}}}}`, endpoint.URL))
			if !json.Valid(configData) {
				t.Fatal("invalid managed Qoder delivery fixture configuration")
			}
			f.write(config, configData, 0600)
			f.env = append(f.env, "AGENT_NOTIFICATIONS_CONFIG="+config)
			profile := filepath.Join(f.home, "TEST selected Qoder profile")
			if err := os.MkdirAll(profile, 0700); err != nil {
				t.Fatal(err)
			}
			f.write(filepath.Join(profile, "settings.json"), []byte("{\"foreign\":\"TEST-preserved\"}\n"), 0600)
			f.env = append(f.env, "QODER_CONFIG_DIR="+profile)
			calls := filepath.Join(f.project, "TEST native Qoder calls")
			// The UAP native CLI contract stays bound to this selected disposable
			// profile for inventory, installation, verification and removal.
			program := filepath.Join(f.tools, "TEST-qoder-native.py")
			f.write(program, []byte(`import json, os, pathlib, shutil, sys
args = sys.argv[1:]
assert args[0] == '--config-dir' and args[2] == 'plugins', args
profile = pathlib.Path(args[1])
assert profile == pathlib.Path(`+strconv.Quote(profile)+`)
assert os.environ['QODER_CONFIG_DIR'] == str(profile)
assert os.environ['HOME'] == os.environ['USERPROFILE']
if args[3] == 'list':
 registry = profile / 'plugins/installed_plugins_v2.json'
 data = json.loads(registry.read_text()) if registry.exists() else {}
 print(json.dumps([{'id': key, 'version': rows[0]['version']} for key, rows in data.get('plugins', {}).items()]))
 sys.exit(0)
if args[3] == 'uninstall':
 assert args[4:] == ['agent-notifications-qoder', '--scope', 'user'], args
 registry = profile / 'plugins/installed_plugins_v2.json'
 data = json.loads(registry.read_text())
 for row in data.get('plugins', {}).get('agent-notifications-qoder@local', []):
  shutil.rmtree(row['installPath'])
 for relative, field in [('plugins/installed_plugins_v2.json', 'plugins'), ('settings.json', 'enabledPlugins')]:
  path = profile / relative
  data = json.loads(path.read_text())
  data[field].pop('agent-notifications-qoder@local', None)
  path.write_text(json.dumps(data) + '\n')
 print(json.dumps({'ok': True, 'pluginId': 'agent-notifications-qoder@local', 'fullyRemoved': True}))
 sys.exit(0)
assert len(args) == 7 and args[3] == 'install' and args[5:] == ['--scope', 'user'], args
source = pathlib.Path(args[4])
manifest = json.loads((source / '.qoder-plugin/plugin.json').read_text())
plugin = 'agent-notifications-qoder@local'
dest = profile / 'plugins/cache/local/agent-notifications-qoder' / manifest['version']
shutil.copytree(source, dest, dirs_exist_ok=True)
registry = profile / 'plugins/installed_plugins_v2.json'
data = json.loads(registry.read_text()) if registry.exists() else {'version': 2}
data.setdefault('plugins', {})[plugin] = [{'scope': 'user', 'version': manifest['version'], 'installPath': str(dest)}]
registry.parent.mkdir(parents=True, exist_ok=True)
registry.write_text(json.dumps(data) + '\n')
settings = profile / 'settings.json'
data = json.loads(settings.read_text()) if settings.exists() else {}
data.setdefault('enabledPlugins', {})[plugin] = True
settings.write_text(json.dumps(data) + '\n')
with open(`+strconv.Quote(calls)+`, 'a') as out:
 out.write(json.dumps(args) + '\n')
print(json.dumps({'ok': True, 'pluginId': plugin, 'path': str(dest)}))
`), 0600)
			f.write(filepath.Join(f.tools, "qodercli"), []byte("#!/usr/bin/env bash\nexec python3 "+shellQuote(program)+" \"$@\"\n"), 0700)
			f.publicJourney()
			before := f.snapshot()
			answer := "n\n"
			if approved {
				answer = "y\n"
			}
			r := f.terminal([]string{"--webhook", "--plain"}, promptStep{"comma-separated", "qoder\n", nil}, promptStep{setupProductsConfirmationTitle, answer, nil})
			if r.code != 0 {
				t.Logf("Qoder installer diagnostics: %s", f.diagnosticText())
			}
			requireBootstrapSuccess(t, r)
			if !strings.Contains(r.screen, "Qoder (CLI present)") || !strings.Contains(r.screen, "Channels: Desktop off, Webhook on") {
				t.Fatalf("Qoder selection or consent omitted: %s", r.screen)
			}
			if !approved {
				f.unchanged(before)
				if _, err := os.Stat(calls); !os.IsNotExist(err) {
					t.Fatalf("canceled Qoder confirmation reached native CLI: %v", err)
				}
				return
			}
			f.assertConsumer("qoder-notifications", true)
			callBytes, err := os.ReadFile(calls)
			if err != nil || strings.Count(string(callBytes), "\n") != 1 {
				t.Fatalf("native Qoder was not dispatched once: %s %v", callBytes, err)
			}
			settings, err := os.ReadFile(filepath.Join(profile, "settings.json"))
			if err != nil || !strings.Contains(string(settings), "TEST-preserved") || !strings.Contains(string(settings), "agent-notifications-qoder@local") {
				t.Fatalf("frozen Qoder profile lost settings/registration: %s %v", settings, err)
			}
			if _, err := os.Stat(filepath.Join(f.home, ".qoder", "settings.json")); !os.IsNotExist(err) {
				t.Fatalf("native install substituted default Qoder profile: %v", err)
			}
			consumer := f.ownership().Consumers["qoder-notifications"]
			marker := "TEST-managed-qoder-completion"
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			event := exec.CommandContext(ctx, consumer.Commands[0], "qoder-event", "Stop", "--control-root", f.control(), "--binding", consumer.Commands[1])
			event.Env, event.Dir, event.Stdin = f.env, f.project, strings.NewReader(fmt.Sprintf(`{"session_id":%q,"hook_event_name":"Stop","last_assistant_message":%q}`, marker, marker))
			eventOut, eventErr := event.CombinedOutput()
			cancel()
			if eventErr != nil {
				t.Fatalf("installed managed hook failed: %s %v", eventOut, eventErr)
			}
			select {
			case body := <-received:
				if !bytes.Contains(body, []byte(marker)) {
					t.Fatalf("managed delivery lost event: %s", body)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("installed UAP-bound managed hook did not deliver its selected webhook")
			}
			assertQoderStalledInputAllowsRemove(t, f, profile)
		})
	}
}

// Write more than the socket's send buffer without closing stdin. Successful
// writing proves the real installed hook consumed input, eliminating a startup
// timing guess. Its producer then remains stalled while removal takes its lock.
func assertQoderStalledInputAllowsRemove(t *testing.T, f *bootstrapFixture, profile string) {
	t.Helper()
	c := f.ownership().Consumers["qoder-notifications"]
	if len(c.Commands) != 2 {
		t.Fatalf("Qoder command binding missing: %+v", c)
	}
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	syscall.CloseOnExec(fds[0])
	syscall.CloseOnExec(fds[1])
	writer := os.NewFile(uintptr(fds[0]), "TEST-stalled-producer")
	reader := os.NewFile(uintptr(fds[1]), "TEST-hook-input")
	defer func() { _ = writer.Close() }()
	defer func() { _ = reader.Close() }()
	if err = syscall.SetsockoptInt(fds[0], syscall.SOL_SOCKET, syscall.SO_SNDBUF, 4096); err != nil {
		t.Fatal(err)
	}
	size, err := syscall.GetsockoptInt(fds[0], syscall.SOL_SOCKET, syscall.SO_SNDBUF)
	if err != nil {
		t.Fatal(err)
	}
	hook := exec.Command(c.Commands[0], "qoder-event", "Stop", "--control-root", f.control(), "--binding", c.Commands[1])
	hook.Env = f.env
	hook.Dir = f.project
	hook.Stdin = reader
	var hookOut bytes.Buffer
	hook.Stdout = &hookOut
	hook.Stderr = &hookOut
	if err = hook.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close(); _ = hook.Process.Kill(); _ = hook.Wait() }()
	_ = reader.Close()
	written := make(chan error, 1)
	go func() {
		_, writeErr := fmt.Fprint(writer, `{"session_id":"TEST-stalled","hook_event_name":"Stop","last_assistant_message":"`+strings.Repeat("x", size*2))
		written <- writeErr
	}()
	select {
	case err = <-written:
		if err != nil {
			t.Fatalf("hook did not consume its input: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("hook did not read beyond the finite input buffer")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	remove := exec.CommandContext(ctx, f.binary, "setup-qoder", "remove", "--control-root", f.control(), "--config-root", profile, "--qoder-executable", filepath.Join(f.tools, "qodercli"))
	remove.Env = f.env
	remove.Dir = f.project
	output, err := remove.CombinedOutput()
	if err != nil {
		t.Fatalf("stalled hook blocked removal: %v %s", err, output)
	}
	f.assertConsumer("qoder-notifications", false)
}
