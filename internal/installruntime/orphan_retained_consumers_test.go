package installruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/codexcommand"
)

// The published portable v1 wire contract is independent of the kernel decoder.
// Importing portable here would cycle through its installruntime dependency.
type retainedBindingFixture struct {
	Version        int    `json:"version"`
	Integration    string `json:"integration"`
	InstallationID string `json:"installationID"`
	BindingID      string `json:"bindingID"`
	ScopeID        string `json:"scopeID"`
	ComponentID    string `json:"componentID"`
	Owner          string `json:"owner"`
	ScopeRoot      string `json:"scopeRoot"`
	DataRoot       string `json:"dataRoot"`
	ControlRoot    string `json:"controlRoot"`
	GlobalConfig   string `json:"globalConfig"`
	RuntimeRoot    string `json:"runtimeRoot"`
	Primary        string `json:"primary"`
}

func retainedBindingConsumer(t *testing.T, b retainedBindingFixture) (string, Consumer) {
	t.Helper()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return "portable:" + hex.EncodeToString(sum[:]), Consumer{RuntimeRoot: b.RuntimeRoot, Registration: string(raw), Commands: []string{filepath.Join(b.RuntimeRoot, filepath.FromSlash(b.Primary))}}
}

func newRetainedConsumersFixture(t *testing.T) *orphanFixture {
	t.Helper()
	f := newOrphanFixture(t)
	// StageNative recognizes this TEST fixture offline; the helper must never run.
	f.retainNative(t)
	f.before.Consumers["claude-hooks"] = f.before.Consumers["retained"]
	delete(f.before.Consumers, "retained")
	f.saveLedger(t)
	binary := "claude-notifications-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	realCodex := filepath.Join(f.root, "real-codex", codexcommand.InstallDirName)
	plugin := filepath.Join(f.root, "opencode", "plugins", "agent-notifications.js")
	replacements := []File{
		{Path: filepath.Join(f.primary, binary), Data: []byte("inert legacy OpenCode TEST sender " + WriterProtocolMarker + " " + LocalWriterProtocolMarker), Mode: 0700},
		{Path: plugin, Data: []byte("inert OpenCode TEST registration"), Mode: 0600},
		{Path: filepath.Join(f.primary, "bin", binary), Data: []byte("inert portable TEST primary " + WriterProtocolMarker + " " + LocalWriterProtocolMarker), Mode: 0700},
		{Path: filepath.Join(realCodex, "bin", "codex-hook-wrapper.sh"), Data: []byte("inert retained POSIX wrapper"), Mode: 0700},
		{Path: filepath.Join(realCodex, "bin", "codex-hook-wrapper.cmd"), Data: []byte("inert retained cmd wrapper"), Mode: 0644},
	}
	for i := 0; i < 5; i++ {
		path := filepath.Join(f.primary, fmt.Sprintf("asset-%02d", i))
		replacements = append(replacements, File{Path: path, Before: f.before.Files[path], Remove: true})
	}
	var err error
	f.before, err = Commit(f.ctx, Request{ControlRoot: f.control, RuntimeRoot: f.primary, Owner: "existing-installer", ConsumerID: "claude-hooks", RefreshOnly: true, Files: replacements})
	if err != nil {
		t.Fatal(err)
	}
	register := func(key string, c Consumer) {
		t.Helper()
		f.before, err = Commit(f.ctx, Request{ControlRoot: f.control, RuntimeRoot: c.RuntimeRoot, Owner: "existing-installer", ConsumerID: key, Consumer: c})
		if err != nil {
			t.Fatal(err)
		}
	}
	hooks := filepath.Join(filepath.Dir(realCodex), "hooks.json")
	orphanWrite(t, hooks, "inert retained Codex registration")
	register("codex:"+hooks, Consumer{RuntimeRoot: realCodex, Registration: hooks, Commands: codexcommand.Commands(realCodex)})
	register("opencode-notifications", Consumer{RuntimeRoot: f.primary, Registration: plugin, Commands: []string{filepath.Join(f.primary, binary), "opencode-event", "--protocol", "1"}})
	for _, integration := range []string{"claude", "codex"} {
		b := retainedBindingFixture{1, integration, "TEST-installation", "TEST-" + integration, "user", f.before.ID, "existing-installer", f.control, filepath.Join(f.root, "data", "agent-notify"), f.control, filepath.Join(f.root, "config", "config.json"), f.primary, "bin/" + binary}
		key, c := retainedBindingConsumer(t, b)
		register(key, c)
	}
	if len(f.before.Consumers) != 6 || len(f.before.Files) != 49 || len(f.orphanPaths) != 21 {
		t.Fatalf("lost incident shape: consumers=%d files=%d orphan=%d", len(f.before.Consumers), len(f.before.Files), len(f.orphanPaths))
	}
	if _, err := ReadInstalledSnapshot(f.control); err != nil {
		t.Fatalf("initial fixture unhealthy: %v", err)
	}
	return f
}

