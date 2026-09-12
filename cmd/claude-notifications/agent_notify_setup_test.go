//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"

	"golang.org/x/sys/unix"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	notifysetup "github.com/777genius/agent-notifications/internal/agentnotify/setup"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func setupCommandTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		st, e := os.Lstat(p)
		if e != nil {
			return e
		}
		v := st.Mode().String()
		if st.Mode().IsRegular() {
			b, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			v += fmt.Sprintf(":%x", sha256.Sum256(b))
		}
		if st.Mode()&os.ModeSymlink != 0 {
			link, e := os.Readlink(p)
			if e != nil {
				return e
			}
			v += link
		}
		out[p] = v
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func setupCommandRoot(t *testing.T) string {
	t.Helper()
	p, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func setupCommandWrite(t *testing.T, p, s string, mode os.FileMode) {
	t.Helper()
	if e := os.WriteFile(p, []byte(s), mode); e != nil {
		t.Fatal(e)
	}
}
func setupCommandRead(t *testing.T, p string) string {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func setupCommandContext(t *testing.T) context.Context {
	t.Helper()
	ctx, c := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(c)
	return ctx
}

type setupCommandFixture struct {
	root, control, runtime, global, command, config, app string
	composition                                          agentNotifySetupComposition
	verifies                                             *int
}

func newSetupCommandFixture(t *testing.T) setupCommandFixture {
	t.Helper()
	root := setupCommandRoot(t)
	f := setupCommandFixture{root: root, control: filepath.Join(root, "control"), runtime: filepath.Join(root, "runtime"), global: filepath.Join(root, "global.json"), config: filepath.Join(root, "client.json"), app: filepath.Join(root, "Chosen.app"), verifies: new(int)}
	f.command = filepath.Join(f.runtime, "bin", "claude-notifications")
	native := filepath.Join(root, "Fixture.app")
	bin := filepath.Join(native, "Contents", "MacOS", "terminal-notifier-modern")
	if e := os.MkdirAll(filepath.Dir(bin), 0700); e != nil {
		t.Fatal(e)
	}
	setupCommandWrite(t, bin, "#!/bin/sh\nexit 97\n", 0700)
	if e := os.Mkdir(f.app, 0700); e != nil {
		t.Fatal(e)
	}
	change, e := installruntime.StageRetainedNative(setupCommandContext(t), f.control, native)
	if e != nil {
		t.Fatal(e)
	}
	// Hosted test-only native qualification, matching completed setup core fixtures.
	change.After.DecoderFloor = 1
	_, e = installruntime.Commit(setupCommandContext(t), installruntime.Request{ControlRoot: f.control, RuntimeRoot: f.runtime, Owner: "existing-installer", ConsumerID: "hooks", Consumer: installruntime.Consumer{Commands: []string{f.command}}, Native: change, Files: []installruntime.File{{Path: f.command, Data: []byte("#!/bin/sh\n# " + installruntime.WriterProtocolMarker + "\nexit 98\n"), Mode: 0700}, {Path: filepath.Join(f.runtime, "hook"), Data: []byte("unchanged hooks"), Mode: 0600}}})
	if e != nil {
		t.Fatal(e)
	}
	setupCommandWrite(t, f.global, `{"foreign":{"keep":true},"notifications":{"desktop":{"enabled":false,"sound":false,"clickToFocus":false}}}`, 0600)
	f.composition.globalConfigPath = func() (string, error) { return f.global, nil }
	f.composition.permission = func(context.Context, string, installruntime.InstalledSnapshot, bool) (string, error) {
		t.Fatal("unexpected permission call")
		return "unavailable", nil
	}
	f.composition.setup = func(o *notifysetup.Options) {
		o.Platform = "darwin"
		o.JournalClock = journal.ClockFunc(func() journal.Sample { return journal.Sample{Boot: "fixture", Seconds: 100, Available: true} })
		o.VerifyApplication = func(ctx context.Context, a notifysetup.Application) error {
			*f.verifies++
			if a.Path != f.app || a.TeamID != "TEAM123456" {
				return fmt.Errorf("fixture mismatch")
			}
			return ctx.Err()
		}
	}
	return f
}
func (f setupCommandFixture) generation(t *testing.T) uint64 {
	t.Helper()
	s, e := installruntime.ReadInstalledSnapshot(f.control)
	if e != nil {
		t.Fatal(e)
	}
	return s.Ledger.Generation
}
func (f setupCommandFixture) args(t *testing.T, op string, extra ...string) []string {
	t.Helper()
	a := []string{op, "--control-root", f.control, "--json"}
	if op != "status" && op != "permission-status" {
		a = append(a, "--expected-generation", strconv.FormatUint(f.generation(t), 10))
	}
	return append(a, extra...)
}
func setupCommandRun(t *testing.T, ctx context.Context, a []string, c agentNotifySetupComposition, want int) agentNotifySetupResult {
	t.Helper()
	var out bytes.Buffer
	code := agentNotifySetupExecute(ctx, a, &out, c)
	if code != want {
		t.Fatalf("code %d want %d: %s", code, want, &out)
	}
	var r agentNotifySetupResult
	if e := json.Unmarshal(out.Bytes(), &r); e != nil {
		t.Fatalf("%s: %v", &out, e)
	}
	return r
}
func (f setupCommandFixture) route() []string {
	return []string{"--global-config", f.global, "--app", f.app, "--team-id", "TEAM123456", "--allow-unknown-caller", "true", "--allow-caller-asserted", "false"}
}

func TestSetupNotificationsLifecycle(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			f := newSetupCommandFixture(t)
			ctx := setupCommandContext(t)
			foreign := `{"hooks":{"Stop":[{"command":"foreign-hook"}]},"mcpServers":{"foreign":{"command":"foreign-command"}},"secret":"do-not-print"}`
			if provider == "codex" {
				foreign = "secret = \"do-not-print\"\n[hooks]\nStop = \"foreign-hook\"\n[mcp_servers.foreign]\ncommand = \"foreign-command\"\n"
			}
			setupCommandWrite(t, f.config, foreign, 0600)
			originalGlobal := setupCommandRead(t, f.global)
			originalHook := setupCommandRead(t, filepath.Join(f.runtime, "hook"))
			client := []string{"--provider", provider, "--config", f.config, "--command", f.command}
			r := setupCommandRun(t, ctx, f.args(t, "register", client...), f.composition, 0)
			if r.ExplicitIntent || r.RuntimeEligible || r.Activation != "activation_required" || *f.verifies != 0 {
				t.Fatalf("registration auto-enabled: %+v", r)
			}
			before := setupCommandTree(t, f.root)
			setupCommandRun(t, ctx, f.args(t, "register", client...), f.composition, 0)
			if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
				t.Fatal("repeat registration changed state")
			}
			r = setupCommandRun(t, ctx, f.args(t, "enable", f.route()...), f.composition, 0)
			if !r.ExplicitIntent || !r.RuntimeEligible || *f.verifies < 2 || r.Permission != "not_checked" {
				t.Fatalf("enable: %+v, verifies %d", r, *f.verifies)
			}
			before = setupCommandTree(t, f.root)
			r = setupCommandRun(t, ctx, f.args(t, "status", "--global-config", f.global), f.composition, 0)
			if !r.ExplicitIntent || r.DesktopEnabled == nil || *r.DesktopEnabled || r.Configuration != "configured" || r.Permission != "not_checked" {
				t.Fatalf("status: %+v", r)
			}
			if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
				t.Fatal("status changed state")
			}
			// Omitted route must preserve route, consent, rates and durable namespace.
			s, e := installruntime.ReadPolicySnapshot(ctx, f.control)
			if e != nil {
				t.Fatal(e)
			}
			setupCommandRun(t, ctx, f.args(t, "enable", "--global-config", f.global), f.composition, 0)
			after, e := installruntime.ReadPolicySnapshot(ctx, f.control)
			if e != nil {
				t.Fatal(e)
			}
			for _, k := range []string{"route", "rates", "setupState"} {
				if !bytes.Equal(s.Fields[k], after.Fields[k]) {
					t.Fatalf("enable reset %s", k)
				}
			}
			// Ordinary registration after opt-in must preserve the intent too.
			r = setupCommandRun(t, ctx, f.args(t, "register", client...), f.composition, 0)
			if !r.ExplicitIntent {
				t.Fatal("register revoked intent")
			}
			if setupCommandRead(t, f.global) != originalGlobal {
				t.Fatal("global settings changed")
			}
			count := *f.verifies
			// Revocation must succeed with unavailable app, canonical config and journal.
			if e := os.RemoveAll(f.app); e != nil {
				t.Fatal(e)
			}
			if e := os.Remove(f.global); e != nil {
				t.Fatal(e)
			}
			if e := os.Rename(filepath.Join(f.control, "state"), filepath.Join(f.control, "state-hidden")); e != nil {
				t.Fatal(e)
			}
			r = setupCommandRun(t, ctx, f.args(t, "disable"), f.composition, 0)
			if r.ExplicitIntent || r.RuntimeEligible || *f.verifies != count {
				t.Fatalf("disable: %+v", r)
			}
			if !strings.Contains(setupCommandRead(t, f.config), "agent_notifications") {
				t.Fatal("disable removed MCP registration")
			}
			setupCommandRun(t, ctx, f.args(t, "remove", client...), f.composition, 0)
			config := setupCommandRead(t, f.config)
			for _, keep := range []string{"foreign-hook", "foreign-command", "do-not-print"} {
				if !strings.Contains(config, keep) {
					t.Fatalf("lost %s", keep)
				}
			}
			if setupCommandRead(t, filepath.Join(f.runtime, "hook")) != originalHook {
				t.Fatal("hook changed")
			}
			// The canonical global bytes were never modified before intentional test removal.
			if originalGlobal != `{"foreign":{"keep":true},"notifications":{"desktop":{"enabled":false,"sound":false,"clickToFocus":false}}}` {
				t.Fatal("bad fixture")
			}
		})
	}
}

