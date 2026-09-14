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
	"path/filepath"
	"reflect"
	"regexp"
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
	if !id.MatchString(b.Primary) || b.Primary == "." || b.Primary == ".." {
		return "", installruntime.Consumer{}, nil, ErrInvalid
	}
	raw, err := json.Marshal(b)
	if err != nil || len(raw) > MaxBytes {
		return "", installruntime.Consumer{}, nil, ErrInvalid
	}
	hash := sha256.Sum256(raw)
	key := "portable:" + hex.EncodeToString(hash[:])
	return key, installruntime.Consumer{RuntimeRoot: b.RuntimeRoot, Registration: string(raw), Commands: []string{filepath.Join(b.RuntimeRoot, b.Primary)}}, raw, nil
}
func (b Binding) Filename() (string, error) {
	key, _, _, err := b.Registration()
	if err != nil {
		return "", err
	}
	return "agent-notify-" + key[len("portable:"):] + ".json", nil
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
	return removePrivate(b.DataRoot, name)
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
	executable := filepath.Join(snapshot.Ledger.RuntimeRoot, b.Primary)
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
	if snapshot.Recovery || ledger.ID != b.ComponentID || ledger.Owner != b.Owner || !samePhysicalPath(ledger.RuntimeRoot, b.RuntimeRoot) || ledger.WriterFloor > installruntime.WriterFloor || ledger.DecoderFloor > 1 {
		return ErrInvalid
	}
	key, want, _, err := b.Registration()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(ledger.Consumers[key], want) {
		return ErrInvalid
	}
	executable := filepath.Join(ledger.RuntimeRoot, b.Primary)
	fingerprint, ok := ledger.Files[executable]
	if !ok || !fingerprint.Exists || fingerprint.Link != "" || fingerprint.Mode&0111 == 0 {
		return ErrInvalid
	}

	return nil
}
