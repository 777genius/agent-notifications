package qoderinstall

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/qoderpackage"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/ports"
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
	HomeDir, ConfigRoot, QoderExecutable                 string
	ChannelPreimage                                      string
	Desktop, Webhook                                     bool
	PreserveChannels                                     bool
	GOOS, GOARCH, Version                                string
	// Runner is an internal test seam; public setup always uses UAP's OS runner.
	Runner ports.CommandRunner
	Fault  func(string) error
}

// The runtime owns this receipt and its executable, never Qoder's native cache
// or settings. UAP owns the prepared package and native CLI lifecycle. Native
// file identities are read-only evidence binding consent to the actual copy.
type receipt struct {
	Schema                                                                  int `json:"schema"`
	ConfigRoot, PackageRoot, Binding                                        string
	InstallationID, UAPBindingID, ManagedRoot, TreeDigest, ProjectionDigest string
	Executable                                                              string
	Pending                                                                 bool
	Removing                                                                bool
	Version                                                                 string
	NativeEntry, NativeEnabled                                              json.RawMessage
	Files                                                                   map[string]installruntime.Identity
}

func DefaultRequest(action Action) Request {
	root, _ := installruntime.ControlRoot()
	home, _ := os.UserHomeDir()
	return Request{Action: action, ControlRoot: root, HomeDir: home, ConfigRoot: os.Getenv("QODER_CONFIG_DIR"), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Version: config.ConsumerVersion}
}
func ResolveConfigRoot(r Request) (string, error) {
	root := r.ConfigRoot
	if root == "" {
		if !filepath.IsAbs(r.HomeDir) {
			return "", errors.New("absolute Qoder home required")
		}
		root = filepath.Join(r.HomeDir, ".qoder")
	}
	return qoderProfile(root)
}
func normalize(r Request) (Request, error) {
	if !filepath.IsAbs(r.ControlRoot) {
		return r, errors.New("absolute control root required")
	}
	var err error
	r.ControlRoot, err = installruntime.CanonicalPath(r.ControlRoot)
	if err != nil {
		return r, err
	}
	r.ConfigRoot, err = ResolveConfigRoot(r)
	return r, err
}
func readReceipt(root string, l installruntime.Ledger) (receipt, error) {
	var out receipt
	path := filepath.Join(root, receiptName)
	d, err := readDocument(path)
	owned, ok := installruntime.OwnedFile(l, path)
	c, registered := l.Consumers[ConsumerID]
	if err != nil || !ok || !registered || c.Registration != path || owned != d.before || json.Unmarshal(d.data, &out) != nil || out.Schema != 2 || out.Binding == "" || !filepath.IsAbs(out.ConfigRoot) || out.InstallationID == "" || out.UAPBindingID == "" || !filepath.IsAbs(out.ManagedRoot) || !filepath.IsAbs(out.Executable) {
		return out, errors.New("qoder UAP receipt missing, edited or retired; legacy installation adoption is not supported")
	}
	return out, nil
}
func checkFiles(l installruntime.Ledger, r receipt) error {
	if r.Pending {
		return errors.New("qoder installation is incomplete; retry the same install or update with explicit channels")
	}
	want, ok := installruntime.OwnedFile(l, r.Executable)
	got, err := installruntime.Fingerprint(r.Executable)
	if err != nil || !ok || !got.Exists || want != got {
		return errors.New("managed Qoder runtime executable was edited or removed")
	}
	return checkNativeSnapshot(r)
}

