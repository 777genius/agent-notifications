package geminiinstall

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/geminihooks"
)

type Action string

const (
	Install Action = "install"
	Update  Action = "update"
	Remove  Action = "remove"
	Recover Action = "recover"
)

type Request struct {
	Action                                               Action
	ControlRoot, RuntimeRoot, BinarySource, NativeSource string
	HomeDir, GeminiHome, ConfigRoot                      string
	ChannelPreimage                                      string
	Desktop, Webhook                                     bool
	GOOS, GOARCH                                         string
	// Fault forwards the existing kernel test seam; the CLI never supplies it.
	Fault func(string) error
}

func DefaultRequest(action Action) Request {
	root, _ := installruntime.ControlRoot()
	home, _ := os.UserHomeDir()
	return Request{Action: action, ControlRoot: root, HomeDir: home,
		GeminiHome: strings.TrimSpace(os.Getenv("GEMINI_CLI_HOME")), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
}

func settingsPath(r Request) (string, error) {
	root, err := ResolveConfigRoot(r)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "settings.json"), nil
}

// Apply composes the existing transaction kernel with the actual public UAP
// planner. Only our receipt and binary are ledger files; native settings use
// ConfigPaths and are read/planned in Prepare under both existing locks.
func Apply(ctx context.Context, r Request) error {
	if !filepath.IsAbs(r.ControlRoot) {
		return errors.New("absolute control root required")
	}
	root, err := installruntime.CanonicalPath(r.ControlRoot)
	if err != nil {
		return err
	}
	r.ControlRoot = root
	if r.Action == Recover {
		_, err := installruntime.Recover(ctx, root)
		return err
	}
	if r.Action != Install && r.Action != Update && r.Action != Remove {
		return errors.New("invalid Gemini action")
	}
	name, supported := binaryName(r.GOOS, r.GOARCH)
	if !supported {
		return errors.New("unsupported Gemini platform")
	}
	l, recovery, err := installruntime.ReadOwnership(root)
	if err != nil {
		return err
	}
	if recovery {
		return errors.New("installation recovery required; run setup-gemini recover")
	}
	if l.ID != "" && l.Owner != "existing-installer" {
		return fmt.Errorf("component owned by %s", l.Owner)
	}
	c, registered := l.Consumers[consumerID]
	if r.Action == Remove && !registered {
		return nil
	}
	if r.Action == Update && !registered {
		return errors.New("gemini consumer is not installed")
	}
	// Revoke using the recorded consumer identity before resolving live assets.
	// A moved/replaced runtime must leave consent off even if cleanup conflicts.
	if r.Action == Remove {
		if err := RevokeChannels(ctx, root, c.RuntimeRoot); err != nil {
			return err
		}
		l, recovery, err = installruntime.ReadOwnership(root)
		if err != nil || recovery {
			return errors.New("gemini consent revoked; cleanup requires a current ownership ledger")
		}
		if r.RuntimeRoot == "" {
			r.RuntimeRoot = c.RuntimeRoot
		}
		return remove(ctx, r, l)
	}
	if r.RuntimeRoot == "" {
		if registered {
			r.RuntimeRoot = c.RuntimeRoot
		} else if l.ID != "" {
			r.RuntimeRoot = l.RuntimeRoot
		}
	}
	if !filepath.IsAbs(r.RuntimeRoot) {
		return errors.New("absolute runtime root required")
	}
	r.RuntimeRoot, err = installruntime.CanonicalPath(r.RuntimeRoot)
	if err != nil {
		return err
	}
	if l.ID != "" && l.RuntimeRoot != r.RuntimeRoot {
		return errors.New("runtime root differs from managed installation")
	}
	if registered && c.RuntimeRoot != r.RuntimeRoot {
		return errors.New("gemini runtime relocation refused")
	}
	if r.NativeSource != "" && (r.GOOS != "darwin" || !r.Desktop || !filepath.IsAbs(r.NativeSource)) {
		return errors.New("absolute --native-app source requires macOS desktop consent")
	}
	if !filepath.IsAbs(r.BinarySource) {
		return errors.New("absolute --binary source required")
	}
	settings, err := settingsPath(r)
	if err != nil {
		return err
	}
	binary := filepath.Join(r.RuntimeRoot, name)
	var previous *receipt
	var previousBytes []byte
	if registered {
		old, data, err := readReceipt(root, l)
		if err != nil {
			return err
		}
		if old.SettingsPath != settings {
			return errors.New("gemini config root changed; use the original --config-root")
		}
		previous, previousBytes = &old, data
	}
	policyDesktop, policyWebhook, policyBefore, err := readChannels(root)
	if err != nil {
		return err
	}
	if r.ChannelPreimage != "" {
		data, before, err := readBounded(filepath.Join(root, "agent-notifications.json"), 64<<10)
		if err != nil {
			return err
		}
		var fields map[string]json.RawMessage
		if before.Exists && json.Unmarshal(data, &fields) != nil {
			return errors.New("invalid Gemini channel policy")
		}
		if err := installruntime.CheckObserverChannelPreimage(fields["route"], "geminiNotifications", r.ChannelPreimage); err != nil {
			return err
		}
		policyBefore = before
		if before.Exists {
			policyDesktop, policyWebhook, err = channels(data)
			if err != nil {
				return err
			}
		}
	}
	if !registered && (policyDesktop || policyWebhook) {
		return errors.New("stale Gemini consent must be revoked before installation")
	}
	binding := ""
	if previous != nil {
		binding = previous.Binding
	} else {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return err
		}
		binding = hex.EncodeToString(nonce[:])
	}
	shell := geminihooks.Bash
	if r.GOOS == "windows" {
		shell = geminihooks.PowerShell
	}
	next := receipt{Version: 1, SettingsPath: settings, Binding: binding, Shell: shell, Specs: hookSpecs(binary, root, binding)}
	data, mode, err := readBinary(r.BinarySource, r.GOOS, r.GOARCH)
	if err != nil {
		return err
	}
	beforeBinary, err := installruntime.Fingerprint(binary)
	if err != nil {
		return err
	}
	if beforeBinary.Exists {
		if _, owned := installruntime.OwnedFile(l, binary); !owned {
			return errors.New("target binary is foreign")
		}
	}
	receiptPath := filepath.Join(root, receiptName)
	beforeReceipt, err := installruntime.Fingerprint(receiptPath)
	if err != nil {
		return err
	}
	if beforeReceipt.Exists {
		if _, owned := installruntime.OwnedFile(l, receiptPath); !owned || !registered {
			return errors.New("target Gemini receipt is foreign")
		}
	}
	files := []installruntime.File{{Path: binary, Before: beforeBinary, Data: data, Mode: mode}}
	assetsNoOp := identityMatches(beforeBinary, data, mode)
	if r.GOOS == "windows" {
		shortcut, err := StageWindowsShortcut(r.HomeDir, binary, r.Desktop, l)
		if err != nil {
			return err
		}
		if shortcut != nil {
			files = append(files, *shortcut)
			assetsNoOp = false
		}
	}
	var native *installruntime.NativeChange
	if r.GOOS == "darwin" && r.Desktop {
		if r.NativeSource != "" {
			native, err = installruntime.StageNative(ctx, root, r.NativeSource)
			if err != nil {
				return err
			}
			defer func() {
				cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = installruntime.DiscardNative(cleanup, root, native)
			}()
			if native.After.DecoderFloor < 1 {
				return errors.New("macOS desktop requires verified helper decoder protocol 1")
			}
			assetsNoOp = assetsNoOp && l.Native != nil && l.Native.SHA256 == native.After.SHA256
		} else if l.Native == nil || l.Native.DecoderFloor < 1 {
			return errors.New("macOS desktop requires a verified managed helper; pass --native-app")
		}
	}
	noChange := errors.New("gemini installation unchanged")
	gen := l.Generation
	_, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: root, Owner: "existing-installer", RuntimeRoot: r.RuntimeRoot,
		ConsumerID: consumerID, Consumer: installruntime.Consumer{Registration: receiptPath, Commands: command(binary, root, binding)},
		ExpectedGeneration: &gen, ExpectedPolicy: &policyBefore, Files: files, Native: native, ConfigPaths: []string{settings},
		Fault: r.Fault,
		Prepare: func() ([]installruntime.File, error) {
			settingsBytes, beforeSettings, err := readSettings(settings)
			if err != nil {
				return nil, err
			}
			operation := geminihooks.Install
			var owned *geminihooks.Receipt
			if previous != nil {
				owned = previous.Owned
			}
			if r.Action == Update {
				operation = geminihooks.Update
			}
			plan, err := geminihooks.Plan(geminihooks.Request{Settings: settingsBytes, Shell: shell, Operation: operation, Hooks: next.Specs, Previous: owned})
			if err != nil {
				return nil, err
			}
			next.Owned = plan.Receipt
			if err := geminihooks.VerifyOwned(plan.Desired, next.Owned); err != nil {
				return nil, err
			}
			receiptData, err := receiptBytes(next)
			if err != nil {
				return nil, err
			}
			if registered && plan.NoOp && assetsNoOp && bytes.Equal(receiptData, previousBytes) &&
				policyDesktop == r.Desktop && policyWebhook == r.Webhook {
				return nil, noChange
			}
			extra := []installruntime.File{{Path: receiptPath, Before: beforeReceipt, Data: receiptData, Mode: 0600}}
			if !plan.NoOp {
				extra = append(extra, settingsFile(settings, beforeSettings, plan.Desired))
			}
			return extra, nil
		},
	})
	if err != nil && !errors.Is(err, noChange) {
		return err
	}
	if err := prepareNamespaces(ctx, root, r.GOOS == "darwin" && r.Desktop); err != nil {
		return err
	}
	if errors.Is(err, noChange) {
		return nil
	}
	return setChannels(ctx, root, r.RuntimeRoot, r.Desktop, r.Webhook, r.ChannelPreimage)
}

