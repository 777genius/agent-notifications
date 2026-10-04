package config

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
	"runtime"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/opencodeplugin"
	"github.com/777genius/agent-notifications/internal/strictjson"
)

const managedOpenCodeConsumer = "opencode-notifications"
const OpenCodeWebhookURLEnv = "AGENT_NOTIFICATIONS_WEBHOOK_URL"

// OpenCodeWebhookLookup excludes all ambient values except the event-only URL.
func OpenCodeWebhookLookup(lookup func(string) (string, bool)) func(string) (string, bool) {
	return func(key string) (string, bool) {
		if key == OpenCodeWebhookURLEnv && lookup != nil {
			return lookup(key)
		}
		return "", false
	}
}

// Managed saves validate the selected raw URL without expanding or storing secrets.
func (d Document) validateOpenCodeWebhookEnv() error {
	data := d.original
	if d.schema == 2 {
		var err error
		data, err = d.prepareProfiles().mergedProfile(AgentOpenCode)
		if err != nil {
			return err
		}
	}
	var cfg Config
	if err := decodeTyped(data, &cfg); err != nil {
		return &Error{Code: ConfigInvalid}
	}
	unsupported := false
	_ = os.Expand(cfg.Notifications.Webhook.URL, func(key string) string {
		unsupported = unsupported || key != OpenCodeWebhookURLEnv
		return ""
	})
	if unsupported {
		return &Error{Code: ConfigOpenCodeWebhookEnvUnsupported}
	}
	return nil
}

// ManagedOpenCodeDocument is an observation of the installed route, never a
// config path override. The opaque revision binds bytes, installation identity,
// generation and origin; updates require a new observation after setup changes.
type ManagedOpenCodeDocument struct {
	Selection Selection
	Document  Document
	Revision  string
	snapshot  installruntime.PolicySnapshot
	root      string
}

func managedRevision(d Document, l installruntime.Ledger) string {
	metadata, _ := json.Marshal(l)
	sum := sha256.Sum256(append([]byte(d.Revision()+"\x00"), metadata...))
	return hex.EncodeToString(sum[:])
}

// ReadManagedOpenCode uses the normal control location unless an operator selects
// an existing control root. Neither notification config nor event environment
// overrides are consulted. The installed, origin-bound registration must prove
// the exact executable/control-root route before a policy is exposed.
func ReadManagedOpenCode(ctx context.Context, root string, assets AssetContext) (ManagedOpenCodeDocument, error) {
	ctx, cancel := context.WithTimeout(ctx, MutationTimeout)
	defer cancel()
	var result ManagedOpenCodeDocument
	var err error
	if root == "" {
		root, err = installruntime.ControlRoot()
	}
	if err != nil || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return result, &Error{Code: ConfigInvalid}
	}
	root, err = installruntime.CanonicalPath(root)
	if err != nil || installruntime.CheckPrivateControlRoot(root) != nil {
		return result, &Error{Code: ConfigInvalid}
	}
	result.root = root
	result.Selection = Selection{Path: filepath.Join(root, "agent-notifications.json"), Source: "managed-opencode"}
	s, err := installruntime.ReadPolicySnapshot(ctx, root)
	if err != nil {
		return result, managedPolicyError(ctx, ConfigInvalid)
	}
	if s.Installation.Recovery {
		return result, &Error{Code: ConfigRecoveryRequired}
	}
	if !validManagedOpenCode(root, s) {
		return result, &Error{Code: ConfigInvalid}
	}
	// Pin both existing locks while obtaining the exact bytes used for revision.
	// Serializing Fields would lose the policy's raw CAS preimage and formatting.
	_, release, err := installruntime.AcquirePolicyLease(ctx, root, s)
	if err != nil {
		return result, managedPolicyError(ctx, ConfigChanged)
	}
	defer release()
	// The lease already holds both locks, including on Windows. Use the
	// single-inode reader directly rather than trying to lock the file again.
	metadata, err := readFileSnapshotUnmanaged(filepath.Join(root, "ownership.json"), MaxDocumentBytes)
	if err != nil || strictjson.Validate(metadata.Bytes, strictjson.Budget{Bytes: MaxDocumentBytes, Depth: 32, Entries: 65536}) != nil || ambiguousFields(metadata.Bytes, reflect.TypeOf(installruntime.Ledger{}), "") != "" {
		return result, &Error{Code: ConfigInvalid}
	}
	var ledger installruntime.Ledger
	if json.Unmarshal(metadata.Bytes, &ledger) != nil || !reflect.DeepEqual(ledger, s.Installation.Ledger) {
		return result, &Error{Code: ConfigChanged}
	}
	snap, err := readFileSnapshotUnmanaged(result.Selection.Path, MaxDocumentBytes)
	if err != nil {
		return result, managedPolicyError(ctx, ConfigInvalid)
	}
	sum := sha256.Sum256(snap.Bytes)
	if snap.PhysicalPath != result.Selection.Path || hex.EncodeToString(sum[:]) != s.Preimage.SHA256 {
		return result, &Error{Code: ConfigChanged}
	}
	d, err := ParseDocument(snap.Bytes, snap.PhysicalPath, true)
	if err != nil {
		return result, err
	}
	assets.Agent = AgentOpenCode
	assets.PluginRoot = s.Installation.Ledger.Consumers[managedOpenCodeConsumer].RuntimeRoot
	if _, err = d.Effective(assets); err != nil {
		return result, err
	}
	result.Selection.Exists = true
	result.Document, result.snapshot = d, s
	result.Revision = managedRevision(d, s.Installation.Ledger)
	return result, nil
}

