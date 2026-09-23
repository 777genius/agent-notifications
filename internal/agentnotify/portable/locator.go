// Package portable selects an explicitly registered installed runtime.
// Setup publishes locators after the kernel consumer commit; launch never writes.
package portable

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/strictjson"
)

const MaxBytes = 16384

var ErrInvalid = errors.New("portable_binding_invalid")
var errExists = errors.New("portable_locator_exists")

type Integration string

const (
	Codex  Integration = "codex"
	Claude Integration = "claude"
)

// Binding is immutable consumer identity. Generation is deliberately excluded:
// independent consumer updates must not invalidate surviving bindings. The
// installed snapshot and lease fence current generation at each launch.
type Binding struct {
	Version        int         `json:"version"`
	Integration    Integration `json:"integration"`
	InstallationID string      `json:"installationID"`
	BindingID      string      `json:"bindingID"`
	ScopeID        string      `json:"scopeID"`
	ComponentID    string      `json:"componentID"`
	Owner          string      `json:"owner"`
	ScopeRoot      string      `json:"scopeRoot"`
	DataRoot       string      `json:"dataRoot"`
	ControlRoot    string      `json:"controlRoot"`
	GlobalConfig   string      `json:"globalConfig"`
	RuntimeRoot    string      `json:"runtimeRoot"`
	Primary        string      `json:"primary"`
}

var id = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)
var selector = regexp.MustCompile(`^agent-notify-[0-9a-f]{64}\.json$`)

// PlatformPrimary is the regular writer installed by the release bootstrap.
// Keep the slash-separated locator value independent of the host path separator.
func PlatformPrimary() string {
	name := "claude-notifications-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return "bin/" + name
}

func validPrimary(primary string) bool {
	if len(primary) == 0 || len(primary) > 512 || strings.ContainsAny(primary, `\:`) {
		return false
	}
	for _, part := range strings.Split(primary, "/") {
		if !id.MatchString(part) || part == "." || part == ".." {
			return false
		}
	}
	return filepath.IsLocal(filepath.FromSlash(primary))
}

func primaryPath(root, primary string) string {
	return filepath.Join(root, filepath.FromSlash(primary))
}

// Registration returns the exact consumer record the next setup slice must
// commit under the component lock/CAS before publishing locator bytes. Calling
// this pure function does not establish UAP receipt ownership or authorize setup.
func (b Binding) Registration() (string, installruntime.Consumer, []byte, error) {
	if b.Version != 1 || (b.Integration != Codex && b.Integration != Claude) || b.Owner != "existing-installer" {
		return "", installruntime.Consumer{}, nil, ErrInvalid
	}
	for _, s := range []string{b.InstallationID, b.BindingID, b.ScopeID, b.ComponentID} {
		if !id.MatchString(s) {
			return "", installruntime.Consumer{}, nil, ErrInvalid
		}
	}
	for _, p := range []string{b.ScopeRoot, b.DataRoot, b.ControlRoot, b.GlobalConfig, b.RuntimeRoot} {
		if len(p) > 4096 || !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return "", installruntime.Consumer{}, nil, ErrInvalid
		}
	}
	if !validPrimary(b.Primary) {
		return "", installruntime.Consumer{}, nil, ErrInvalid
	}
	raw, err := json.Marshal(b)
	if err != nil || len(raw) > MaxBytes {
		return "", installruntime.Consumer{}, nil, ErrInvalid
	}
	hash := sha256.Sum256(raw)
	key := "portable:" + hex.EncodeToString(hash[:])
	return key, installruntime.Consumer{RuntimeRoot: b.RuntimeRoot, Registration: string(raw), Commands: []string{primaryPath(b.RuntimeRoot, b.Primary)}}, raw, nil
}

// InstalledPrimary preserves the identity of an already committed installation.
// In particular, old locators used "primary" even though bootstrap never wrote
// that file. A later add/update/remove must not silently change their identity.
func InstalledPrimary(ledger installruntime.Ledger, installationID, controlRoot string) (string, bool, error) {
	primary, _, found, err := installedPaths(ledger, installationID, controlRoot)
	return primary, found, err
}

