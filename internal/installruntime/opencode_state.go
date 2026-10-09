package installruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/777genius/agent-notifications/internal/strictjson"
)

// OpenCode private Init/Purge recovery must be refused by the published Local3 kernel
// before it reads blobs or publishes generic files. Ledger/journal schema4 is retained.
const OpenCodeWriterFloor = 4
const OpenCodeWriterProtocolMarker = "agent-notifications-managed-writer-protocol-v4"
const OpenCodeStoreLimit = 1 << 20
const OpenCodeStoreLock = ".opencode-admission.lock"
const openCodeConsumer = "opencode-notifications"

// OpenCodeRegistration is private persisted protocol, never public diagnostics.
type OpenCodeRegistration struct {
	Origin, Salt, Namespace, BundleSHA256 string
	OriginBound                           bool
}

func (r OpenCodeRegistration) Valid() bool {
	return ownedTransactionBlobName(r.Origin) && ownedTransactionBlobName(r.Salt) &&
		ownedTransactionBlobName(r.Namespace) && ownedTransactionBlobName(r.BundleSHA256)
}

func openCodeStatePath(root string, r OpenCodeRegistration) string {
	return filepath.Join(root, "opencode-admission", r.Namespace, "claims.json")
}

type openCodeDocument struct {
	Schema                    int
	Origin, Namespace, SHA256 string
	Payload                   json.RawMessage
}

func encodeOpenCodeDocument(r OpenCodeRegistration, payload []byte) ([]byte, error) {
	if strictjson.Validate(payload, strictjson.Budget{Bytes: OpenCodeStoreLimit - 1024, Depth: 8, Entries: 4096}) != nil {
		return nil, fmt.Errorf("invalid private store payload")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, payload); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(compact.Bytes())
	return json.Marshal(openCodeDocument{1, r.Origin, r.Namespace, hex.EncodeToString(sum[:]), compact.Bytes()})
}

// OpenCodeStore is the permanent private lock below the component/policy fence.
// Event callers use existing-only acquisition. Close before provider IO.
type OpenCodeStore struct {
	path         string
	registration OpenCodeRegistration
	anchors      []PathAnchor
	release      func()
}

func AcquireOpenCodeStore(ctx context.Context, root string, r OpenCodeRegistration) (*OpenCodeStore, error) {
	if !r.Valid() || privateDirectory(root) != nil {
		return nil, fmt.Errorf("invalid private registration")
	}
	release, err := LockExisting(ctx, filepath.Join(root, OpenCodeStoreLock))
	if err != nil {
		return nil, err
	}
	s := &OpenCodeStore{path: openCodeStatePath(root, r), registration: r, release: release}
	for _, dir := range []string{filepath.Join(root, "opencode-admission"), filepath.Dir(s.path)} {
		if err = privateDirectory(dir); err != nil {
			s.Close()
			return nil, err
		}
	}
	s.anchors, err = pathAnchors(s.path, false)
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *OpenCodeStore) Close() {
	if s.release != nil {
		s.release()
		s.release = nil
	}
}

func (s *OpenCodeStore) Read() ([]byte, error) {
	if s.release == nil {
		return nil, fmt.Errorf("private store closed")
	}
	// Reuse the kernel's owned, private, single-link regular inode validation;
	// opening here does not acquire another lock or mutate the document.
	f, err := openLock(s.path, false)
	if err != nil {
		return nil, err
	}
	info, statErr := f.Stat()
	_ = f.Close()
	if statErr != nil || info.Size() > OpenCodeStoreLimit {
		return nil, fmt.Errorf("private store exceeds bound")
	}
	got, err := Fingerprint(s.path)
	if err != nil || !got.Exists || got.Link != "" || got.Mode != identityMode(0600) {
		return nil, fmt.Errorf("private store unavailable")
	}
	data, err := readRegularFileLimit(s.path, OpenCodeStoreLimit)
	if err != nil || identity(data, 0600) != got {
		return nil, fmt.Errorf("private store changed")
	}
	if strictjson.Validate(data, strictjson.Budget{Bytes: OpenCodeStoreLimit, Depth: 10, Entries: 4096}) != nil {
		return nil, fmt.Errorf("invalid private store")
	}
	var d openCodeDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&d) != nil || d.Schema != 1 || d.Origin != s.registration.Origin || d.Namespace != s.registration.Namespace {
		return nil, fmt.Errorf("private store binding mismatch")
	}
	sum := sha256.Sum256(d.Payload)
	if d.SHA256 != hex.EncodeToString(sum[:]) {
		return nil, fmt.Errorf("private store checksum mismatch")
	}
	return d.Payload, nil
}