func readSettings(path string) ([]byte, installruntime.Identity, error) {
	data, before, err := readBounded(path, geminihooks.MaxSettingsBytes)
	if err == nil && before.Exists && len(data) == 0 {
		err = errors.New("existing Gemini settings file is empty")
	}
	return data, before, err
}

func settingsFile(path string, before installruntime.Identity, data []byte) installruntime.File {
	mode := uint32(0600)
	if before.Exists {
		mode = before.Mode
	}
	return installruntime.File{Path: path, Before: before, Data: data, Mode: mode}
}

func identityMatches(id installruntime.Identity, data []byte, mode uint32) bool {
	sum := sha256.Sum256(data)
	return id.Exists && id.Link == "" && id.SHA256 == hex.EncodeToString(sum[:]) && id.Mode == installruntime.IdentityMode(mode)
}

func prepareNamespaces(ctx context.Context, root string, native bool) error {
	if err := installruntime.CheckPrivateControlRoot(root); err != nil {
		return err
	}
	release, err := installruntime.LockExisting(ctx, filepath.Join(root, ".component-install.lock"))
	if err != nil {
		return err
	}
	cache := filepath.Join(root, "gemini-observations")
	err = os.Mkdir(cache, 0700)
	if errors.Is(err, os.ErrExist) {
		err = nil
	}
	if err == nil {
		err = installruntime.CheckPrivateControlRoot(cache)
	}
	release()
	if err != nil {
		return err
	}
	if native {
		_, err = PrepareNativeSpool(ctx, root)
	}
	return err
}