// InstalledGlobalConfig preserves the config path in a published locator.
// Changing it changes the consumer key, so existing bindings must keep it
// until an explicit migration replaces their locator and consumer together.
func InstalledGlobalConfig(ledger installruntime.Ledger, installationID, controlRoot string) (string, bool, error) {
	_, global, found, err := installedPaths(ledger, installationID, controlRoot)
	return global, found, err
}

func installedPaths(ledger installruntime.Ledger, installationID, controlRoot string) (string, string, bool, error) {
	primary, global := "", ""
	found := false
	for key, consumer := range ledger.Consumers {
		if !strings.HasPrefix(key, "portable:") {
			continue
		}
		var candidate Binding
		if json.Unmarshal([]byte(consumer.Registration), &candidate) != nil || candidate.InstallationID != installationID {
			continue
		}
		wantKey, wantConsumer, _, err := candidate.Registration()
		if err != nil || key != wantKey || !reflect.DeepEqual(consumer, wantConsumer) || candidate.ComponentID != ledger.ID || candidate.Owner != ledger.Owner || !samePhysicalPath(candidate.RuntimeRoot, ledger.RuntimeRoot) || !samePhysicalPath(candidate.ControlRoot, controlRoot) {
			return "", "", false, ErrInvalid
		}
		if found && (primary != candidate.Primary || global != candidate.GlobalConfig) {
			return "", "", false, ErrInvalid
		}
		primary, global, found = candidate.Primary, candidate.GlobalConfig, true
	}
	return primary, global, found, nil
}

// ResolveCommittedBinding selects the exact historical consumer for a UAP
// binding. GlobalConfig and Primary may differ from the current defaults: both
// are part of the registered bytes and must come from the ledger on removal.
// All other fields are fixed by the UAP receipt and the managed installation.
func ResolveCommittedBinding(ledger installruntime.Ledger, expected Binding) (Binding, bool, error) {
	variants, err := CommittedBindingVariants(ledger, expected)
	if err != nil || len(variants) > 1 {
		return Binding{}, false, ErrInvalid
	}
	if len(variants) == 0 {
		return Binding{}, false, nil
	}
	return variants[0], true, nil
}

// CommittedBindingVariants is for a controlled transition that temporarily
// holds the old and replacement consumer for the same UAP binding.
func CommittedBindingVariants(ledger installruntime.Ledger, expected Binding) ([]Binding, error) {
	if _, _, _, err := expected.Registration(); err != nil || ledger.ID != expected.ComponentID || ledger.Owner != expected.Owner || !samePhysicalPath(ledger.RuntimeRoot, expected.RuntimeRoot) {
		return nil, ErrInvalid
	}
	var variants []Binding
	for key, consumer := range ledger.Consumers {
		if !strings.HasPrefix(key, "portable:") {
			continue
		}
		candidate, err := decode([]byte(consumer.Registration))
		if err != nil {
			return nil, ErrInvalid
		}
		candidateKey, candidateConsumer, raw, err := candidate.Registration()
		if err != nil || key != candidateKey || string(raw) != consumer.Registration || !reflect.DeepEqual(candidateConsumer, consumer) || candidate.ComponentID != ledger.ID || candidate.Owner != ledger.Owner || !samePhysicalPath(candidate.RuntimeRoot, ledger.RuntimeRoot) {
			return nil, ErrInvalid
		}
		if candidate.InstallationID != expected.InstallationID || candidate.Integration != expected.Integration || candidate.BindingID != expected.BindingID {
			continue
		}
		if candidate.Version != expected.Version || candidate.ScopeID != expected.ScopeID || candidate.ComponentID != expected.ComponentID || candidate.Owner != expected.Owner || !samePhysicalPath(candidate.ScopeRoot, expected.ScopeRoot) || !samePhysicalPath(candidate.DataRoot, expected.DataRoot) || !samePhysicalPath(candidate.ControlRoot, expected.ControlRoot) || !samePhysicalPath(candidate.RuntimeRoot, expected.RuntimeRoot) {
			return nil, ErrInvalid
		}
		variants = append(variants, candidate)
	}
	return variants, nil
}

