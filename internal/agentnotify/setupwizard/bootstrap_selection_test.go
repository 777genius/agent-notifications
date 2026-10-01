//go:build linux || darwin

package setupwizard

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Red regression: comparing the old whole-ledger generation rejects our own
// unrelated hook registration although the confirmed MCP identity is unchanged.
// Exercise the real read-only observers around actual isolated kernel commits.
func TestBootstrapMCPAdmissionAllowsOwnHookGeneration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	home := t.TempDir()
	control := filepath.Join(home, "control")
	runtimeRoot := filepath.Join(home, "runtime")
	r := Request{Agents: []string{"claude", "codex"}, ControlRoot: control, RuntimeRoot: runtimeRoot,
		ClaudeConfig: filepath.Join(home, "claude"), CodexHome: filepath.Join(home, "codex")}
	before, _, err := ObserveBootstrapMCP(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	expected := BootstrapMCPSelection{Selected: []string{"claude", "codex"}, Projection: before, AllowPolicySeed: true, SeedEnabled: true}
	ledger, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: "TEST-hooks",
		Consumer: installruntime.Consumer{Registration: filepath.Join(runtimeRoot, "hooks.json")}})
	if err != nil {
		t.Fatal(err)
	}
	current, generation, err := ObserveBootstrapMCP(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if generation != ledger.Generation || generation == 0 {
		t.Fatalf("verified generation=%d ledger=%d", generation, ledger.Generation)
	}
	if err := CheckBootstrapMCP(expected, current); err != nil {
		t.Fatalf("own hooks changed MCP admission: %v", err)
	}
	// A policy seed explicitly shown at consent is allowed. A fresh outside
	// disabled policy must not be absorbed as our default-on seed.
	gen := ledger.Generation
	on, off := true, false
	enabled, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: "TEST-hooks", RefreshOnly: true, PolicyOnly: true, ExpectedGeneration: &gen, PolicyEnabled: &on})
	if err != nil {
		t.Fatal(err)
	}
	current, _, err = ObserveBootstrapMCP(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckBootstrapMCP(expected, current); err != nil {
		t.Fatalf("shown seed refused: %v", err)
	}
	gen = enabled.Generation
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: "TEST-hooks", RefreshOnly: true, PolicyOnly: true, ExpectedGeneration: &gen, PolicyEnabled: &off})
	if err != nil {
		t.Fatal(err)
	}
	current, _, err = ObserveBootstrapMCP(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckBootstrapMCP(expected, current); err == nil {
		t.Fatal("external disabled decision silently adopted")
	}
	// The production evaluate/Run guard must reject the same stale projection
	// even with the current generation, not just compare two fresh snapshots.
	currentLedger, _, err := installruntime.ReadOwnership(control)
	if err != nil {
		t.Fatal(err)
	}
	r.Action = ActionInstall
	r.Yes = true
	r.Hooks = &off
	r.AgentNotify = &on
	r.BootstrapMCP = &expected
	r.BootstrapExpectedGeneration = &currentLedger.Generation
	beforeBytes, err := os.ReadFile(filepath.Join(control, "ownership.json"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Run(ctx, r)
	if err == nil || result.Reason != "concurrent_change" {
		t.Fatalf("actual admission adopted opt-out: %+v %v", result, err)
	}
	afterBytes, err := os.ReadFile(filepath.Join(control, "ownership.json"))
	if err != nil || !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatalf("refused admission changed ownership: %v", err)
	}

}

// Red regression: corrupt/recovery state is interpreted as fresh absence and
// the bootstrap auto path acquires new MCP siblings without shown consent.
func TestBootstrapMCPObservationRefusesUnknownState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	home := t.TempDir()
	control := filepath.Join(home, "control")
	if err := os.Mkdir(control, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, "ownership.json"), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ObserveBootstrapMCP(ctx, Request{ControlRoot: control, Agents: []string{"claude", "codex"}}); err == nil {
		t.Fatal("corrupt ownership became fresh")
	}
	if err := os.Remove(filepath.Join(control, "ownership.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, "transaction.json"), []byte("pending"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ObserveBootstrapMCP(ctx, Request{ControlRoot: control, Agents: []string{"claude", "codex"}}); err == nil {
		t.Fatal("pending recovery became fresh")
	}
}
