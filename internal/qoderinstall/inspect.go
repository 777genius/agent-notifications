package qoderinstall

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"runtime"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notifier"
)

type Status struct {
	Status      string `json:"status"`
	ConfigRoot  string `json:"configRoot"`
	PackageRoot string `json:"packageRoot,omitempty"`
	Desktop     bool   `json:"desktop"`
	Webhook     bool   `json:"webhook"`
}

func Inspect(ctx context.Context, r Request) (Status, error) {
	r, err := normalize(r)
	result := Status{Status: "unavailable", ConfigRoot: r.ConfigRoot}
	if err != nil {
		return result, err
	}
	l, recovery, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil {
		return result, err
	}
	if recovery {
		result.Status = "recovery-required"
		return result, nil
	}
	if _, ok := l.Consumers[ConsumerID]; !ok {
		result.Status = "not-installed"
		return result, nil
	}
	previous, err := readReceipt(r.ControlRoot, l)
	if err != nil {
		return result, err
	}
	result.PackageRoot = previous.PackageRoot
	if previous.ConfigRoot != r.ConfigRoot {
		return result, errors.New("qoder profile differs from managed installation")
	}
	if err = checkFiles(l, previous); err != nil {
		return result, err
	}
	if err = checkUAP(ctx, r, previous); err != nil {
		return result, err
	}
	s, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		return result, err
	}
	result.Desktop, result.Webhook = ChannelsFromSnapshot(s, r.ControlRoot, previous.Executable, previous.Binding)
	result.Status = "installed"
	return result, nil
}

func channels(fields map[string]json.RawMessage) (bool, bool, error) {
	var route map[string]json.RawMessage
	if len(fields["route"]) == 0 {
		return false, false, nil
	}
	if json.Unmarshal(fields["route"], &route) != nil {
		return false, false, errors.New("invalid Qoder channel policy")
	}
	var value struct{ Desktop, Webhook *bool }
	if len(route["qoderNotifications"]) == 0 {
		return false, false, nil
	}
	if json.Unmarshal(route["qoderNotifications"], &value) != nil || value.Desktop == nil || value.Webhook == nil {
		return false, false, errors.New("both Qoder channel consent fields required")
	}
	return *value.Desktop, *value.Webhook, nil
}
func setChannels(ctx context.Context, root, runtimeRoot string, desktop, webhook bool, expected string) error {
	s, err := installruntime.ReadPolicySnapshot(ctx, root)
	if err != nil {
		return err
	}
	if err = installruntime.CheckObserverChannelPreimage(s.Fields["route"], "qoderNotifications", expected); err != nil {
		return err
	}
	if s.Installation.Recovery {
		return errors.New("installation recovery required")
	}
	if _, ok := s.Installation.Ledger.Consumers[ConsumerID]; !ok {
		return errors.New("qoder consumer missing")
	}
	oldD, oldW, err := channels(s.Fields)
	if err != nil {
		return err
	}
	if oldD == desktop && oldW == webhook {
		return nil
	}
	gen := s.Installation.Ledger.Generation
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: root, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: ConsumerID, RefreshOnly: true, PolicyOnly: true, ExpectedGeneration: &gen, ExpectedPolicy: &s.Preimage, PolicyFields: map[string]json.RawMessage{"route": mustJSON(map[string]any{"qoderNotifications": map[string]bool{"desktop": desktop, "webhook": webhook}})}})
	return err
}

// ChannelsFromSnapshot is read-only. The explicit managed entrypoint uses the
// receipt, native registration and executable identity as its consent boundary.
func ChannelsFromSnapshot(s installruntime.PolicySnapshot, root, executable, binding string) (bool, bool) {
	if binding == "" || s.Installation.Recovery || s.Installation.Ledger.Owner != "existing-installer" {
		return false, false
	}
	l := s.Installation.Ledger
	c, ok := l.Consumers[ConsumerID]
	if !ok || len(c.Commands) != 2 || c.Commands[0] != executable || c.Commands[1] != binding {
		return false, false
	}
	r, err := readReceipt(root, l)
	if err != nil || r.Binding != binding {
		return false, false
	}
	if r.Pending || r.Executable != executable || checkFiles(l, r) != nil || checkUAPState(root, r) != nil {
		return false, false
	}
	desktop, webhook, err := channels(s.Fields)
	return err == nil && desktop, err == nil && webhook
}

// ProfileExists is intentionally read-only; discovery never probes the CLI.
func ProfileExists(r Request) bool {
	root, err := ResolveConfigRoot(r)
	if err != nil {
		return false
	}
	info, err := os.Stat(root)
	return err == nil && info.IsDir()
}

// SetupPermission is an explicit macOS setup action, never an event side effect.
func SetupPermission(ctx context.Context, root string, request bool) (string, error) {
	if runtime.GOOS != "darwin" {
		return "unavailable", errors.New("OS notification permission setup requires macOS")
	}
	var err error
	root, err = installruntime.CanonicalPath(root)
	if err != nil {
		return "unavailable", err
	}
	s, err := installruntime.ReadPolicySnapshot(ctx, root)
	if err != nil {
		return "unavailable", err
	}
	r, err := readReceipt(root, s.Installation.Ledger)
	if err != nil {
		return "unavailable", err
	}
	if err = checkFiles(s.Installation.Ledger, r); err != nil {
		return "unavailable", err
	}
	return notifier.SetupPermission(ctx, root, s.Installation, request)
}