// ExactCommittedBinding validates one consumer without checking the executable.
func ExactCommittedBinding(ledger installruntime.Ledger, b Binding) bool {
	key, want, _, err := b.Registration()
	if err != nil || ledger.ID != b.ComponentID || ledger.Owner != b.Owner || !samePhysicalPath(ledger.RuntimeRoot, b.RuntimeRoot) {
		return false
	}
	got, ok := ledger.Consumers[key]
	return ok && reflect.DeepEqual(got, want)
}
func (b Binding) Filename() (string, error) {
	key, _, _, err := b.Registration()
	if err != nil {
		return "", err
	}
	return "agent-notify-" + key[len("portable:"):] + ".json", nil
}

// ValidateGlobalConfigParent applies launch's read-only parent check during
// setup, before a binding can be published.
func ValidateGlobalConfigParent(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrInvalid
	}
	return physicalDirectory(filepath.Dir(path))
}

// Publish writes the exact locator after the kernel consumer already exists.
// Identical bytes are a no-op; a conflicting file fails closed.
func Publish(b Binding) (string, error) {
	name, err := b.Filename()
	if err != nil {
		return "", err
	}
	_, _, raw, err := b.Registration()
	if err != nil {
		return "", err
	}
	if err := physicalDirectory(b.DataRoot); err != nil {
		return "", err
	}
	err = writePrivate(b.DataRoot, name, raw)
	if err == errExists {
		current, readErr := readPrivate(b.DataRoot, name)
		if readErr == nil && bytes.Equal(current, raw) {
			return name, nil
		}
		return "", ErrInvalid
	}
	return name, err
}

