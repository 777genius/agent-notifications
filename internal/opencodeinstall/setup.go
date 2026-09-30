// Package opencodeinstall owns the explicit OpenCode notification lifecycle.
package opencodeinstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/opencodeplugin"
	uap "github.com/777genius/plugin-kit-ai/install/integrationctl/opencodeplugin"
)

const (
	consumerID = "opencode-notifications"
	pluginName = "agent-notifications.js"
	binaryName = "claude-notifications-linux-amd64"
)

type Action string

const (
	Install Action = "install"
	Update  Action = "update"
	Remove  Action = "remove"
	Recover Action = "recover"
)

type Request struct {
	Action                                    Action
	ControlRoot, RuntimeRoot, BinarySource    string
	HomeDir, XDGConfigHome, OpenCodeConfigDir string
	Desktop, Webhook                          bool
	// Platform is supplied by the CLI. Tests may qualify a disposable Linux fixture.
	GOOS, GOARCH string
}

func Apply(ctx context.Context, r Request) error {
	if r.GOOS != "linux" || r.GOARCH != "amd64" {
		return errors.New("OpenCode notifications support Linux amd64 only")
	}
	if r.ControlRoot == "" || !filepath.IsAbs(r.ControlRoot) {
		return errors.New("absolute control root required")
	}
	if r.Action == Recover {
		_, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: r.ControlRoot, RecoverOnly: true})
		return err
	}
	if r.Action != Install && r.Action != Update && r.Action != Remove {
		return errors.New("invalid OpenCode action")
	}
	root, err := installruntime.CanonicalPath(r.ControlRoot)
	if err != nil {
		return err
	}
	r.ControlRoot = root
	ledger, recovery, err := installruntime.ReadOwnership(root)
	if err != nil {
		return err
	}
	if recovery {
		return errors.New("installation recovery required; run setup-opencode recover")
	}
	if ledger.ID != "" && ledger.Owner != "existing-installer" {
		return fmt.Errorf("component owned by %s", ledger.Owner)
	}
	previous, registered := ledger.Consumers[consumerID]
	if r.Action == Remove && !registered {
		return nil
	}
	if r.Action == Update && !registered {
		return errors.New("OpenCode consumer is not installed")
	}
	if r.Action != Remove && !r.Desktop && !r.Webhook {
		return errors.New("choose --desktop or --webhook explicitly")
	}
	if r.RuntimeRoot == "" {
		if registered {
			r.RuntimeRoot = previous.RuntimeRoot
		} else if ledger.ID != "" {
			r.RuntimeRoot = ledger.RuntimeRoot
		} else {
			return errors.New("runtime root required")
		}
	}
	r.RuntimeRoot, err = installruntime.CanonicalPath(r.RuntimeRoot)
	if err != nil {
		return err
	}
	if ledger.ID != "" && ledger.RuntimeRoot != r.RuntimeRoot {
		return errors.New("runtime root differs from managed installation")
	}
	if registered && previous.RuntimeRoot != r.RuntimeRoot {
		return errors.New("OpenCode consumer relocation refused")
	}
	binary := filepath.Join(r.RuntimeRoot, binaryName)
	var bundle []byte
	if r.Action != Remove {
		if r.BinarySource == "" || !filepath.IsAbs(r.BinarySource) {
			return errors.New("absolute --binary source required")
		}
		bundle, err = opencodeplugin.Render(binary, root)
		if err != nil {
			return err
		}
	} else {
		// The stored registration remains authoritative when the caller's XDG
		// environment changed since setup. It is never guessed for removal.
		bundle = []byte("removed")
	}
	plugin, err := plan(r, ledger, bundle)
	if err != nil {
		return err
	}
	if registered && previous.Registration != plugin.Target {
		return errors.New("OpenCode config root changed; use the original setup environment")
	}
	if r.Action == Remove {
		return remove(ctx, r, ledger, plugin.Target)
	}
	if plugin.Action == uap.Conflict {
		return fmt.Errorf("OpenCode plugin conflict: %s", plugin.ConflictReason)
	}
	var expectedPolicy *installruntime.Identity
	if !registered {
		policy, err := installruntime.ReadPolicySnapshot(ctx, root)
		if err != nil {
			return err
		}
		// A stale/manual true intent must never become active merely because the
		// first transaction publishes a previously absent registration.
		if desktop, webhook := policyChannels(policy.Fields); desktop || webhook {
			return errors.New("stale OpenCode consent must be revoked before installation")
		}
		expectedPolicy = &policy.Preimage
		if _, owned := installruntime.OwnedFile(ledger, plugin.Target); owned {
			return errors.New("OpenCode plugin path is owned by another consumer")
		}
	}
	data, mode, err := readBinary(r.BinarySource)
	if err != nil {
		return err
	}
	beforeBinary, err := installruntime.Fingerprint(binary)
	if err != nil {
		return err
	}
	if beforeBinary.Exists {
		if _, owned := installruntime.OwnedFile(ledger, binary); !owned {
			return errors.New("target binary is foreign")
		}
	}
	beforePlugin, err := installruntime.Fingerprint(plugin.Target)
	if err != nil {
		return err
	}
	files := []installruntime.File{
		{Path: binary, Before: beforeBinary, Data: data, Mode: mode},
		{Path: plugin.Target, Before: beforePlugin, Data: bundle, Mode: 0600},
	}
	gen := ledger.Generation
	_, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: root, Owner: "existing-installer", RuntimeRoot: r.RuntimeRoot,
		ConsumerID: consumerID, Consumer: installruntime.Consumer{Registration: plugin.Target, Commands: []string{binary, "opencode-event", "--protocol", "1"}},
		ExpectedGeneration: &gen, ExpectedPolicy: expectedPolicy, Files: files,
	})
	if err != nil {
		return err
	}
	return setChannels(ctx, root, r.RuntimeRoot, r.Desktop, r.Webhook)
}

