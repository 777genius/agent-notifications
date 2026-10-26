package qoderinstall

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/qoderpackage"
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
	GOOS, GOARCH, Version                                string
}
type receipt struct {
	Schema        int             `json:"schema"`
	ConfigRoot    string          `json:"configRoot"`
	PackageRoot   string          `json:"packageRoot"`
	Binding       string          `json:"binding"`
	NativeEntry   json.RawMessage `json:"nativeEntry"`
	NativeEnabled json.RawMessage `json:"nativeEnabled"`
	Files         []string        `json:"files"`
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
	if !filepath.IsAbs(root) {
		return "", errors.New("absolute Qoder config root required")
	}
	return installruntime.CanonicalPath(root)
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
	var result receipt
	path := filepath.Join(root, receiptName)
	d, err := readDocument(path)
	owned, ok := installruntime.OwnedFile(l, path)
	c, registered := l.Consumers[ConsumerID]
	if err != nil || !ok || !registered || c.Registration != path || owned != d.before || json.Unmarshal(d.data, &result) != nil || result.Schema != 1 || result.Binding == "" || !filepath.IsAbs(result.ConfigRoot) || !inside(filepath.Join(result.ConfigRoot, "plugins", "cache", "local", "agent-notifications-qoder"), result.PackageRoot) {
		return result, errors.New("managed Qoder receipt is missing or edited")
	}
	return result, nil
}
func checkFiles(l installruntime.Ledger, r receipt) error {
	expected := map[string]bool{}
	for _, path := range r.Files {
		if !inside(r.PackageRoot, path) {
			return errors.New("invalid Qoder owned file path")
		}
		expected[path] = true
		want, ok := installruntime.OwnedFile(l, path)
		got, err := installruntime.Fingerprint(path)
		if err != nil || !ok || want != got || !got.Exists {
			return errors.New("managed Qoder package was edited or removed")
		}
	}
	return filepath.WalkDir(r.PackageRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && !expected[path] {
			return errors.New("qoder package contains an unowned file")
		}
		return nil
	})
}