func readChannels(root string) (bool, bool, installruntime.Identity, error) {
	data, before, err := readBounded(filepath.Join(root, "agent-notifications.json"), 64<<10)
	if err != nil || !before.Exists {
		return false, false, before, err
	}
	desktop, webhook, err := channels(data)
	return desktop, webhook, before, err
}

func channels(data []byte) (bool, bool, error) {
	var policy struct {
		Route struct {
			Gemini *struct{ Desktop, Webhook *bool } `json:"geminiNotifications"`
		} `json:"route"`
	}
	if json.Unmarshal(data, &policy) != nil {
		return false, false, errors.New("invalid Gemini channel policy")
	}
	if policy.Route.Gemini == nil {
		return false, false, nil
	}
	if policy.Route.Gemini.Desktop == nil || policy.Route.Gemini.Webhook == nil {
		return false, false, errors.New("both Gemini channel consent fields are required")
	}
	return *policy.Route.Gemini.Desktop, *policy.Route.Gemini.Webhook, nil
}

func setChannels(ctx context.Context, root, runtimeRoot string, desktop, webhook bool, expected ...string) error {
	s, err := installruntime.ReadPolicySnapshot(ctx, root)
	if err != nil {
		return err
	}
	if len(expected) > 0 {
		if err := installruntime.CheckObserverChannelPreimage(s.Fields["route"], "geminiNotifications", expected[0]); err != nil {
			return err
		}
	}
	if s.Installation.Recovery {
		return errors.New("installation recovery required")
	}
	if _, ok := s.Installation.Ledger.Consumers[consumerID]; !ok {
		return errors.New("gemini consumer missing")
	}
	current, _ := json.Marshal(s.Fields)
	oldDesktop, oldWebhook, err := channels(current)
	if err != nil {
		return err
	}
	if oldDesktop == desktop && oldWebhook == webhook {
		return nil
	}
	value, _ := json.Marshal(map[string]bool{"desktop": desktop, "webhook": webhook})
	route, _ := json.Marshal(map[string]json.RawMessage{"geminiNotifications": value})
	gen := s.Installation.Ledger.Generation
	_, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: root, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: consumerID,
		RefreshOnly: true, PolicyOnly: true, ExpectedGeneration: &gen, ExpectedPolicy: &s.Preimage,
		PolicyFields: map[string]json.RawMessage{"route": route},
	})
	return err
}