func TestOrphanRetainedConsumersActualIncidentRecovery(t *testing.T) {
	f := newRetainedConsumersFixture(t)
	nativeBefore, err := treeFingerprint(f.before.Native.Path)
	if err != nil {
		t.Fatal(err)
	}
	f.abandon(t, true)
	images := orphanControlImages(t, f.control)
	req := f.recoveryRequest()
	p, err := PreviewOrphanConsumer(f.control, req)
	if err != nil || !p.Admissible || p.SelectedCount != 21 || !reflect.DeepEqual(p.SelectedPaths, f.orphanPaths) || !p.NativeValidated || p.NativeIdentityRefresh {
		t.Fatalf("six-consumer incident preview: %+v %v", p, err)
	}
	assertOrphanImages(t, images)
	got, err := RecoverOrphanConsumer(f.ctx, f.control, req)
	if err != nil {
		t.Fatal(err)
	}
	// Exact delta compares every surviving consumer, file and native record.
	assertOrphanDelta(t, f, got, false)
	if len(got.Consumers) != 5 || len(got.Files) != 28 || !reflect.DeepEqual(got.Native, f.before.Native) {
		t.Fatal("retained ownership/native changed")
	}
	policy, err := os.ReadFile(filepath.Join(f.control, "agent-notifications.json"))
	if err != nil || !bytes.Equal(policy, images[filepath.Join(f.control, "agent-notifications.json")]) {
		t.Fatal("raw policy bytes changed", err)
	}
	nativeAfter, err := treeFingerprint(got.Native.Path)
	if err != nil || nativeAfter != nativeBefore {
		t.Fatal("native bytes changed", err)
	}
	s, err := ReadInstalledSnapshot(f.control)
	if err != nil || s.Recovery || !s.Enabled || !reflect.DeepEqual(s.Ledger, got) {
		t.Fatalf("recovered snapshot unhealthy: %+v %v", s, err)
	}
	if _, err := os.Lstat(filepath.Join(f.control, "transaction.json")); !os.IsNotExist(err) {
		t.Fatal("recovery journal remains", err)
	}
}

func TestOrphanRetainedConsumersRejectUnknownOrSharedReferences(t *testing.T) {
	damages := []string{"portable-malformed", "portable-hash", "portable-unknown-field", "portable-noncanonical", "portable-duplicate", "portable-version", "portable-owner", "portable-component", "portable-control", "portable-primary", "portable-command", "portable-shared-registration", "opencode-shared-registration", "opencode-argv", "opencode-untracked", "opencode-private", "unknown-codec"}
	for _, field := range []string{"ScopeRoot", "DataRoot", "ControlRoot", "GlobalConfig", "RuntimeRoot"} {
		damages = append(damages, "portable-shared-"+field)
	}
	for _, damage := range damages {
		t.Run(damage, func(t *testing.T) {
			f := newRetainedConsumersFixture(t)
			f.abandon(t, true)
			if strings.HasPrefix(damage, "portable-") {
				var key string
				var c Consumer
				for k, v := range f.before.Consumers {
					if strings.HasPrefix(k, "portable:") {
						key, c = k, v
						break
					}
				}
				var b retainedBindingFixture
				if err := json.Unmarshal([]byte(c.Registration), &b); err != nil {
					t.Fatal(err)
				}
				delete(f.before.Consumers, key)
				switch damage {
				case "portable-version":
					b.Version = 2
				case "portable-owner":
					b.Owner = "foreign"
				case "portable-component":
					b.ComponentID = "foreign"
				case "portable-control":
					b.ControlRoot = filepath.Join(f.root, "wrong-control")
				case "portable-primary":
					b.Primary = "../escape"
				case "portable-shared-registration":
					b.GlobalConfig = f.registration
				default:
					if strings.HasPrefix(damage, "portable-shared-") {
						reflect.ValueOf(&b).Elem().FieldByName(strings.TrimPrefix(damage, "portable-shared-")).SetString(f.runtime)
					}
				}
				key, c = retainedBindingConsumer(t, b)
				switch damage {
				case "portable-malformed":
					c.Registration = "{"
				case "portable-hash":
					key = "portable:" + strings.Repeat("0", 64)
				case "portable-unknown-field":
					c.Registration = strings.TrimSuffix(c.Registration, "}") + `,"unknownReference":"ignored"}`
				case "portable-noncanonical":
					c.Registration = " " + c.Registration
				case "portable-duplicate":
					c.Registration = strings.TrimSuffix(c.Registration, "}") + `,"version":1}`
				case "portable-command":
					c.Commands = []string{f.orphanPaths[0]}
				}
				if damage != "portable-hash" {
					sum := sha256.Sum256([]byte(c.Registration))
					key = "portable:" + hex.EncodeToString(sum[:])
				}
				f.before.Consumers[key] = c
			} else {
				c := f.before.Consumers["opencode-notifications"]
				switch damage {
				case "opencode-argv":
					c.Commands[3] = "2"
				case "opencode-shared-registration":
					c.Registration = filepath.Join(f.runtime, "agent-notifications.js")
				case "opencode-untracked":
					c.Commands[0] = filepath.Join(f.primary, "untracked-sender")
				case "opencode-private":
					c.OpenCode = &OpenCodeRegistration{}
					f.before.Schema = 4
					f.before.WriterFloor = OpenCodeWriterFloor
				case "unknown-codec":
					delete(f.before.Consumers, "opencode-notifications")
					f.before.Consumers["unknown-observer"] = c
				}
				if damage != "unknown-codec" {
					f.before.Consumers["opencode-notifications"] = c
				}
			}
			f.saveLedger(t)
			images := orphanControlImages(t, f.control)
			req := f.recoveryRequest()
			if p, err := PreviewOrphanConsumer(f.control, req); err == nil || p.Admissible {
				t.Fatalf("unsafe preview admitted: %+v %v", p, err)
			}
			assertOrphanImages(t, images)
			if _, err := RecoverOrphanConsumer(f.ctx, f.control, req); err == nil {
				t.Fatal("unsafe recovery admitted")
			}
			assertOrphanImages(t, images)
		})
	}
}