func Apply(ctx context.Context, r Request) error {
	var err error
	r, err = normalize(r)
	if err != nil {
		return err
	}
	if r.Action == Recover {
		_, err = installruntime.Recover(ctx, r.ControlRoot)
		return err
	}
	if r.Action != Install && r.Action != Update && r.Action != Remove {
		return errors.New("invalid Qoder action")
	}
	if !filepath.IsAbs(r.QoderExecutable) {
		return errors.New("absolute --qoder-executable required")
	}
	st, err := os.Stat(r.QoderExecutable)
	if err != nil || !st.Mode().IsRegular() {
		return errors.New("native Qoder executable unavailable")
	}
	l, recovery, err := installruntime.ReadOwnership(r.ControlRoot)
	if err != nil {
		return err
	}
	if recovery {
		return errors.New("installation recovery required; run setup-qoder recover")
	}
	if l.ID != "" && l.Owner != "existing-installer" {
		return errors.New("managed runtime has another owner")
	}
	c, registered := l.Consumers[ConsumerID]
	policyData, err := readDocument(filepath.Join(r.ControlRoot, "agent-notifications.json"))
	if err != nil {
		return err
	}
	policyFields, err := object(policyData.data)
	if err != nil {
		return err
	}
	if err = installruntime.CheckObserverChannelPreimage(policyFields["route"], "qoderNotifications", r.ChannelPreimage); err != nil {
		return err
	}
	oldDesktop, oldWebhook, err := channels(policyFields)
	if err != nil {
		return err
	}
	if !registered && r.Action != Remove && (oldDesktop || oldWebhook) {
		return errors.New("stale Qoder consent must be revoked before installation")
	}
	if r.Action == Remove && !registered {
		return nil
	}
	if r.Action == Update && !registered {
		return errors.New("qoder is not installed")
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
	if !filepath.IsAbs(r.RuntimeRoot) {
		return errors.New("absolute runtime root required")
	}
	r.RuntimeRoot, err = installruntime.CanonicalPath(r.RuntimeRoot)
	if err != nil {
		return err
	}
	var previous *receipt
	if registered {
		old, e := readReceipt(r.ControlRoot, l)
		if e != nil {
			return e
		}
		if old.ConfigRoot != r.ConfigRoot || c.RuntimeRoot != r.RuntimeRoot {
			return errors.New("qoder profile or runtime relocation refused")
		}
		if e = checkFiles(l, old); e != nil {
			return e
		}
		previous = &old
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
	var source string
	var sharedData []byte
	var sharedMode uint32
	sharedName, supported := sharedBinaryName(r.GOOS, r.GOARCH)
	if !supported {
		return errors.New("unsupported Qoder release platform")
	}
	if r.Action != Remove {
		if !filepath.IsAbs(r.BinarySource) {
			return errors.New("absolute --binary source required")
		}
		sharedData, sharedMode, err = readBinary(r.BinarySource, r.GOOS, r.GOARCH)
		if err != nil {
			return err
		}
		temp, e := os.MkdirTemp("", "TEST-qoder-package-")
		if e != nil {
			return e
		}
		defer func() { _ = os.RemoveAll(temp) }()
		source = filepath.Join(temp, "bundle")
		_, err = qoderpackage.Build(qoderpackage.BuildRequest{Version: r.Version, GOOS: r.GOOS, GOARCH: r.GOARCH, Executable: r.BinarySource, OutputRoot: source, ManagedControlRoot: r.ControlRoot, Binding: binding})
		if err != nil {
			return err
		}
	}
	plan, cleanup, err := planNative(ctx, r, source, r.Action == Remove, previous)
	if err != nil {
		return err
	}
	defer cleanup()
	if r.Action != Remove {
		// A new version directory can already contain user files even when no
		// desired filename collides. Do not silently adopt that foreign tree.
		err = filepath.WalkDir(plan.packageRoot, func(path string, d os.DirEntry, walkErr error) error {
			if os.IsNotExist(walkErr) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if !d.IsDir() {
				if _, owned := installruntime.OwnedFile(l, path); !owned {
					return errors.New("qoder destination contains an unowned file")
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	var native *installruntime.NativeChange
	if r.Action != Remove && r.Desktop && r.GOOS == "darwin" {
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
			if native.After.DecoderFloor < 1 {
				return errors.New("macOS desktop requires verified helper decoder protocol 1")
			}
		} else if l.Native == nil || l.Native.DecoderFloor < 1 {
			return errors.New("macOS desktop requires a verified managed helper; pass --native-app")
		}
	}
	receiptPath := filepath.Join(r.ControlRoot, receiptName)
	receiptBefore, err := installruntime.Fingerprint(receiptPath)
	if err != nil {
		return err
	}
	if receiptBefore.Exists && !registered {
		return errors.New("foreign Qoder receipt exists")
	}
	next := receipt{Schema: 1, ConfigRoot: r.ConfigRoot, PackageRoot: plan.packageRoot, Binding: binding}
	if r.Action != Remove {
		next.NativeEntry, err = nativeEntry(plan.desired[registryName])
		if err != nil {
			return err
		}
		next.NativeEnabled, err = nativeEnabled(plan.desired["settings.json"])
		if err != nil {
			return err
		}
	}
	files := []installruntime.File{}
	if r.Action != Remove {
		path := filepath.Join(r.RuntimeRoot, sharedName)
		before, e := installruntime.Fingerprint(path)
		if e != nil {
			return e
		}
		if before.Exists {
			if _, ok := installruntime.OwnedFile(l, path); !ok {
				return errors.New("shared executable destination is unowned")
			}
		}
		files = append(files, installruntime.File{Path: path, Before: before, Data: sharedData, Mode: sharedMode})
	}
	desiredPackageFiles := map[string]bool{}
	for rel, data := range plan.desired {
		if !strings.HasPrefix(rel, "package/") {
			continue
		}
		path := filepath.Join(plan.packageRoot, filepath.FromSlash(strings.TrimPrefix(rel, "package/")))
		before, e := installruntime.Fingerprint(path)
		if e != nil {
			return e
		}
		if before.Exists {
			if _, ok := installruntime.OwnedFile(l, path); !ok {
				return errors.New("native Qoder package has unowned destination")
			}
		}
		mode := uint32(0600)
		if strings.HasPrefix(rel, "package/bin/") {
			mode = 0755
		}
		files = append(files, installruntime.File{Path: path, Before: before, Data: data, Mode: mode})
		next.Files = append(next.Files, path)
		desiredPackageFiles[path] = true
	}
	if r.Action != Remove && r.Desktop && r.GOOS == "darwin" {
		change := native
		if change == nil {
			change = &installruntime.NativeChange{After: *l.Native}
		}
		aliases, e := installruntime.NativeAlias(change, filepath.Join(plan.packageRoot, "bin"))
		if e != nil {
			return e
		}
		for _, file := range aliases {
			files = append(files, file)
			next.Files = append(next.Files, file.Path)
			desiredPackageFiles[file.Path] = true
		}
	}
	sort.Strings(next.Files)
	if previous != nil {
		for _, path := range previous.Files {
			if r.Action != Remove && desiredPackageFiles[path] {
				continue
			}
			before, e := installruntime.Fingerprint(path)
			if e != nil {
				return e
			}
			files = append(files, installruntime.File{Path: path, Before: before, Remove: true})
		}
	}
	// RemoveConsumer disallows ordinary Files; adapter files are returned under
	// the kernel locks in Prepare, alongside native registry CAS mutations.
	gen := l.Generation
	req := installruntime.Request{ControlRoot: r.ControlRoot, RuntimeRoot: r.RuntimeRoot, Owner: "existing-installer", ConsumerID: ConsumerID, Consumer: installruntime.Consumer{Registration: receiptPath, Commands: []string{filepath.Join(plan.packageRoot, "bin", binaryName(r.GOOS)), binding}}, ExpectedGeneration: &gen, Native: native, RemoveConsumer: r.Action == Remove, ConfigPaths: []string{filepath.Join(r.ConfigRoot, filepath.FromSlash(registryName)) + ".agent-notifications-qoder", filepath.Join(r.ConfigRoot, "settings.json") + ".agent-notifications-qoder"}, AdapterConfigPaths: []string{filepath.Join(r.ConfigRoot, filepath.FromSlash(registryName)), filepath.Join(r.ConfigRoot, "settings.json")}}
	req.ExpectedPolicy = &policyData.before
	if registered && r.Action != Remove {
		req.PolicyFields = map[string]json.RawMessage{"route": mustJSON(map[string]any{"qoderNotifications": map[string]bool{"desktop": r.Desktop, "webhook": r.Webhook}})}
	}
	req.Prepare = func() ([]installruntime.File, error) {
		result := append([]installruntime.File(nil), files...)
		for rel, d := range plan.documents {
			path := filepath.Join(r.ConfigRoot, filepath.FromSlash(rel))
			mode := uint32(0600)
			if d.before.Exists {
				mode = d.before.Mode
			}
			result = append(result, installruntime.File{Path: path, Before: d.before, Data: plan.desired[rel], Mode: mode})
		}
		if r.Action == Remove {
			result = append(result, installruntime.File{Path: receiptPath, Before: receiptBefore, Remove: true})
		} else {
			result = append(result, installruntime.File{Path: receiptPath, Before: receiptBefore, Data: mustJSON(next), Mode: 0600})
		}
		return result, nil
	}
	// With no sibling consumers, the kernel retires all owned files itself.
	if r.Action == Remove && len(l.Consumers) == 1 {
		files = nil
		req.Prepare = func() ([]installruntime.File, error) {
			var result []installruntime.File
			for rel, d := range plan.documents {
				mode := uint32(0600)
				if d.before.Exists {
					mode = d.before.Mode
				}
				result = append(result, installruntime.File{Path: filepath.Join(r.ConfigRoot, filepath.FromSlash(rel)), Before: d.before, Data: plan.desired[rel], Mode: mode})
			}
			return result, nil
		}
	}
	if r.Action == Remove {
		if err = setChannels(ctx, r.ControlRoot, r.RuntimeRoot, false, false, ""); err != nil {
			return err
		}
		l, _, err = installruntime.ReadOwnership(r.ControlRoot)
		if err != nil {
			return err
		}
		gen = l.Generation
		currentPolicy, e := readDocument(filepath.Join(r.ControlRoot, "agent-notifications.json"))
		if e != nil {
			return e
		}
		req.ExpectedPolicy = &currentPolicy.before
	}
	if _, err = installruntime.Commit(ctx, req); err != nil {
		return err
	}
	if r.Action != Remove && !registered {
		return setChannels(ctx, r.ControlRoot, r.RuntimeRoot, r.Desktop, r.Webhook, r.ChannelPreimage)
	}
	return nil
}
func binaryName(goos string) string {
	if goos == "windows" {
		return "claude-notifications.exe"
	}
	return "claude-notifications"
}
