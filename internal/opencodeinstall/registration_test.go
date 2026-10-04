package opencodeinstall

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Independently renders a private setup artifact, without JS or any runtime.
type registrationFixtureRenderer struct{}

func (registrationFixtureRenderer) RenderRegistration(executable, root, origin string) ([]byte, error) {
	return json.Marshal([]string{"inert E2 renderer contract", executable, root, origin})
}

// Catches origin rotation on update/repair and ledger-wide origin reuse on a
// same-path reinstall where an unrelated consumer keeps the ledger alive.
func TestRegistrationLifecycleWithRetainedSharedLedger(t *testing.T) {
	ctx, r, plugin := fixture(t)
	r.Renderer = registrationFixtureRenderer{}
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	l, _, _ := installruntime.ReadOwnership(r.ControlRoot)
	first := *l.Consumers[consumerID].OpenCode
	var rendered []string
	data, _ := os.ReadFile(plugin)
	if json.Unmarshal(data, &rendered) != nil || rendered[3] != first.Origin || !first.Valid() || !first.OriginBound {
		t.Fatal("rendered origin not bound")
	}
	other := installruntime.Consumer{Registration: filepath.Join(r.RuntimeRoot, "shared-fixture"), Commands: []string{"inert-other-command"}}
	gen := l.Generation
	if _, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot, Owner: l.Owner, ConsumerID: "other-test-consumer", Consumer: other, ExpectedGeneration: &gen}); err != nil {
		t.Fatal(err)
	}
	r.Action = Update
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	// Repair a missing owned bundle: same registration and origin, no new epoch.
	if err := os.Remove(plugin); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	l, _, _ = installruntime.ReadOwnership(r.ControlRoot)
	if !reflect.DeepEqual(first, *l.Consumers[consumerID].OpenCode) {
		t.Fatal("update/repair changed origin")
	}
	lock, err := os.Lstat(filepath.Join(r.ControlRoot, installruntime.OpenCodeStoreLock))
	if err != nil {
		t.Fatal(err)
	}
	r.Action = Remove
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	after, _, _ := installruntime.ReadOwnership(r.ControlRoot)
	if after.ID != l.ID || !reflect.DeepEqual(after.Consumers["other-test-consumer"], otherWithRuntime(other, r.RuntimeRoot)) {
		t.Fatal("shared metadata lost")
	}
	if _, err := os.Lstat(filepath.Join(r.ControlRoot, "opencode-admission", first.Namespace)); !os.IsNotExist(err) {
		t.Fatal("private state retained")
	}
	r.Action = Install
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	l, _, _ = installruntime.ReadOwnership(r.ControlRoot)
	second := l.Consumers[consumerID].OpenCode
	if second.Origin == first.Origin || second.Salt == first.Salt || second.Namespace == first.Namespace || l.ID != after.ID {
		t.Fatal("reinstall reused private registration")
	}
	named, _ := os.Lstat(filepath.Join(r.ControlRoot, installruntime.OpenCodeStoreLock))
	if !os.SameFile(lock, named) {
		t.Fatal("permanent store lock inode replaced")
	}
}

func otherWithRuntime(c installruntime.Consumer, root string) installruntime.Consumer {
	c.RuntimeRoot = root
	return c
}

// Catches event-time origin adoption, corrupted state repair, and accidental
// qualification of the still-supported legacy two-token renderer.
func TestLegacyMigrationAndCorruptPrivateStateFailClosed(t *testing.T) {
	ctx, r, plugin := fixture(t)
	r.Renderer = nil
	// Actual old persisted ownership, with no newly introduced private fields.
	gen := uint64(0)
	binary := filepath.Join(r.RuntimeRoot, linuxTestBinaryName)
	data, _ := os.ReadFile(r.BinarySource)
	if _, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot, Owner: "existing-installer", ConsumerID: consumerID,
		Consumer: installruntime.Consumer{Registration: plugin, Commands: []string{binary, "opencode-event", "--protocol", "1"}}, ExpectedGeneration: &gen,
		Files: []installruntime.File{{Path: binary, Data: data, Mode: 0700}, {Path: plugin, Data: []byte("owned legacy bundle"), Mode: 0600}}}); err != nil {
		t.Fatal(err)
	}
	s, _ := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if lease, err := AcquireRegistration(ctx, r.ControlRoot, s, binary, "linux", "amd64", ""); err == nil {
		lease.Close()
		t.Fatal("originless lease admitted")
	}
	r.Action = Update
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	s, _ = installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	reg := *s.Installation.Ledger.Consumers[consumerID].OpenCode
	if reg.OriginBound {
		t.Fatal("legacy renderer qualified")
	}
	if desktop, webhook := ChannelsFromSnapshot(s, binary, "linux", "amd64"); desktop || webhook {
		t.Fatal("legacy bundle enabled delivery")
	}
	if lease, err := AcquireRegistration(ctx, r.ControlRoot, s, binary, "linux", "amd64", reg.Origin); err == nil {
		lease.Close()
		t.Fatal("originless bundle admitted")
	}
	r.Renderer = registrationFixtureRenderer{}
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	l, _, _ := installruntime.ReadOwnership(r.ControlRoot)
	if l.Consumers[consumerID].OpenCode.Origin != reg.Origin {
		t.Fatal("renderer migration rotated incarnation")
	}
	path := filepath.Join(r.ControlRoot, "opencode-admission", reg.Namespace, "claims.json")
	if err := os.WriteFile(path, []byte(`{"broken":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, r); err == nil {
		t.Fatal("corrupt state repaired into empty claims")
	}
	if err := RevokeChannels(ctx, r.ControlRoot, r.RuntimeRoot); err != nil {
		t.Fatal("private damage blocked revocation", err)
	}
}