func plan(r Request, ledger installruntime.Ledger, desired []byte) (uap.Placement, error) {
	digest := sha256.Sum256(desired)
	base, err := uap.Plan(uap.Input{HomeDir: r.HomeDir, XDGConfigHome: r.XDGConfigHome, Override: r.OpenCodeConfigDir,
		FileName: pluginName, DesiredSHA256: hex.EncodeToString(digest[:])})
	if err != nil {
		return base, err
	}
	id, err := installruntime.Fingerprint(base.Target)
	if err != nil {
		return base, err
	}
	var existing *uap.Existing
	if id.Exists {
		kind := uap.Regular
		if id.Link != "" {
			kind = uap.Symlink
		} else if info, e := os.Lstat(base.Target); e != nil || !info.Mode().IsRegular() {
			kind = uap.Other
		}
		existing = &uap.Existing{Path: base.Target, Kind: kind, SHA256: id.SHA256}
	}
	owned := ""
	if claim, ok := installruntime.OwnedFile(ledger, base.Target); ok {
		owned = claim.SHA256
	}
	return uap.Plan(uap.Input{HomeDir: r.HomeDir, XDGConfigHome: r.XDGConfigHome, Override: r.OpenCodeConfigDir,
		FileName: pluginName, DesiredSHA256: hex.EncodeToString(digest[:]), OwnedSHA256: owned, Existing: existing})
}

func readBinary(path string) ([]byte, uint32, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Size() > 32<<20 {
		return nil, 0, errors.New("binary must be a bounded executable regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, 32<<20+1))
	if err != nil || len(data) > 32<<20 {
		return nil, 0, errors.New("binary read failed or exceeded limit")
	}
	if !installruntime.WriterCompatible(data) {
		return nil, 0, errors.New("binary does not declare the managed writer protocol")
	}
	return data, uint32(info.Mode().Perm() &^ 0022), nil
}

func setChannels(ctx context.Context, root, runtimeRoot string, desktop, webhook bool) error {
	s, err := installruntime.ReadPolicySnapshot(ctx, root)
	if err != nil {
		return err
	}
	if s.Installation.Recovery {
		return errors.New("installation recovery required")
	}
	if _, ok := s.Installation.Ledger.Consumers[consumerID]; !ok {
		return errors.New("OpenCode consumer missing")
	}
	value, _ := json.Marshal(map[string]bool{"desktop": desktop, "webhook": webhook})
	route, _ := json.Marshal(map[string]json.RawMessage{"openCodeNotifications": value})
	gen := s.Installation.Ledger.Generation
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: root, RuntimeRoot: runtimeRoot,
		Owner: "existing-installer", ConsumerID: consumerID, RefreshOnly: true, PolicyOnly: true,
		ExpectedGeneration: &gen, ExpectedPolicy: &s.Preimage,
		PolicyFields: map[string]json.RawMessage{"route": route}})
	return err
}