func Apply(ctx context.Context, r Request) error {
	var err error
	r, err = normalize(r)
	if err != nil {
		return err
	}
	unlock, err := installruntime.Lock(ctx, filepath.Join(uapRoot(r.ControlRoot), ".host-install.lock"))
	if err != nil {
		return fmt.Errorf("lock Qoder composition: %w", err)
	}
	defer unlock()
	if r.Action == Recover {
		return recoverInstallation(ctx, r)
	}
	if r.Action != Install && r.Action != Update && r.Action != Remove {
		return errors.New("invalid Qoder action")
	}
	if !filepath.IsAbs(r.QoderExecutable) {
		return errors.New("absolute --qoder-executable required")
	}
	info, err := os.Stat(r.QoderExecutable)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("native Qoder executable unavailable")
	}
	snap, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		return fmt.Errorf("observe Qoder runtime policy: %w", err)
	}
	l := snap.Installation.Ledger
	if snap.Installation.Recovery || l.PendingMutation != nil {
		return errors.New("installation recovery required; run setup-qoder recover")
	}
	if l.ID != "" && l.Owner != "existing-installer" {
		return errors.New("managed runtime has another owner")
	}
	if err = installruntime.CheckObserverChannelPreimage(snap.Fields["route"], "qoderNotifications", r.ChannelPreimage); err != nil {
		return err
	}
	c, registered := l.Consumers[ConsumerID]
	var previous *receipt
	if registered {
		old, e := readReceipt(r.ControlRoot, l)
		if e != nil {
			return e
		}
		if old.ConfigRoot != r.ConfigRoot {
			return errors.New("qoder profile relocation refused")
		}
		if old.Removing && r.Action != Remove {
			return errors.New("qoder removal is pending; retry remove before installing")
		}
		if old.Pending {
			if e = verifyPendingNative(r.ControlRoot, old); e != nil {
				return e
			}
		}
		if !old.Pending {
			if e = checkFiles(l, old); e != nil {
				return e
			}
			if e = checkUAP(ctx, r, old); e != nil {
				return e
			}
		}
		if r.PreserveChannels {
			r.Desktop, r.Webhook, err = channels(snap.Fields)
			if err != nil {
				return err
			}
		}
		previous = &old
	} else {
		if d, e := readDocument(filepath.Join(r.ControlRoot, receiptName)); e != nil || d.before.Exists {
			return errors.New("foreign or retired Qoder receipt exists")
		}
		if r.Action == Remove {
			return nil
		}
		if r.Action == Update {
			return errors.New("qoder is not installed")
		}
		desktop, webhook, e := channels(snap.Fields)
		if e != nil {
			return e
		}
		if desktop || webhook {
			return errors.New("stale Qoder consent must be revoked before installation")
		}
	}
	if r.RuntimeRoot == "" {
		if registered {
			r.RuntimeRoot = c.RuntimeRoot
		} else if l.RuntimeRoot != "" {
			r.RuntimeRoot = l.RuntimeRoot
		} else {
			r.RuntimeRoot = filepath.Join(r.ControlRoot, "runtime")
		}
	}
	r.RuntimeRoot, err = installruntime.CanonicalPath(r.RuntimeRoot)
	if err != nil || !filepath.IsAbs(r.RuntimeRoot) {
		return errors.New("absolute runtime root required")
	}
	if registered && c.RuntimeRoot != r.RuntimeRoot {
		return errors.New("qoder runtime relocation refused")
	}
	if r.Action != Remove {
		if err = checkCacheDestination(r, previous); err != nil {
			return err
		}
	}
	eng, err := newEngine(r)
	if err != nil {
		return fmt.Errorf("create Qoder UAP engine: %w", err)
	}
	if r.Action == Remove {
		return removeInstallation(ctx, r, eng, *previous)
	}
	name, supported := sharedBinaryName(r.GOOS, r.GOARCH)
	if !supported {
		return errors.New("unsupported Qoder release platform")
	}
	executable := filepath.Join(r.RuntimeRoot, name)
	data, mode, err := readBinary(r.BinarySource, r.GOOS, r.GOARCH)
	if err != nil {
		return fmt.Errorf("read Qoder runtime source: %w", err)
	}
	binding := ""
	if previous != nil {
		binding = previous.Binding
	} else {
		var nonce [16]byte
		if _, err = rand.Read(nonce[:]); err != nil {
			return err
		}
		binding = hex.EncodeToString(nonce[:])
	}
	temp, err := os.MkdirTemp("", "TEST-qoder-uap-package-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(temp) }()
	source := filepath.Join(temp, "bundle")
	if _, err = qoderpackage.Build(qoderpackage.BuildRequest{Version: r.Version, GOOS: r.GOOS, GOARCH: r.GOARCH, Executable: r.BinarySource, OutputRoot: source, ManagedControlRoot: r.ControlRoot, Binding: binding, ManagedExecutable: executable}); err != nil {
		return err
	}
	req := uapinstaller.Request{Operation: uapinstaller.OpInstall, PackageRoot: source, SourceRoot: r.ControlRoot, ClientID: "qoder", ClientConfigRoot: r.ConfigRoot, ClientExecutable: r.QoderExecutable, RequiredComponents: []string{"hooks"}}
	if previous != nil {
		req.InstallationID = previous.InstallationID
		if present, e := hasUAPBinding(ctx, eng, previous.UAPBindingID); e != nil {
			return e
		} else if present {
			req.Operation = uapinstaller.OpUpdate
		}
	}
	prepared, err := eng.Prepare(ctx, req)
	if err != nil {
		return fmt.Errorf("prepare Qoder UAP installation: %w", err)
	}
	defer func() { _ = prepared.Close() }()
	plan := prepared.Plan()
	next := receipt{Schema: 2, ConfigRoot: r.ConfigRoot, Binding: binding, InstallationID: plan.InstallationID, UAPBindingID: plan.BindingID, ManagedRoot: plan.TargetPath, TreeDigest: plan.TreeDigest, Executable: executable, Version: r.Version, Pending: true}
	if previous != nil {
		next.PackageRoot, next.NativeEntry, next.NativeEnabled, next.Files = previous.PackageRoot, previous.NativeEntry, previous.NativeEnabled, previous.Files
	}

	channelBasis, err := installruntime.ObserverChannelPreimage(snap.Fields["route"], "qoderNotifications")
	if err != nil {
		return err
	}
	oldDesktop, oldWebhook, err := channels(snap.Fields)
	if err != nil {
		return err
	}
	if registered && (oldDesktop || oldWebhook) {
		channelBasis, err = installruntime.ObserverChannelPreimage(mustJSON(map[string]any{"qoderNotifications": map[string]bool{"desktop": false, "webhook": false}}), "qoderNotifications")
		if err != nil {
			return err
		}
	}
	// Revoke before UAP's external effects. Failure leaves a truthful pending
	// receipt with channels off; retry reuses the exact managed identities.
	if registered {
		if err = setChannels(ctx, r.ControlRoot, r.RuntimeRoot, false, false, r.ChannelPreimage); err != nil {
			return err
		}
	}
	if r.Fault != nil {
		if err = r.Fault("qoder_consent_revoked"); err != nil {
			return err
		}
	}
	if err = publishRuntime(ctx, r, next, data, mode, channelBasis); err != nil {
		return fmt.Errorf("prepare Qoder shared runtime: %w", err)
	}
	if r.Fault != nil {
		if err = r.Fault("qoder_runtime_prepared"); err != nil {
			return err
		}
	}
	result, err := eng.Apply(ctx, prepared, uapinstaller.Decision{Confirmed: true})
	if err != nil {
		return fmt.Errorf("qoder UAP installation incomplete (consent remains off): %w", err)
	}
	if result.Client.Activation != string(domain.ActivationActive) || result.Client.Verification != string(domain.VerificationInstalled) {
		return fmt.Errorf("qoder UAP native activation is unverified (%s/%s, outcome %s, targets %+v); consent remains off", result.Client.Activation, result.Client.Verification, result.Outcome, result.Targets)
	}
	if r.Fault != nil {
		if err = r.Fault("qoder_uap_installed"); err != nil {
			return err
		}
	}
	next.Pending = false
	if err = attestNativePackage(ctx, r, eng, &next); err != nil {
		return err
	}
	if err = publishReceipt(ctx, r, next); err != nil {
		return err
	}
	return setChannels(ctx, r.ControlRoot, r.RuntimeRoot, r.Desktop, r.Webhook, channelBasis)
}