func remove(ctx context.Context, r Request, l installruntime.Ledger) error {
	// Cleanup retains the ordinary physical runtime checks. Only the preceding
	// exact false-patch transaction can use the recorded path without resolving it.
	if !filepath.IsAbs(r.RuntimeRoot) {
		return errors.New("gemini consent revoked; cleanup conflict: absolute runtime root required")
	}
	physical, err := installruntime.CanonicalPath(r.RuntimeRoot)
	if err != nil {
		return fmt.Errorf("gemini consent revoked; cleanup conflict: %w", err)
	}
	c, registered := l.Consumers[consumerID]
	if !registered || physical != l.RuntimeRoot || physical != c.RuntimeRoot {
		return errors.New("gemini consent revoked; cleanup conflict: runtime root differs from managed installation")
	}
	r.RuntimeRoot = physical
	previous, _, err := readReceipt(r.ControlRoot, l)
	if err != nil {
		return fmt.Errorf("gemini consent revoked; cleanup conflict: %w", err)
	}
	if r.ConfigRoot != "" {
		requested, err := settingsPath(r)
		if err != nil || requested != previous.SettingsPath {
			return errors.New("gemini consent revoked; original --config-root required for cleanup")
		}
	}
	gen := l.Generation
	_, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot, Owner: "existing-installer", ConsumerID: consumerID,
		RemoveConsumer: true, ExpectedGeneration: &gen, ConfigPaths: []string{previous.SettingsPath},
		Prepare: func() ([]installruntime.File, error) {
			data, before, err := readSettings(previous.SettingsPath)
			if err != nil {
				return nil, err
			}
			plan, err := geminihooks.Plan(geminihooks.Request{Settings: data, Shell: previous.Shell, Operation: geminihooks.Remove, Previous: previous.Owned})
			if err != nil {
				return nil, err
			}
			var files []installruntime.File
			if !plan.NoOp {
				files = append(files, settingsFile(previous.SettingsPath, before, plan.Desired))
			}
			if len(l.Consumers) > 1 {
				path := filepath.Join(r.ControlRoot, receiptName)
				id, ok := installruntime.OwnedFile(l, path)
				if !ok {
					return nil, errors.New("gemini receipt ownership missing")
				}
				files = append(files, installruntime.File{Path: path, Before: id, Remove: true})
				if r.GOOS == "windows" {
					shortcut, err := StageWindowsShortcut(r.HomeDir, "", false, l)
					if err != nil {
						return nil, err
					}
					if shortcut != nil {
						files = append(files, *shortcut)
					}
				}
			}
			return files, nil
		},
	})
	return err
}