func (s *OpenCodeStore) Write(payload []byte) error {
	if _, err := s.Read(); err != nil {
		return err
	}
	before, err := Fingerprint(s.path)
	if err != nil {
		return err
	}
	data, err := encodeOpenCodeDocument(s.registration, payload)
	if err != nil {
		return err
	}
	return safePublish(File{Path: s.path, Before: before, Data: data, Mode: 0600, Parents: s.anchors}, true)
}

// prepareOpenCodeState runs only under existing Commit locks. Init and purge are
// closed redo decisions; no transient adapter callback is recovery authority.
func prepareOpenCodeState(ctx context.Context, root string, before, after Ledger, policy map[string]json.RawMessage) (*File, *PurgeTree, error) {
	old := before.Consumers[openCodeConsumer].OpenCode
	consumer := after.Consumers[openCodeConsumer]
	next := consumer.OpenCode
	if old == nil && next == nil {
		return nil, nil, nil
	}
	if next != nil {
		owned, ok := OwnedFile(after, consumer.Registration)
		if !next.Valid() || !ok || owned.Link != "" || owned.SHA256 != next.BundleSHA256 {
			return nil, nil, fmt.Errorf("OpenCode registration/bundle mismatch")
		}
	}
	if old != nil {
		if next != nil && (old.Origin != next.Origin || old.Salt != next.Salt || old.Namespace != next.Namespace) {
			return nil, nil, fmt.Errorf("OpenCode incarnation cannot change before completed removal")
		}
		s, err := AcquireOpenCodeStore(ctx, root, *old)
		if err != nil {
			return nil, nil, err
		}
		defer s.Close()
		if _, err = s.Read(); err != nil {
			return nil, nil, err
		}
		if next == nil {
			if _, exists := after.Consumers[openCodeConsumer]; exists {
				return nil, nil, fmt.Errorf("OpenCode registration cannot be dropped")
			}
			var route struct {
				Channels struct{ Desktop, Webhook *bool } `json:"openCodeNotifications"`
			}
			if json.Unmarshal(policy["route"], &route) != nil || route.Channels.Desktop == nil || route.Channels.Webhook == nil || *route.Channels.Desktop || *route.Channels.Webhook {
				return nil, nil, fmt.Errorf("OpenCode purge requires revoked channels")
			}
			path := filepath.Dir(s.path)
			dir, err := os.Open(path)
			if err != nil {
				return nil, nil, err
			}
			entriesAtRoot, readErr := dir.ReadDir(2)
			_ = dir.Close()
			if (readErr != nil && len(entriesAtRoot) == 0) || len(entriesAtRoot) != 1 || entriesAtRoot[0].Name() != "claims.json" {
				return nil, nil, fmt.Errorf("unexpected private store entries")
			}
			entries, err := capturePurgeTree(path)
			if err != nil {
				return nil, nil, err
			}
			if len(entries) != 2 {
				return nil, nil, fmt.Errorf("unexpected private store entries")
			}
			return nil, &PurgeTree{path, entries}, nil
		}
		return nil, nil, nil
	}
	// An originless upgrade must prove the existing owned bundle, not adopt a
	// foreign registration or a legacy store as a fresh empty claim horizon.
	if legacy, exists := before.Consumers[openCodeConsumer]; exists {
		id, ok := OwnedFile(before, legacy.Registration)
		if !ok || !id.Exists || legacy.Registration != consumer.Registration {
			return nil, nil, fmt.Errorf("originless registration is not owned")
		}
	}
	release, err := Lock(ctx, filepath.Join(root, OpenCodeStoreLock))
	if err != nil {
		return nil, nil, err
	}
	defer release()
	path := openCodeStatePath(root, *next)
	if _, err = os.Lstat(filepath.Dir(path)); !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("private namespace already exists")
	}
	data, err := encodeOpenCodeDocument(*next, []byte(`{}`))
	if err != nil {
		return nil, nil, err
	}
	anchors, err := pathAnchors(path, false)
	if err != nil {
		return nil, nil, err
	}
	return &File{Path: path, Data: data, Mode: 0600, Parents: anchors}, nil, nil
}