func remove(ctx context.Context, r Request, ledger installruntime.Ledger, target string) error {
	if err := setChannels(ctx, r.ControlRoot, r.RuntimeRoot, false, false); err != nil {
		return err
	}
	// A shared runtime retains other consumers. Remove this plugin under CAS
	// before deleting its registration; the last consumer is cleaned by kernel.
	if len(ledger.Consumers) > 1 {
		l, _, err := installruntime.ReadOwnership(r.ControlRoot)
		if err != nil {
			return err
		}
		before, ok := installruntime.OwnedFile(l, target)
		if ok {
			gen := l.Generation
			_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot,
				Owner: "existing-installer", ConsumerID: consumerID, RefreshOnly: true, ExpectedGeneration: &gen,
				Files: []installruntime.File{{Path: target, Before: before, Remove: true}}})
			if err != nil {
				return err
			}
		} else if observed, err := installruntime.Fingerprint(target); err != nil || observed.Exists {
			return errors.New("unowned OpenCode plugin path blocks removal")
		}
	}
	l, _, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil {
		return err
	}
	gen := l.Generation
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot,
		Owner: "existing-installer", ConsumerID: consumerID, RemoveConsumer: true, ExpectedGeneration: &gen})
	return err
}

// CurrentGate reads the product registration on each event. No recovery or
// config mutation occurs on this observation path.
type CurrentGate struct{ ControlRoot, Executable, GOOS, GOARCH string }

func (g CurrentGate) Enabled(ctx context.Context) bool {
	desktop, webhook := g.Channels(ctx)
	return desktop || webhook
}

func (g CurrentGate) Channels(ctx context.Context) (bool, bool) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	goos, goarch := runtime.GOOS, runtime.GOARCH
	if g.GOOS != "" {
		goos = g.GOOS
	}
	if g.GOARCH != "" {
		goarch = g.GOARCH
	}
	if goos != "linux" || goarch != "amd64" {
		return false, false
	}
	root := g.ControlRoot
	if root == "" {
		root = os.Getenv("AGENT_NOTIFICATIONS_CONTROL_ROOT")
	}
	if root == "" {
		var err error
		root, err = installruntime.ControlRoot()
		if err != nil {
			return false, false
		}
	}
	s, err := installruntime.ReadPolicySnapshot(ctx, root)
	if err != nil || s.Installation.Recovery {
		return false, false
	}
	c, ok := s.Installation.Ledger.Consumers[consumerID]
	if !ok || c.Registration == "" || filepath.Base(c.Registration) != pluginName || len(c.Commands) != 4 ||
		c.Commands[1] != "opencode-event" || c.Commands[2] != "--protocol" || c.Commands[3] != "1" {
		return false, false
	}
	executable := g.Executable
	if executable == "" {
		executable, err = os.Executable()
		if err != nil {
			return false, false
		}
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return false, false
	}
	command, err := filepath.EvalSymlinks(c.Commands[0])
	if err != nil || command != executable || command != filepath.Join(c.RuntimeRoot, binaryName) {
		return false, false
	}
	if id, ok := installruntime.OwnedFile(s.Installation.Ledger, c.Registration); !ok || !id.Exists {
		return false, false
	}
	if id, ok := installruntime.OwnedFile(s.Installation.Ledger, command); !ok || !id.Exists {
		return false, false
	}
	return policyChannels(s.Fields)
}

func policyChannels(fields map[string]json.RawMessage) (bool, bool) {
	var route map[string]json.RawMessage
	if json.Unmarshal(fields["route"], &route) != nil {
		return false, false
	}
	var channels struct {
		Desktop *bool `json:"desktop"`
		Webhook *bool `json:"webhook"`
	}
	if json.Unmarshal(route["openCodeNotifications"], &channels) != nil || channels.Desktop == nil || channels.Webhook == nil {
		return false, false
	}
	return *channels.Desktop, *channels.Webhook
}

func DefaultRequest(action Action) Request {
	root, _ := installruntime.ControlRoot()
	home, _ := os.UserHomeDir()
	return Request{Action: action, ControlRoot: root, HomeDir: home, XDGConfigHome: strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")),
		OpenCodeConfigDir: strings.TrimSpace(os.Getenv("OPENCODE_CONFIG_DIR")), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
}
