package opencodeinstall

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestRevokeChannelsSurvivesDamagedNativeAndMissingOwnedBinary(t *testing.T) {
	ctx, r, _ := fixture(t)
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	s, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	gen := s.Installation.Ledger.Generation
	enabled := true
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot,
		Owner: "existing-installer", ConsumerID: consumerID, RefreshOnly: true, PolicyOnly: true,
		ExpectedGeneration: &gen, PolicyEnabled: &enabled,
		PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"unrelated":{"keep":true}}`)},
	}); err != nil {
		t.Fatal(err)
	}
	// A legacy native bundle is enough to exercise the integrity gate; no
	// helper is executed or notification sent by this test.
	source := filepath.Join(t.TempDir(), "source.app")
	helper := filepath.Join(source, "Contents", "MacOS", "terminal-notifier-modern")
	if err := os.MkdirAll(filepath.Dir(helper), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, []byte("inert test helper"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(r.ControlRoot, "native"), 0700); err != nil {
		t.Fatal(err)
	}
	change, err := installruntime.StageNative(ctx, r.ControlRoot, source)
	if err != nil {
		t.Fatal(err)
	}
	l, _, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	gen = l.Generation
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot,
		Owner: "existing-installer", ConsumerID: consumerID, RefreshOnly: true,
		ExpectedGeneration: &gen, Native: change,
	}); err != nil {
		t.Fatal(err)
	}
	installedBinary := filepath.Join(r.RuntimeRoot, linuxTestBinaryName)
	if err := os.Remove(installedBinary); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(change.After.Path, "Contents", "MacOS", "terminal-notifier-modern"), []byte("damaged helper"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot); err == nil {
		t.Fatal("damaged assets unexpectedly passed ordinary snapshot verification")
	}
	if err := RevokeChannels(ctx, r.ControlRoot, r.RuntimeRoot); err != nil {
		t.Fatalf("revoke was blocked by damaged delivery assets: %v", err)
	}
	policy, err := installruntime.ReadUserPolicy(r.ControlRoot)
	if err != nil || !policy.Enabled {
		t.Fatalf("unrelated portable enablement changed: %+v %v", policy, err)
	}
	raw, err := os.ReadFile(filepath.Join(r.ControlRoot, "agent-notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fields struct {
		Route struct {
			OpenCodeNotifications struct{ Desktop, Webhook bool } `json:"openCodeNotifications"`
			Unrelated             struct{ Keep bool }             `json:"unrelated"`
		} `json:"route"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if fields.Route.OpenCodeNotifications.Desktop || fields.Route.OpenCodeNotifications.Webhook || !fields.Route.Unrelated.Keep {
		t.Fatalf("revoke changed wrong policy members: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(change.After.Path, "Contents", "MacOS", "terminal-notifier-modern")); err != nil {
		t.Fatal("revoke unexpectedly cleaned damaged native asset")
	}
}

func TestRevokeKernelRejectsAnyEnablingPatch(t *testing.T) {
	ctx, r, _ := fixture(t)
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	s, err := installruntime.ReadRevocationSnapshot(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	_, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot,
		Owner: "existing-installer", ConsumerID: consumerID, RefreshOnly: true, PolicyOnly: true, RevokeOpenCode: true,
		ExpectedGeneration: &s.Generation, ExpectedPolicy: &s.Preimage,
		PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"openCodeNotifications":{"desktop":true,"webhook":false}}`)},
	})
	if err == nil {
		t.Fatal("revocation bypass admitted desktop enablement")
	}
}