func validateOpenCodeDecision(root string, tx transaction) error {
	old, next := tx.Before.Consumers[openCodeConsumer].OpenCode, tx.After.Consumers[openCodeConsumer].OpenCode
	if old == nil && next != nil && tx.OpenCodeInit == nil || old != nil && next == nil && tx.OpenCodePurge == nil {
		return fmt.Errorf("missing private recovery decision")
	}
	if old != nil && next != nil && (old.Origin != next.Origin || old.Salt != next.Salt || old.Namespace != next.Namespace || old.OriginBound && !next.OriginBound) {
		return fmt.Errorf("private recovery changes incarnation")
	}
	if next != nil && !reflect.DeepEqual(old, next) {
		owned, ok := OwnedFile(tx.After, tx.After.Consumers[openCodeConsumer].Registration)
		if (tx.After.Schema != 4 && tx.After.Schema != 5) || tx.After.WriterFloor < OpenCodeWriterFloor || !next.Valid() || !ok || owned.SHA256 != next.BundleSHA256 || owned.Link != "" {
			return fmt.Errorf("private recovery bundle mismatch")
		}
	}
	if tx.OpenCodeInit == nil && tx.OpenCodePurge == nil {
		return nil
	}
	if (tx.Schema != 4 && tx.Schema != 5) || (tx.After.Schema != 4 && tx.After.Schema != 5) || tx.After.WriterFloor < OpenCodeWriterFloor || tx.Rollback {
		return fmt.Errorf("invalid private state protocol")
	}
	if f := tx.OpenCodeInit; f != nil {
		r := tx.After.Consumers[openCodeConsumer].OpenCode
		if r == nil || !r.Valid() || tx.Before.Consumers[openCodeConsumer].OpenCode != nil || f.Path != openCodeStatePath(root, *r) || f.Before.Exists || f.Remove || f.Link != "" || f.Mode != 0600 || tx.OpenCodePurge != nil {
			return fmt.Errorf("invalid private initialization")
		}
		seed, err := encodeOpenCodeDocument(*r, []byte(`{}`))
		if err != nil || !bytes.Equal(seed, f.Data) {
			return fmt.Errorf("invalid private seed")
		}
	}
	if p := tx.OpenCodePurge; p != nil {
		r := tx.Before.Consumers[openCodeConsumer].OpenCode
		if r == nil || !r.Valid() || p.Path != filepath.Dir(openCodeStatePath(root, *r)) || len(p.Entries) != 2 || tx.After.Consumers[openCodeConsumer].OpenCode != nil {
			return fmt.Errorf("invalid private purge")
		}
		if _, exists := tx.After.Consumers[openCodeConsumer]; exists {
			return fmt.Errorf("purge retains consumer")
		}
		leaf := p.Entries["claims.json"]
		if p.Entries["."].Directory == "" || !leaf.File.Exists || leaf.ObjectID == "" || leaf.File.Link != "" || leaf.Directory != "" || leaf.File.Mode != identityMode(0600) {
			return fmt.Errorf("invalid private purge identities")
		}
	}
	return nil
}

func recoverOpenCodeState(ctx context.Context, root string, tx transaction, fault func(string) error) error {
	if err := validateOpenCodeDecision(root, tx); err != nil {
		return err
	}
	if tx.OpenCodeInit == nil && tx.OpenCodePurge == nil {
		return nil
	}
	release, err := LockExisting(ctx, filepath.Join(root, OpenCodeStoreLock))
	if err != nil {
		return err
	}
	defer release()
	if tx.OpenCodeInit != nil {
		if err = safePublish(*tx.OpenCodeInit, true); err != nil {
			return err
		}
		if fault != nil {
			return fault("opencode-init")
		}
	}
	if tx.OpenCodePurge != nil {
		return cleanupPurgeTree(*tx.OpenCodePurge, fault)
	}
	return nil
}

func protectOpenCodeProtocol(before, after Ledger) error {
	old := before.Consumers[openCodeConsumer].OpenCode
	next := after.Consumers[openCodeConsumer].OpenCode
	if old != nil && next != nil && old.OriginBound && !next.OriginBound {
		return fmt.Errorf("cannot downgrade origin binding")
	}
	for id, c := range after.Consumers {
		if c.OpenCode != nil && (id != openCodeConsumer || !c.OpenCode.Valid()) {
			return fmt.Errorf("invalid consumer private protocol")
		}
	}
	return nil
}