func RevokeLocator(b Binding) error {
	name, err := b.Filename()
	if err != nil {
		return err
	}
	_, _, raw, err := b.Registration()
	if err != nil {
		return err
	}
	current, err := readPrivate(b.DataRoot, name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !bytes.Equal(current, raw) {
		return ErrInvalid
	}
	return removePrivate(b.DataRoot, name)
}

// ExactLocator returns true only for the private, byte-identical locator.
// A missing locator is allowed during a resumed revoke.
func ExactLocator(b Binding) (bool, error) {
	name, err := b.Filename()
	if err != nil {
		return false, err
	}
	_, _, raw, err := b.Registration()
	if err != nil {
		return false, err
	}
	current, err := readPrivate(b.DataRoot, name)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil || !bytes.Equal(current, raw) {
		return false, ErrInvalid
	}
	return true, nil
}
func ParseArgs(args []string) (string, error) {
	if len(args) != 2 || args[0] != "--locator" || !selector.MatchString(args[1]) {
		return "", ErrInvalid
	}
	return args[1], nil
}
func decode(raw []byte) (Binding, error) {
	var b Binding
	if strictjson.Validate(raw, strictjson.Budget{Bytes: MaxBytes, Depth: 2, Entries: 16}) != nil {
		return b, ErrInvalid
	}
	// Require exact key spellings; encoding/json otherwise accepts case aliases.
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return b, ErrInvalid
	}
	allowed := []string{"version", "integration", "installationID", "bindingID", "scopeID", "componentID", "owner", "scopeRoot", "dataRoot", "controlRoot", "globalConfig", "runtimeRoot", "primary"}
	if len(fields) != len(allowed) {
		return b, ErrInvalid
	}
	for _, k := range allowed {
		if _, ok := fields[k]; !ok {
			return b, ErrInvalid
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&b) != nil {
		return b, ErrInvalid
	}
	_, _, _, err := b.Registration()
	return b, err
}

type Lease struct {
	Binding    Binding
	Executable string
	SHA256     string
	Release    func()
}

// Acquire rejects missing/revoked/foreign bindings before any runtime work.
// The consumer record, not a data marker, authorizes the complete identity.
func Acquire(ctx context.Context, data, name string) (*Lease, error) {
	if ctx == nil || ctx.Err() != nil || !selector.MatchString(name) {
		return nil, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	raw, err := readPrivate(data, name)
	if err != nil {
		return nil, ErrInvalid
	}
	b, err := decode(raw)
	if err != nil || !samePhysicalPath(b.DataRoot, data) {
		return nil, ErrInvalid
	}
	filename, _ := b.Filename()
	if filename != name {
		return nil, ErrInvalid
	}
	for _, p := range []string{b.ScopeRoot, b.ControlRoot, b.RuntimeRoot, filepath.Dir(b.GlobalConfig)} {
		if physicalDirectory(p) != nil {
			return nil, ErrInvalid
		}
	}
	snapshot, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		return nil, ErrInvalid
	}
	if err = b.CheckSnapshot(snapshot); err != nil {
		return nil, err
	}
	executable, _, err := b.ownedPrimary(snapshot.Ledger)
	if err != nil {
		return nil, err
	}
	// Bound the primary's file security separately from ledger content hashing.
	if checkPrimary(executable) != nil {
		return nil, ErrInvalid
	}
	_, release, err := installruntime.AcquireSetupLease(ctx, b.ControlRoot, snapshot)
	if err != nil {
		return nil, ErrInvalid
	}
	// Recheck the selected file under the lease: a locator removed while waiting
	// for setup must not become an admitted launch.
	current, readErr := readPrivate(data, name)
	if readErr != nil || !bytes.Equal(current, raw) || checkPrimary(executable) != nil {
		release()
		return nil, ErrInvalid
	}
	return &Lease{Binding: b, Executable: executable, SHA256: snapshot.Ledger.Files[executable].SHA256, Release: release}, nil
}

// CheckSnapshot keeps an already-running portable service bound to the current
// consumer on every policy read. Revocation does not require deleting shared data.
func (b Binding) CheckSnapshot(snapshot installruntime.InstalledSnapshot) error {
	ledger := snapshot.Ledger
	if snapshot.Recovery || ledger.ID != b.ComponentID || ledger.Owner != b.Owner || !samePhysicalPath(ledger.RuntimeRoot, b.RuntimeRoot) || ledger.WriterFloor > installruntime.ReservationWriterFloor || ledger.DecoderFloor > 1 {
		return ErrInvalid
	}
	key, want, _, err := b.Registration()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(ledger.Consumers[key], want) {
		return ErrInvalid
	}
	if _, _, err := b.ownedPrimary(ledger); err != nil {
		return ErrInvalid
	}

	return nil
}

// CheckPrimaryFile is used before a new binding is committed. It does not
// require its consumer record yet, but it requires a live, ledger-owned writer.
func (b Binding) CheckPrimaryFile(snapshot installruntime.InstalledSnapshot) error {
	if _, _, _, err := b.Registration(); err != nil {
		return err
	}
	_, err := ResolvePrimaryExecutable(snapshot.Ledger, b.Primary)
	return err
}

// ResolvePrimaryExecutable selects a live, ledger-owned writer for setup.
// Old bindings retain the logical "primary" identity even when that filename
// was never installed; their helper must use the same owned fallback as launch.
func ResolvePrimaryExecutable(ledger installruntime.Ledger, primary string) (string, error) {
	if !validPrimary(primary) {
		return "", ErrInvalid
	}
	path, fingerprint, err := ownedPrimary(ledger, primary)
	if err != nil || checkPrimary(path) != nil {
		return "", ErrInvalid
	}
	current, err := installruntime.Fingerprint(path)
	if err != nil || !reflect.DeepEqual(current, fingerprint) {
		return "", ErrInvalid
	}
	return path, nil
}

func (b Binding) ownedPrimary(ledger installruntime.Ledger) (string, installruntime.Identity, error) {
	return ownedPrimary(ledger, b.Primary)
}

func ownedPrimary(ledger installruntime.Ledger, primary string) (string, installruntime.Identity, error) {
	path := primaryPath(ledger.RuntimeRoot, primary)
	fingerprint, ok := ledger.Files[path]
	if !ok && primary == "primary" {
		// Compatibility for published locators with the old absent default.
		// An unexpected file at that name is drift, not permission to fall back.
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return "", installruntime.Identity{}, ErrInvalid
		}
		path = primaryPath(ledger.RuntimeRoot, PlatformPrimary())
		fingerprint, ok = ledger.Files[path]
	}
	if !ok || !fingerprint.Exists || fingerprint.Link != "" || !portablePrimaryMode(fingerprint.Mode) {
		return "", installruntime.Identity{}, ErrInvalid
	}
	return path, fingerprint, nil
}

func portablePrimaryMode(mode uint32) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	return mode&0111 != 0
}