func TestSetupNotificationsParserNoEffects(t *testing.T) {
	root := setupCommandRoot(t)
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	invalid := [][]string{
		{}, {"permission"}, {"enable"}, {"disable", "--expected-generation", "0"},
		{"status", "--expected-generation", "1"}, {"status", "--help", "--oops"},
		{"status", "--json", "--json"}, {"status", "--control-root", "relative"},
		{"register", "--expected-generation", "1", "--provider", "desktop", "--config", "/a", "--command", "/b"},
		{"register", "--expected-generation", "1", "--provider", "codex"},
		{"enable", "--expected-generation", "1", "--app", "/A.app", "--team-id", "TEAM123456"},
		{"disable", "--expected-generation", "1", "--allow-unknown-caller", "true"},
		{"status", "--control-root", "/a\nsecret"}, {"status", "--control-root", strings.Repeat("x", 4097)},
		{"enable", "--expected-generation", "1", "--expected-generation", "2"},
	}
	route := []string{"enable", "--expected-generation", "1", "--app", "/A.app", "--team-id", "TEAM123456", "--allow-unknown-caller", "true", "--allow-caller-asserted", "false"}
	for _, replacement := range []struct {
		index int
		value string
	}{{4, "/A.txt"}, {6, "badteam!!!"}, {8, "yes"}, {10, "1"}} {
		a := append([]string(nil), route...)
		a[replacement.index] = replacement.value
		invalid = append(invalid, a)
	}
	before := setupCommandTree(t, root)
	for _, a := range invalid {
		var out bytes.Buffer
		if code := agentNotifySetupExecute(context.Background(), a, &out, agentNotifySetupComposition{setup: func(*notifysetup.Options) { t.Fatal("composition on invalid args") }}); code != 2 {
			t.Fatalf("%q: code %d %s", a, code, &out)
		}
	}
	for _, a := range [][]string{{"--help"}, {"enable", "--help"}, {"status", "--help"}} {
		var out bytes.Buffer
		if agentNotifySetupExecute(nil, a, &out, agentNotifySetupComposition{}) != 0 || !strings.Contains(out.String(), "activation-required") {
			t.Fatal("help")
		}
	}
	if !reflect.DeepEqual(before, setupCommandTree(t, root)) {
		t.Fatal("help/invalid created state")
	}
	if _, _, e := parseAgentNotifySetup(route); e != nil {
		t.Fatal(e)
	}
}

