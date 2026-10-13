package geminiinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/geminihooks"
)

const fixtureSettings = `{
 // foreign user comment
 "hooksConfig":{"enabled":false,"disabled":["foreign.blocked"],"notifications":true},
 "security":{"folderTrust":{"enabled":true}},
 "foreign":{"reserved":"keep"},
 "hooks":{"enabled":false,"AfterAgent":[{"hooks":[{"type":"command","name":"foreign.finish","command":"printf foreign"}]}]}
}`

func installFixture(t *testing.T) (context.Context, Request, string) {
	t.Helper()
	if _, ok := binaryName(runtime.GOOS, runtime.GOARCH); !ok {
		t.Skip("unsupported fixture host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	base, err := installruntime.CanonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := Request{Action: Install, ControlRoot: filepath.Join(base, "control"), RuntimeRoot: filepath.Join(base, "runtime"),
		HomeDir: filepath.Join(base, "TEST-profile"), ConfigRoot: filepath.Join(base, "TEST-profile", ".gemini"),
		BinarySource: filepath.Join(base, "candidate"), Webhook: true, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	// A real test executable exercises platform/header/writer checks. These
	// copied bytes are never executed by an agent, shell or notification helper.
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.BinarySource, append(data, []byte("fixture-v1")...), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(r.ConfigRoot, 0700); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(r.ConfigRoot, "settings.json")
	if err := os.WriteFile(settings, []byte(fixtureSettings), 0600); err != nil {
		t.Fatal(err)
	}
	return ctx, r, settings
}

// Whole-settings ownership would reject a foreign edit and delete native
// settings on final cleanup. Exercise the actual host/UAP/kernel boundary.
func TestInstallRepeatUpdateAndSharedRemove(t *testing.T) {
	ctx, r, settings := installFixture(t)
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	l, recovery, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil || recovery {
		t.Fatalf("host registration: %+v %v", l, err)
	}
	if _, owned := installruntime.OwnedFile(l, settings); owned {
		t.Fatal("whole settings became a ledger file")
	}
	ownedReceipt, _, err := readReceipt(r.ControlRoot, l)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(settings)
	if err != nil || geminihooks.VerifyOwned(data, ownedReceipt.Owned) != nil {
		t.Fatalf("owned hook registration: %v", err)
	}
	for _, keep := range []string{"foreign user comment", `"enabled":false`, "foreign.blocked", "folderTrust", "foreign.finish"} {
		if !bytes.Contains(data, []byte(keep)) {
			t.Fatalf("installation lost foreign setting %q", keep)
		}
	}
	if err := installruntime.CheckPrivateControlRoot(filepath.Join(r.ControlRoot, "gemini-observations")); err != nil {
		t.Fatal(err)
	}
	foreign := bytes.Replace(data, []byte(`"reserved":"keep"`), []byte(`"reserved":"changed"`), 1)
	if bytes.Equal(foreign, data) {
		t.Fatal("fixture did not produce a foreign edit")
	}
	if err := os.WriteFile(settings, foreign, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, r); err != nil {
		t.Fatalf("repeat rejected a foreign edit: %v", err)
	}
	repeated, _, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil || repeated.Generation != l.Generation {
		t.Fatalf("identical repeat was not a no-op: %+v %v", repeated, err)
	}
	if got, _ := os.ReadFile(settings); !bytes.Equal(got, foreign) {
		t.Fatal("no-op changed native bytes")
	}
	gen := repeated.Generation
	if _, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot,
		Owner: "existing-installer", ConsumerID: "test-sibling", Consumer: installruntime.Consumer{Registration: filepath.Join(r.RuntimeRoot, "sibling")},
		ExpectedGeneration: &gen, PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"openCodeNotifications":{"desktop":true,"webhook":false}}`)}}); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(r.BinarySource)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.BinarySource, append(source, []byte("fixture-v2")...), 0700); err != nil {
		t.Fatal(err)
	}
	r.Action = Update
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	name, _ := binaryName(r.GOOS, r.GOARCH)
	binary := filepath.Join(r.RuntimeRoot, name)
	if installed, err := os.ReadFile(binary); err != nil || !bytes.HasSuffix(installed, []byte("fixture-v2")) {
		t.Fatalf("actual binary update: %v", err)
	}
	r.Action = Remove
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	l, recovery, err = installruntime.ReadOwnership(r.ControlRoot)
	if err != nil || recovery {
		t.Fatal(err)
	}
	if _, ok := l.Consumers[consumerID]; ok {
		t.Fatal("Gemini registration survived removal")
	}
	if _, ok := l.Consumers["test-sibling"]; !ok {
		t.Fatal("removal removed a sibling")
	}
	if _, err := os.Stat(binary); err != nil {
		t.Fatal("removal removed the shared binary")
	}
	if _, err := os.Lstat(filepath.Join(r.ControlRoot, receiptName)); !os.IsNotExist(err) {
		t.Fatal("own receipt survived cleanup")
	}
	remaining, err := os.ReadFile(settings)
	if err != nil || bytes.Contains(remaining, []byte("agent-notifications-gemini-")) || !bytes.Contains(remaining, []byte("foreign.finish")) || !bytes.Contains(remaining, []byte(`"reserved":"changed"`)) {
		t.Fatalf("cleanup lost settings or retained owned groups: %s %v", remaining, err)
	}
	s, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	var routes map[string]json.RawMessage
	if err := json.Unmarshal(s.Fields["route"], &routes); err != nil {
		t.Fatal(err)
	}
	var gemini, openCode struct{ Desktop, Webhook bool }
	_ = json.Unmarshal(routes["geminiNotifications"], &gemini)
	_ = json.Unmarshal(routes["openCodeNotifications"], &openCode)
	if gemini.Desktop || gemini.Webhook || !openCode.Desktop || openCode.Webhook {
		t.Fatal("removal changed unrelated consent")
	}
}

func TestRemoveRevokesBeforeDamagedReceiptOrOwnedHookConflict(t *testing.T) {
	for _, damage := range []string{"receipt", "owned-group"} {
		t.Run(damage, func(t *testing.T) {
			ctx, r, settings := installFixture(t)
			if err := Apply(ctx, r); err != nil {
				t.Fatal(err)
			}
			if damage == "receipt" {
				if err := os.Remove(filepath.Join(r.ControlRoot, receiptName)); err != nil {
					t.Fatal(err)
				}
			} else {
				data, err := os.ReadFile(settings)
				if err != nil {
					t.Fatal(err)
				}
				edited := bytes.Replace(data, []byte("agent-notifications-gemini-after-agent"), []byte("foreign-owned-name-edit"), 1)
				if bytes.Equal(edited, data) {
					t.Fatal("fixture did not edit the owned group")
				}
				if err := os.WriteFile(settings, edited, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(settings)
			if err != nil {
				t.Fatal(err)
			}
			r.Action = Remove
			if err := Apply(ctx, r); err == nil {
				t.Fatal("damaged receipt/owned group was silently cleaned")
			}
			desktop, webhook, _, err := readChannels(r.ControlRoot)
			if err != nil || desktop || webhook {
				t.Fatalf("cleanup failure left consent active: %v %v %v", desktop, webhook, err)
			}
			if after, _ := os.ReadFile(settings); !bytes.Equal(after, before) {
				t.Fatal("cleanup conflict overwrote settings")
			}
			l, _, err := installruntime.ReadOwnership(r.ControlRoot)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := l.Consumers[consumerID]; !ok {
				t.Fatal("cleanup conflict discarded registration")
			}
		})
	}
}

func TestInstallDoesNotAdoptUnreceiptedNativeGroups(t *testing.T) {
	ctx, r, settings := installFixture(t)
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := RevokeChannels(ctx, r.ControlRoot, r.RuntimeRoot); err != nil {
		t.Fatal(err)
	}
	l, _, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	gen := l.Generation
	if _, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot,
		Owner: "existing-installer", ConsumerID: consumerID, RemoveConsumer: true, ExpectedGeneration: &gen}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, r); !errors.Is(err, geminihooks.ErrConflict) {
		t.Fatalf("unreceipted native groups were adopted: %v", err)
	}
	if got, _ := os.ReadFile(settings); !bytes.Equal(got, data) {
		t.Fatal("adoption refusal changed settings")
	}
}

func TestHostRecoveryKeepsNativeConfigOutsideLedger(t *testing.T) {
	ctx, r, settings := installFixture(t)
	r.Fault = func(phase string) error {
		if phase == "promotion:"+settings {
			return errors.New("synthetic host crash")
		}
		return nil
	}
	if err := Apply(ctx, r); err == nil {
		t.Fatal("host did not exercise transaction recovery")
	}
	if _, recovery, err := installruntime.ReadOwnership(r.ControlRoot); err != nil || !recovery {
		t.Fatalf("missing recovery marker: %v", err)
	}
	r.Action, r.Fault = Recover, nil
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	l, recovery, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil || recovery {
		t.Fatal(err)
	}
	if _, owned := installruntime.OwnedFile(l, settings); owned {
		t.Fatal("recovery adopted the entire config")
	}
	ownedReceipt, _, err := readReceipt(r.ControlRoot, l)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(settings)
	if err != nil || geminihooks.VerifyOwned(data, ownedReceipt.Owned) != nil {
		t.Fatalf("recovery lost the group contract: %v", err)
	}
	desktop, webhook, _, err := readChannels(r.ControlRoot)
	if err != nil || desktop || webhook {
		t.Fatal("recovery enabled channels without explicit setup completion")
	}
	r.Action = Install
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, webhook, _, err := readChannels(r.ControlRoot); err != nil || !webhook {
		t.Fatal("post-recovery setup did not activate selected consent")
	}
}

func TestDefaultPlacementUsesEffectiveGeminiHomeParent(t *testing.T) {
	root, err := installruntime.CanonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GEMINI_CLI_HOME", filepath.Join(root, "TEST-effective-home"))
	r := DefaultRequest(Install)
	r.HomeDir = filepath.Join(root, "TEST-profile")
	path, err := settingsPath(r)
	if err != nil || path != filepath.Join(root, "TEST-effective-home", ".gemini", "settings.json") {
		t.Fatalf("effective home placement: %s %v", path, err)
	}
	r.ConfigRoot = filepath.Join(root, "TEST-explicit-config")
	path, err = settingsPath(r)
	if err != nil || path != filepath.Join(root, "TEST-explicit-config", "settings.json") {
		t.Fatalf("explicit placement: %s %v", path, err)
	}
}

// A preserved disabled product must remain upgradeable without re-enabling it.
func TestDisabledChannelsInstallRepeat(t *testing.T) {
	ctx, r, _ := installFixture(t)
	r.Desktop, r.Webhook = false, false
	for _, action := range []Action{Install, Install, Update} {
		r.Action = action
		if err := Apply(ctx, r); err != nil {
			t.Fatal(err)
		}
		desktop, webhook, _, err := readChannels(r.ControlRoot)
		if err != nil {
			t.Fatal(err)
		}
		if desktop || webhook {
			t.Fatal("disabled channels revived by setup")
		}
	}
}

// Revocation after the outer bootstrap checkpoint must not be overwritten by
// either registration's fresh snapshot or the later channel writer snapshot.
func TestFrozenChannelsRefuseLaterRevocation(t *testing.T) {
	ctx, r, _ := installFixture(t)
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	snapshot, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := installruntime.ObserverChannelPreimage(snapshot.Fields["route"], "geminiNotifications")
	if err != nil {
		t.Fatal(err)
	}
	r.ChannelPreimage = expected
	if err := RevokeChannels(ctx, r.ControlRoot, r.RuntimeRoot); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, r); err == nil {
		t.Fatal("frozen upgrade overwrote later revocation")
	}
	if err := setChannels(ctx, r.ControlRoot, r.RuntimeRoot, r.Desktop, r.Webhook, expected); err == nil {
		t.Fatal("final channel writer overwrote later revocation")
	}
	after, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	var fields struct{ Desktop, Webhook bool }
	var route map[string]json.RawMessage
	if err := json.Unmarshal(after.Fields["route"], &route); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(route["geminiNotifications"], &fields); err != nil || fields.Desktop || fields.Webhook {
		t.Fatalf("revoked decision changed: %+v %v", fields, err)
	}
}
