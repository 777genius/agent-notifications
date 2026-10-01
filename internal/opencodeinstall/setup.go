// Package opencodeinstall owns the explicit OpenCode notification lifecycle.
package opencodeinstall

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
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
)

type platform struct {
	goos, goarch, binary string
	format               string
	machine              uint32
}

var supportedPlatforms = []platform{
	{"linux", "amd64", "claude-notifications-linux-amd64", "elf", 62},
	{"linux", "arm64", "claude-notifications-linux-arm64", "elf", 183},
	{"darwin", "amd64", "claude-notifications-darwin-amd64", "macho", 0x01000007},
	{"darwin", "arm64", "claude-notifications-darwin-arm64", "macho", 0x0100000c},
	{"windows", "amd64", "claude-notifications-windows-amd64.exe", "pe", 0x8664},
}

func targetPlatform(goos, goarch string) (platform, bool) {
	for _, p := range supportedPlatforms {
		if p.goos == goos && p.goarch == goarch {
			return p, true
		}
	}
	return platform{}, false
}

type Action string

const (
	Install Action = "install"
	Update  Action = "update"
	Remove  Action = "remove"
	Recover Action = "recover"
)

type Request struct {
	Renderer                                  BundleRenderer
	Action                                    Action
	ControlRoot, RuntimeRoot, BinarySource    string
	NativeSource                              string
	HomeDir, XDGConfigHome, OpenCodeConfigDir string
	Desktop, Webhook                          bool
	// Platform is supplied by the CLI. Tests use disposable platform fixtures.
	GOOS, GOARCH string
}