func TestSetupNotificationsStaleCanceledMissing(t *testing.T) {
	f := newSetupCommandFixture(t)
	ctx := setupCommandContext(t)
	stale := f.args(t, "disable")
	stale[len(stale)-1] = "999"
	before := setupCommandTree(t, f.root)
	r := setupCommandRun(t, ctx, stale, f.composition, 1)
	if r.Reason != "generation_changed" {
		t.Fatal(r)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	r = setupCommandRun(t, canceled, f.args(t, "enable", f.route()...), f.composition, 1)
	if r.Reason != "canceled" {
		t.Fatal(r)
	}
	r = setupCommandRun(t, ctx, []string{"status", "--control-root", filepath.Join(f.root, "missing"), "--json"}, f.composition, 1)
	if r.Reason != "managed_runtime_required" {
		t.Fatal(r)
	}
	if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
		t.Fatal("failure changed state")
	}
}

func TestSetupNotificationsDispatch(t *testing.T) {
	if os.Getenv("SETUP_COMMAND_DISPATCH_CHILD") == "1" {
		os.Args = append([]string{"claude-notifications"}, strings.Split(os.Getenv("SETUP_COMMAND_DISPATCH_ARGS"), " ")...)
		main()
		return
	}
	root := setupCommandRoot(t)
	before := setupCommandTree(t, root)
	for _, tc := range []struct {
		args     string
		code     int
		contains string
	}{{"setup-notifications --help", 0, "Usage: claude-notifications setup-notifications"}, {"setup-notifications enable --oops", 2, "invalid_arguments"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSetupNotificationsDispatch$")
		cmd.Env = append(os.Environ(), "SETUP_COMMAND_DISPATCH_CHILD=1", "SETUP_COMMAND_DISPATCH_ARGS="+tc.args, "HOME="+root, "XDG_CONFIG_HOME="+root, "CODEX_HOME="+root)
		out, e := cmd.CombinedOutput()
		cancel()
		code := 0
		if e != nil {
			if ee, ok := e.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				t.Fatal(e)
			}
		}
		if code != tc.code || !strings.Contains(string(out), tc.contains) {
			t.Fatalf("dispatch %d %s %v", code, out, e)
		}
	}
	if !reflect.DeepEqual(before, setupCommandTree(t, root)) {
		t.Fatal("dispatch created state")
	}
}

