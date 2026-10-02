package geminiinstall

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/geminihooks"
)

// RegisteredFromSnapshot checks installer-owned receipt/runtime identities and
// delegates native group ownership to UAP. Native activation and trust are left
// to Gemini; installation never changes those policies.
func RegisteredFromSnapshot(s installruntime.PolicySnapshot, root, executable, binding, goos, goarch string) bool {
	l := s.Installation.Ledger
	name, supported := binaryName(goos, goarch)
	if !supported || s.Installation.Recovery || l.ID == "" || l.Owner != "existing-installer" {
		return false
	}
	c, ok := l.Consumers[consumerID]
	if !ok || c.RuntimeRoot == "" || len(c.Commands) == 0 {
		return false
	}
	executable, err := filepath.EvalSymlinks(executable)
	if err != nil || executable != filepath.Join(c.RuntimeRoot, name) || c.Commands[0] != executable {
		return false
	}
	r, _, err := readReceipt(root, l)
	if err != nil || (binding != "" && r.Binding != binding) {
		return false
	}
	want, ok := installruntime.OwnedFile(l, executable)
	got, err := installruntime.Fingerprint(executable)
	if err != nil || !ok || !want.Exists || got != want {
		return false
	}
	settings, err := readNativeSettings(r.SettingsPath)
	return err == nil && geminihooks.VerifyOwned(settings, r.Owned) == nil
}

func ChannelsFromSnapshot(s installruntime.PolicySnapshot, root, executable, binding, goos, goarch string) (bool, bool) {
	if binding == "" || !RegisteredFromSnapshot(s, root, executable, binding, goos, goarch) {
		return false, false
	}
	data, err := json.Marshal(s.Fields)
	if err != nil {
		return false, false
	}
	desktop, webhook, err := channels(data)
	return err == nil && desktop, err == nil && webhook
}

// SnapshotCurrent is read-only and never acquires another policy lease. It can
// be used both before an effect and under the effect owner's retained lease.
func SnapshotCurrent(root string, expected installruntime.PolicySnapshot) bool {
	l, recovery, err := installruntime.ReadOwnership(root)
	if err != nil || recovery || l.ID != expected.Installation.Ledger.ID || l.Generation != expected.Installation.Ledger.Generation {
		return false
	}
	_, _, policy, err := readChannels(root)
	if err != nil || policy != expected.Preimage {
		return false
	}
	_, err = os.Lstat(filepath.Join(root, "transaction.json"))
	return os.IsNotExist(err)
}