func Apply(ctx context.Context, r Request) error {
	p, supported := targetPlatform(r.GOOS, r.GOARCH)
	if !supported {
		return errors.New("unsupported OpenCode platform")
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
	if r.NativeSource != "" && (p.goos != "darwin" || !r.Desktop || !filepath.IsAbs(r.NativeSource)) {
		return errors.New("absolute --native-app source requires macOS desktop consent")
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
	binary := filepath.Join(r.RuntimeRoot, p.binary)
	var bundle []byte
	var registration installruntime.OpenCodeRegistration
	if r.Action != Remove {
		if r.BinarySource == "" || !filepath.IsAbs(r.BinarySource) {
			return errors.New("absolute --binary source required")
		}
		registration, err = preparedRegistration(previous.OpenCode)
		if err != nil {
			return err
		}
		if r.Renderer != nil {
			bundle, err = r.Renderer.RenderRegistration(binary, root, registration.Origin)
			registration.OriginBound = true
		} else {
			bundle, err = opencodeplugin.Render(binary, root)
			registration.OriginBound = false
		}
		if err != nil {
			return err
		}
		sum := sha256.Sum256(bundle)
		registration.BundleSHA256 = hex.EncodeToString(sum[:])
	} else {
		// The stored registration remains authoritative when the caller's XDG
		// environment changed since setup. It is never guessed for removal.
		bundle = []byte("removed")
	}
	plugin, plannedPlugin, err := plan(r, ledger, bundle)
	if err != nil {
		return err
	}
	if registered && previous.Registration != plugin.Target {
		return errors.New("OpenCode config root changed; use the original setup environment")
	}
	if r.Action == Remove {
		return remove(ctx, r, plugin.Target)
	}
	if plugin.Action == uap.Conflict {
		return fmt.Errorf("OpenCode plugin conflict: %s", plugin.ConflictReason)
	}
	var expectedPolicy *installruntime.Identity
	if !registered {
		policy, err := installruntime.ReadPolicySnapshot(ctx, root)
		if errors.Is(err, os.ErrNotExist) && ledger.ID == "" {
			// A precreated private control directory has no lock inodes yet.
			// Commit creates them; the absent policy remains a CAS preimage.
			preimage, fingerprintErr := installruntime.Fingerprint(filepath.Join(root, "agent-notifications.json"))
			if fingerprintErr != nil {
				return fingerprintErr
			}
			if !preimage.Exists {
				policy.Preimage = preimage
				policy.Fields = map[string]json.RawMessage{}
				err = nil
			}
		}
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
	data, mode, err := readBinary(r.BinarySource, p)
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
	files := []installruntime.File{
		{Path: binary, Before: beforeBinary, Data: data, Mode: mode},
		{Path: plugin.Target, Before: plannedPlugin, Data: bundle, Mode: 0600},
	}
	if p.goos == "windows" {
		shortcut, shortcutErr := stageWindowsShortcutForSetup(r.HomeDir, binary, r.Desktop, ledger)
		if shortcutErr != nil {
			return shortcutErr
		}
		if shortcut != nil {
			files = append(files, *shortcut)
		}
	}
	var native *installruntime.NativeChange
	if p.goos == "darwin" && r.Desktop {
		if r.NativeSource != "" {
			native, err = installruntime.StageNative(ctx, root, r.NativeSource)
			if err != nil {
				return fmt.Errorf("OpenCode native package verification failed: %w", err)
			}
			defer func() {
				cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = installruntime.DiscardNative(cleanup, root, native)
			}()
			if native.After.DecoderFloor < 1 {
				return errors.New("macOS desktop requires a managed native app with decoder protocol 1")
			}
		} else if ledger.Native == nil || ledger.Native.DecoderFloor < 1 {
			return errors.New("macOS desktop requires a verified managed native app; pass --native-app")
		}
	}
	gen := ledger.Generation
	_, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: root, Owner: "existing-installer", RuntimeRoot: r.RuntimeRoot,
		ConsumerID: consumerID, Consumer: installruntime.Consumer{Registration: plugin.Target, Commands: []string{binary, "opencode-event", "--protocol", "1"}, OpenCode: &registration},
		ExpectedGeneration: &gen, ExpectedPolicy: expectedPolicy, Files: files, Native: native,
	})
	if err != nil {
		return err
	}
	if p.goos == "darwin" && r.Desktop {
		if _, err := PrepareNativeSpool(ctx, root); err != nil {
			return err
		}
	}
	return setChannels(ctx, root, r.RuntimeRoot, r.Desktop, r.Webhook)
}

func plan(r Request, ledger installruntime.Ledger, desired []byte) (uap.Placement, installruntime.Identity, error) {
	digest := sha256.Sum256(desired)
	base, err := uap.Plan(uap.Input{HomeDir: r.HomeDir, XDGConfigHome: r.XDGConfigHome, Override: r.OpenCodeConfigDir,
		FileName: pluginName, DesiredSHA256: hex.EncodeToString(digest[:])})
	if err != nil {
		return base, installruntime.Identity{}, err
	}
	id, err := installruntime.Fingerprint(base.Target)
	if err != nil {
		return base, id, err
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
	placement, err := uap.Plan(uap.Input{HomeDir: r.HomeDir, XDGConfigHome: r.XDGConfigHome, Override: r.OpenCodeConfigDir,
		FileName: pluginName, DesiredSHA256: hex.EncodeToString(digest[:]), OwnedSHA256: owned, Existing: existing})
	return placement, id, err
}

func readBinary(path string, p platform) ([]byte, uint32, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() || (p.goos != "windows" && info.Mode().Perm()&0111 == 0) || info.Size() > 32<<20 {
		return nil, 0, errors.New("binary must be a bounded executable regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, 32<<20+1))
	if err != nil || len(data) > 32<<20 {
		return nil, 0, errors.New("binary read failed or exceeded limit")
	}
	if !installruntime.WriterCompatible(data) {
		return nil, 0, errors.New("binary does not declare the managed writer protocol")
	}
	if !binaryMatchesPlatform(data, p) {
		return nil, 0, errors.New("binary format or architecture does not match OpenCode target")
	}
	if p.goos == "windows" {
		return data, 0600, nil
	}
	return data, uint32(info.Mode().Perm() &^ 0022), nil
}

func binaryMatchesPlatform(data []byte, p platform) bool {
	switch p.format {
	case "elf":
		return len(data) >= 20 && string(data[:4]) == "\x7fELF" && data[4] == 2 && data[5] == 1 && uint32(binary.LittleEndian.Uint16(data[18:20])) == p.machine
	case "macho":
		return len(data) >= 8 && binary.BigEndian.Uint32(data[:4]) == 0xcffaedfe && binary.LittleEndian.Uint32(data[4:8]) == p.machine
	case "pe":
		if len(data) < 0x40 || string(data[:2]) != "MZ" {
			return false
		}
		offset := binary.LittleEndian.Uint32(data[0x3c:0x40])
		return offset <= uint32(len(data)-6) && string(data[offset:offset+4]) == "PE\x00\x00" && uint32(binary.LittleEndian.Uint16(data[offset+4:offset+6])) == p.machine
	}
	return false
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

func remove(ctx context.Context, r Request, target string) error {
	if err := RevokeChannels(ctx, r.ControlRoot, r.RuntimeRoot); err != nil {
		return err
	}
	// One teardown decision under the same kernel fence includes private purge.
	files := []installruntime.File{}
	l, _, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil {
		return err
	}
	if len(l.Consumers) > 1 {
		before, ok := installruntime.OwnedFile(l, target)
		if ok {
			files = append(files, installruntime.File{Path: target, Before: before, Remove: true})
		} else if observed, err := installruntime.Fingerprint(target); err != nil || observed.Exists {
			return errors.New("unowned OpenCode plugin path blocks removal")
		}
		if r.GOOS == "windows" {
			shortcut, err := stageWindowsShortcutForSetup(r.HomeDir, "", false, l)
			if err != nil {
				return err
			}
			if shortcut != nil {
				files = append(files, *shortcut)
			}
		}
	}
	gen := l.Generation
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot,
		Owner: "existing-installer", ConsumerID: consumerID, RemoveConsumer: true, ExpectedGeneration: &gen, Files: files})
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
	_, supported := targetPlatform(goos, goarch)
	if !supported {
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
	executable := g.Executable
	if executable == "" {
		executable, err = os.Executable()
		if err != nil {
			return false, false
		}
	}
	return ChannelsFromSnapshot(s, executable, goos, goarch)
}

// ChannelsFromSnapshot validates OpenCode ownership and explicit consent in an
// already-read policy snapshot. It is safe to use inside a setup/native lease
// callback after the caller revalidates that snapshot under the component lock.
func ChannelsFromSnapshot(s installruntime.PolicySnapshot, executable, goos, goarch string) (bool, bool) {
	if !RegisteredFromSnapshot(s, executable, goos, goarch) {
		return false, false
	}
	c := s.Installation.Ledger.Consumers[consumerID]
	id, owned := installruntime.OwnedFile(s.Installation.Ledger, c.Registration)
	if c.OpenCode == nil || !c.OpenCode.Valid() || !c.OpenCode.OriginBound || !owned || id.SHA256 != c.OpenCode.BundleSHA256 {
		return false, false
	}
	return policyChannels(s.Fields)
}

// RegisteredFromSnapshot checks the owned OpenCode command without requiring
// desktop consent, so explicit permission setup can precede channel enablement.
func RegisteredFromSnapshot(s installruntime.PolicySnapshot, executable, goos, goarch string) bool {
	if s.Installation.Recovery {
		return false
	}
	p, supported := targetPlatform(goos, goarch)
	if !supported {
		return false
	}
	c, ok := s.Installation.Ledger.Consumers[consumerID]
	if !ok || c.Registration == "" || filepath.Base(c.Registration) != pluginName || len(c.Commands) != 4 ||
		c.Commands[1] != "opencode-event" || c.Commands[2] != "--protocol" || c.Commands[3] != "1" {
		return false
	}
	executable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return false
	}
	command, err := filepath.EvalSymlinks(c.Commands[0])
	if err != nil || command != executable || command != filepath.Join(c.RuntimeRoot, p.binary) {
		return false
	}
	if id, ok := installruntime.OwnedFile(s.Installation.Ledger, c.Registration); !ok || !id.Exists {
		return false
	}
	if id, ok := installruntime.OwnedFile(s.Installation.Ledger, command); !ok || !id.Exists {
		return false
	}
	return true
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