func TestSetupNotificationsRestrictionsAndConflict(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			f := newSetupCommandFixture(t)
			ctx := setupCommandContext(t)
			client := []string{"--provider", provider, "--config", f.config, "--command", f.command}
			setupCommandRun(t, ctx, f.args(t, "register", client...), f.composition, 0)
			original := setupCommandRead(t, f.config)
			if provider == "codex" {
				original += "enabled = false\ndisabled_tools = ['notify']\nstartup_timeout_sec = 17\ncustom = 'keep'\n[mcp_servers.agent_notifications.env]\nKEY = 'private-value'\n"
			} else {
				original = strings.Replace(original, `"command":`, `"enabled": false, "disabled_tools": ["notify"], "timeout": 17, "custom": "keep", "env": {"KEY":"private-value"}, "command":`, 1)
			}
			setupCommandWrite(t, f.config, original, 0600)
			before := setupCommandTree(t, f.root)
			r := setupCommandRun(t, ctx, f.args(t, "register", client...), f.composition, 0)
			if r.ExplicitIntent || !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
				t.Fatal("repeat changed restrictions/intent")
			}
			// A second managed executable is installed by the real kernel fixture; the CLI
			// only updates the exact client transport and retains every restriction.
			next := filepath.Join(f.runtime, "next-server")
			g := f.generation(t)
			_, e := installruntime.Commit(ctx, installruntime.Request{ControlRoot: f.control, RuntimeRoot: f.runtime, Owner: "existing-installer", ConsumerID: "hooks", RefreshOnly: true, ExpectedGeneration: &g, Files: []installruntime.File{{Path: next, Data: []byte("inert never launched"), Mode: 0700}}})
			if e != nil {
				t.Fatal(e)
			}
			client[len(client)-1] = next
			setupCommandRun(t, ctx, f.args(t, "register", client...), f.composition, 0)
			actual := setupCommandRead(t, f.config)
			for _, value := range []string{"false", "notify", "17", "keep", "private-value", next} {
				if !strings.Contains(actual, value) {
					t.Fatalf("lost restriction %s", value)
				}
			}
			// User command override must conflict without rollback of the foreign edit.
			setupCommandWrite(t, f.config, strings.Replace(actual, next, "/foreign/overridden-command", 1), 0600)
			before = setupCommandTree(t, f.root)
			for _, op := range []string{"register", "remove"} {
				var out bytes.Buffer
				code := agentNotifySetupExecute(ctx, f.args(t, op, client...), &out, f.composition)
				if code != 1 || !strings.Contains(out.String(), "registration_conflict") || strings.Contains(out.String(), "private-value") {
					t.Fatalf("conflict output: %d %s", code, &out)
				}
			}
			if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
				t.Fatal("conflict changed foreign config")
			}
		})
	}
}