func publishRuntime(ctx context.Context, r Request, next receipt, data []byte, mode uint32, channelBasis string) error {
	s, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		return err
	}
	if err = installruntime.CheckObserverChannelPreimage(s.Fields["route"], "qoderNotifications", channelBasis); err != nil {
		return err
	}
	l := s.Installation.Ledger
	path := next.Executable
	before, err := installruntime.Fingerprint(path)
	if err != nil {
		return err
	}
	if before.Exists {
		if owned, ok := installruntime.OwnedFile(l, path); !ok || owned != before {
			return errors.New("shared executable destination is unowned or edited")
		}
	}
	receiptPath := filepath.Join(r.ControlRoot, receiptName)
	receiptBefore, err := installruntime.Fingerprint(receiptPath)
	if err != nil {
		return err
	}
	files := []installruntime.File{{Path: path, Before: before, Data: data, Mode: mode}, {Path: receiptPath, Before: receiptBefore, Data: mustJSON(next), Mode: 0600}}
	var native *installruntime.NativeChange
	if r.Desktop && r.GOOS == "darwin" {
		if r.NativeSource != "" {
			native, err = installruntime.StageNative(ctx, r.ControlRoot, r.NativeSource)
			if err != nil {
				return err
			}
			defer func() {
				clean, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = installruntime.DiscardNative(clean, r.ControlRoot, native)
			}()
		}
		change := native
		if change == nil {
			if l.Native == nil || l.Native.DecoderFloor < 1 {
				return errors.New("macOS desktop requires a verified managed helper; pass --native-app")
			}
			change = &installruntime.NativeChange{After: *l.Native}
		}
		if change.After.DecoderFloor < 1 {
			return errors.New("macOS desktop requires verified helper decoder protocol 1")
		}
		aliases, e := installruntime.NativeAlias(change, filepath.Join(r.RuntimeRoot, "bin"))
		if e != nil {
			return e
		}
		files = append(files, aliases...)
	}
	req := installruntime.Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot, Owner: "existing-installer", ConsumerID: ConsumerID, Consumer: installruntime.Consumer{Registration: receiptPath, Commands: []string{next.Executable, next.Binding}}, ExpectedGeneration: &l.Generation, ExpectedPolicy: &s.Preimage, Files: files, Native: native}
	_, configured := s.Fields["enabled"]
	committed, err := installruntime.Commit(ctx, req)
	if err != nil {
		return err
	}
	if !configured {
		// Seed only the originally absent decision, after this consumer exists.
		// A concurrent policy/generation change fails the original CAS.
		on := true
		_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot, Owner: "existing-installer", ConsumerID: ConsumerID, RefreshOnly: true, PolicyOnly: true, ExpectedGeneration: &committed.Generation, ExpectedPolicy: &s.Preimage, PolicyEnabled: &on})
	}
	return err
}
func publishReceipt(ctx context.Context, r Request, next receipt) error {
	s, err := installruntime.ReadPolicySnapshot(ctx, r.ControlRoot)
	if err != nil {
		return err
	}
	path := filepath.Join(r.ControlRoot, receiptName)
	before, err := installruntime.Fingerprint(path)
	if err != nil {
		return err
	}
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot, Owner: "existing-installer", ConsumerID: ConsumerID, RefreshOnly: true, ExpectedGeneration: &s.Installation.Ledger.Generation, ExpectedPolicy: &s.Preimage, Files: []installruntime.File{{Path: path, Before: before, Data: mustJSON(next), Mode: 0600}}})
	return err
}