func validManagedOpenCode(root string, s installruntime.PolicySnapshot) bool {
	l := s.Installation.Ledger
	c, ok := l.Consumers[managedOpenCodeConsumer]
	if !ok || l.ID == "" || l.Owner != "existing-installer" || l.Schema != 4 || l.WriterFloor != installruntime.OpenCodeWriterFloor ||
		l.PolicyGeneration == 0 || !filepath.IsAbs(l.RuntimeRoot) || filepath.Clean(l.RuntimeRoot) != l.RuntimeRoot ||
		!s.Preimage.Exists || s.Preimage.Link != "" || c.OpenCode == nil || !c.OpenCode.Valid() || !c.OpenCode.OriginBound ||
		!filepath.IsAbs(c.RuntimeRoot) || filepath.Clean(c.RuntimeRoot) != c.RuntimeRoot ||
		!filepath.IsAbs(c.Registration) || filepath.Base(c.Registration) != "agent-notifications.js" || len(c.Commands) != 4 ||
		c.Commands[1] != "opencode-event" || c.Commands[2] != "--protocol" || c.Commands[3] != "1" {
		return false
	}
	binary := "claude-notifications-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if c.Commands[0] != filepath.Join(c.RuntimeRoot, binary) {
		return false
	}
	executable, owned := installruntime.OwnedFile(l, c.Commands[0])
	if !owned || !executable.Exists || executable.Link != "" {
		return false
	}
	bundle, owned := installruntime.OwnedFile(l, c.Registration)
	if !owned || !bundle.Exists || bundle.Link != "" || bundle.SHA256 != c.OpenCode.BundleSHA256 {
		return false
	}
	// Re-render only the embedded asset. This verifies that the persisted owned
	// plugin actually routes to this control root, without evaluating any JS.
	rendered, err := (opencodeplugin.RegistrationRenderer{}).RenderRegistration(c.Commands[0], root, c.OpenCode.Origin)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(rendered)
	return hex.EncodeToString(sum[:]) == bundle.SHA256
}

func managedPolicyError(ctx context.Context, code Code) error {
	if ctx.Err() != nil {
		code = ConfigLockTimeout
	}
	return &Error{Code: code}
}

// ApplyManagedOpenCodeEdits is a Store-facing adapter: the existing raw editor
// validates supported config leaves; the existing managed transaction publishes
// the exact policy file, policy generation and ledger under its standard locks.
// Setup consent, origin and all unedited raw fields remain immutable here.
func ApplyManagedOpenCodeEdits(ctx context.Context, root string, assets AssetContext, expect string, edits Edits) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, MutationTimeout)
	defer cancel()
	if expect == "" {
		return Result{}, &Error{Code: ConfigConflict}
	}
	current, err := ReadManagedOpenCode(ctx, root, assets)
	result := Result{Selection: current.Selection, Revision: current.Revision}
	if err != nil {
		return result, err
	}
	if current.Revision != expect {
		return result, &Error{Code: ConfigConflict}
	}
	assets.Agent = AgentOpenCode
	assets.PluginRoot = current.snapshot.Installation.Ledger.Consumers[managedOpenCodeConsumer].RuntimeRoot
	next, err := ApplyRawEdits(current.Document, edits, assets)
	if err != nil {
		return result, err
	}
	if err = next.validateOpenCodeWebhookEnv(); err != nil {
		return result, err
	}
	// The managed policy has the kernel's smaller budget, even though the
	// ordinary raw config editor also serves documents up to four MiB.
	if strictjson.Validate(next.Bytes(), strictjson.Budget{Bytes: 64 * 1024, Depth: 16, Entries: 1024}) != nil {
		return result, &Error{Code: ConfigInvalid}
	}
	if bytes.Equal(current.Document.Bytes(), next.Bytes()) {
		return result, nil
	}
	s := current.snapshot
	l := s.Installation.Ledger
	consumer := l.Consumers[managedOpenCodeConsumer]
	// This existing-only policy seam refuses recovery before CAS. No separate writer,
	// lock implementation, policy engine or generation file is introduced.
	committed, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: current.root, RuntimeRoot: consumer.RuntimeRoot, Owner: l.Owner, ConsumerID: managedOpenCodeConsumer,
		PolicyOnly: true, RefreshOnly: true, ExpectedGeneration: &l.Generation, ExpectedPolicy: &s.Preimage,
		PolicyDocument: next.Bytes(),
		Prepare: func() ([]installruntime.File, error) {
			metadata, e := readFileSnapshotUnmanaged(filepath.Join(current.root, "ownership.json"), MaxDocumentBytes)
			var observed installruntime.Ledger
			if e != nil || strictjson.Validate(metadata.Bytes, strictjson.Budget{Bytes: MaxDocumentBytes, Depth: 32, Entries: 65536}) != nil ||
				ambiguousFields(metadata.Bytes, reflect.TypeOf(observed), "") != "" || json.Unmarshal(metadata.Bytes, &observed) != nil || !reflect.DeepEqual(observed, l) {
				return nil, &Error{Code: ConfigConflict}
			}
			return nil, nil
		},
	})
	if err != nil {
		if errors.Is(err, installruntime.ErrPolicyRecovery) {
			return result, &Error{Code: ConfigRecoveryRequired}
		}
		if errors.Is(err, installruntime.ErrPolicyConflict) {
			return result, managedPolicyError(ctx, ConfigConflict)
		}
		// A kernel failure may follow journal or file publication. Do not tell
		// the operator that nothing happened; recovery/inspection must decide.
		return result, &Error{Code: ConfigCommitUncertain}
	}
	d, err := ParseDocument(next.Bytes(), current.Selection.Path, true)
	if err != nil {
		return result, &Error{Code: ConfigCommitUncertain}
	}
	result.Changed = true
	result.Revision = managedRevision(d, committed)
	return result, nil
}