func TestSetupNotificationsVerifierAndCancellation(t *testing.T) {
	f := newSetupCommandFixture(t)
	ctx := setupCommandContext(t)
	before := setupCommandTree(t, f.root)
	// Keep the production verifier, but qualify the platform to reach it on Linux.
	// Darwin uses an injected rejection after verifying the production binding;
	// no real codesign process or application is launched by this test.
	composition := agentNotifySetupComposition{globalConfigPath: f.composition.globalConfigPath, setup: func(o *notifysetup.Options) {
		if reflect.ValueOf(o.VerifyApplication).Pointer() != reflect.ValueOf(verifyAgentNotifyApplication).Pointer() {
			t.Fatal("production verifier not wired")
		}
		o.Platform = "darwin"
		if runtime.GOOS == "darwin" {
			o.VerifyApplication = func(context.Context, notifysetup.Application) error { return fmt.Errorf("offline fixture rejection") }
		}
	}}
	r := setupCommandRun(t, ctx, f.args(t, "enable", f.route()...), composition, 1)
	if r.Reason != "application_identity_invalid" {
		t.Fatal(r)
	}
	if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
		t.Fatal("failed verification provisioned state")
	}
	canceled, cancel := context.WithCancel(ctx)
	defer cancel()
	composition.setup = func(o *notifysetup.Options) {
		f.composition.setup(o)
		o.VerifyApplication = func(context.Context, notifysetup.Application) error { cancel(); return context.Canceled }
	}
	r = setupCommandRun(t, canceled, f.args(t, "enable", f.route()...), composition, 1)
	if r.Reason != "canceled" || !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
		t.Fatal("verification cancellation changed state")
	}
}

