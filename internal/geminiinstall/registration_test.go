package geminiinstall

import (
	"bytes"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"os"
	"testing"
)

// A foreign settings edit must keep the receipt usable, while an own-group or
// runtime edit and a stale binding/generation must deny an effect immediately.
func TestRegistrationOwnsGroupsAndRuntimeOnly(t *testing.T) {
	ctx, r, settings := installFixture(t)
	if err := Apply(ctx, r); err != nil {
		t.Fatal(err)
	}
	s, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	receipt, _, err := readReceipt(r.ControlRoot, s.Installation.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	binary := s.Installation.Ledger.Consumers[consumerID].Commands[0]
	valid := func(binding string) bool {
		return RegisteredFromSnapshot(s, r.ControlRoot, binary, binding, r.GOOS, r.GOARCH)
	}
	if !valid(receipt.Binding) || valid("different-binding") {
		t.Fatal("binding was not enforced")
	}
	d, w := ChannelsFromSnapshot(s, r.ControlRoot, binary, receipt.Binding, r.GOOS, r.GOARCH)
	if d || !w {
		t.Fatal("independent Gemini consent not applied")
	}
	original, _ := os.ReadFile(settings)
	foreign := bytes.Replace(original, []byte("printf foreign"), []byte("printf changed"), 1)
	if bytes.Equal(original, foreign) {
		t.Fatal("fixture did not edit foreign command")
	}
	if err := os.WriteFile(settings, foreign, 0600); err != nil {
		t.Fatal(err)
	}
	if !valid(receipt.Binding) {
		t.Fatal("foreign group invalidated registration")
	}
	edited := bytes.Replace(foreign, []byte("agent-notifications-gemini-after-agent"), []byte("foreign-replaced-owner"), 1)
	if err := os.WriteFile(settings, edited, 0600); err != nil {
		t.Fatal(err)
	}
	if valid(receipt.Binding) {
		t.Fatal("owned group drift admitted")
	}
	if err := os.WriteFile(settings, foreign, 0600); err != nil {
		t.Fatal(err)
	}
	if err := RevokeChannels(ctx, r.ControlRoot, r.RuntimeRoot); err != nil {
		t.Fatal(err)
	}
	if SnapshotCurrent(r.ControlRoot, s) {
		t.Fatal("loaded snapshot survived revocation")
	}
	live, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	d, w = ChannelsFromSnapshot(live, r.ControlRoot, binary, receipt.Binding, r.GOOS, r.GOARCH)
	if d || w {
		t.Fatal("revoked channels admitted")
	}
	data, _ := os.ReadFile(binary)
	if err := os.WriteFile(binary, append(data, '!'), 0700); err != nil {
		t.Fatal(err)
	}
	if valid(receipt.Binding) {
		t.Fatal("runtime drift admitted")
	}
}

func TestNativeSettingsRejectsSymlinkWithoutWriting(t *testing.T) {
	_, _, settings := installFixture(t)
	target := settings + ".foreign"
	if err := os.Rename(settings, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, settings); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := readNativeSettings(settings); err == nil {
		t.Fatal("settings symlink followed")
	}
}