func TestSetupNotificationsPhysicalAndInvalidStatus(t *testing.T) {
	f := newSetupCommandFixture(t)
	ctx := setupCommandContext(t)
	alias := filepath.Join(f.root, "alias")
	if e := os.Symlink(f.control, alias); e != nil {
		t.Fatal(e)
	}
	before := setupCommandTree(t, f.root)
	r := setupCommandRun(t, ctx, []string{"status", "--control-root", alias, "--json"}, f.composition, 1)
	if r.Reason != "physical_path_required" || !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
		t.Fatal("symlink path accepted or changed")
	}
	setupCommandRun(t, ctx, f.args(t, "enable", f.route()...), f.composition, 0)
	setupCommandWrite(t, f.global, `{"secret":"DO-NOT-PRINT",broken`, 0600)
	before = setupCommandTree(t, f.root)
	r = setupCommandRun(t, ctx, f.args(t, "status", "--global-config", f.global), f.composition, 1)
	if r.Reason != "configuration_invalid" || !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
		t.Fatal("corrupt status repaired state")
	}
	setupCommandRun(t, ctx, f.args(t, "disable"), f.composition, 0)
}

func TestSetupNotificationsDisableUnavailableNative(t *testing.T) {
	f := newSetupCommandFixture(t)
	ctx := setupCommandContext(t)
	setupCommandRun(t, ctx, f.args(t, "enable", f.route()...), f.composition, 0)
	args := f.args(t, "disable")
	s, e := installruntime.ReadInstalledSnapshot(f.control)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(s.Ledger.Native.Path, s.Ledger.Native.Path+"-unavailable"); e != nil {
		t.Fatal(e)
	}
	r := setupCommandRun(t, ctx, args, f.composition, 0)
	if r.ExplicitIntent || r.RuntimeEligible {
		t.Fatal(r)
	}
}

func TestSetupNotificationsStdioFlagsAndBoundedOutput(t *testing.T) {
	for _, tc := range []struct{ blocked, nonblock bool }{{false, false}, {false, true}, {true, false}, {true, true}} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			read, write, e := agentNotifyBlockingPipe()
			if e != nil {
				t.Fatal(e)
			}
			defer read.Close()
			defer write.Close()
			fd := int(write.Fd())
			if tc.blocked {
				if e = agentNotifyFillPipe(write); e != nil {
					t.Fatal(e)
				}
			}
			if e = unix.SetNonblock(fd, tc.nonblock); e != nil {
				t.Fatal(e)
			}
			initial := agentNotifyFlagsOf(t, write)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSetupNotificationsDispatch$")
			root := setupCommandRoot(t)
			cmd.Env = append(os.Environ(), "SETUP_COMMAND_DISPATCH_CHILD=1", "SETUP_COMMAND_DISPATCH_ARGS=setup-notifications --help", "HOME="+root, "XDG_CONFIG_HOME="+root, "CODEX_HOME="+root)
			cmd.Stdout = write
			cmd.Stderr = write
			e = cmd.Run()
			if tc.blocked {
				if ee, ok := e.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
					t.Fatalf("blocked output: %v", e)
				}
			} else if e != nil {
				t.Fatal(e)
			}
			if ctx.Err() != nil {
				t.Fatal("output was not bounded")
			}
			if actual := agentNotifyFlagsOf(t, write); actual != initial {
				t.Fatalf("flags changed %x -> %x", initial, actual)
			}
		})
	}
}

func TestSetupNotificationsDisableDoesNotResolveGlobal(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(fmt.Sprint(symlink), func(t *testing.T) {
			f := newSetupCommandFixture(t)
			ctx := setupCommandContext(t)
			setupCommandRun(t, ctx, f.args(t, "enable", f.route()...), f.composition, 0)
			if symlink {
				if err := os.Remove(f.global); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), f.global); err != nil {
					t.Fatal(err)
				}
			} else {
				setupCommandWrite(t, f.global, `{broken`, 0600)
			}
			f.composition.globalConfigPath = func() (string, error) { t.Fatal("disable resolved global"); return "", nil }
			r := setupCommandRun(t, ctx, f.args(t, "disable"), f.composition, 0)
			if r.ExplicitIntent || r.RuntimeEligible {
				t.Fatal(r)
			}
		})
	}
}
