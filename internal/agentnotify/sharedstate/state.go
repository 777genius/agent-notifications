// Package sharedstate owns only private, ephemeral shared-MCP discovery state.
package sharedstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/strictjson"
)

const recordName = "endpoint.json"
const maxRecord = 4096

// Record describes one authenticated loopback endpoint. PID is diagnostic only.
// Token is a random 32-byte secret encoded as lowercase hexadecimal.
type Record struct {
	Version int    `json:"version"`
	Address string `json:"address"`
	Token   string `json:"token"`
	PID     int    `json:"pid"`
}

type document struct {
	Identity string `json:"identity"`
	Record
}

type Store struct {
	root, namespace, dir, identity string
	ids                            []string
	mu                             sync.Mutex
}

// Open requires an existing private plugin data root. It creates only ephemeral
// discovery directories and permanent lock inodes, never policy or journals.
func Open(dataRoot, bindingKey, runtimeSHA string) (*Store, error) {
	if !hash(bindingKey) || !hash(runtimeSHA) {
		return nil, errors.New("invalid shared endpoint identity")
	}
	if !filepath.IsAbs(dataRoot) || filepath.Clean(dataRoot) != dataRoot {
		return nil, errors.New("shared endpoint root must be absolute and clean")
	}
	root, err := installruntime.PhysicalPath(dataRoot)
	if err != nil {
		return nil, errors.New("invalid shared endpoint root")
	}
	identity := bindingKey + ":" + runtimeSHA
	digest := sha256.Sum256([]byte(identity))
	namespace := filepath.Join(root, ".shared-mcp")
	s := &Store{root: root, namespace: namespace, dir: filepath.Join(namespace, hex.EncodeToString(digest[:])), identity: identity}
	d, ids, err := openDirectory(s, true)
	if err != nil {
		return nil, safeError("open", err)
	}
	defer d.close()
	s.ids = ids
	for _, name := range []string{"start.lock", "server.lock"} {
		if err := d.ensureLock(name); err != nil {
			return nil, safeError("lock preparation", err)
		}
	}
	return s, nil
}

func (s *Store) Identity() string { return s.identity }

func hash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func validRecord(r Record) bool {
	if r.Version != 1 || !hash(r.Token) || r.PID <= 0 {
		return false
	}
	host, port, err := net.SplitHostPort(r.Address)
	if err != nil || host != "127.0.0.1" {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n <= 65535 && port == strconv.Itoa(n)
}

func safeError(action string, err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return &os.PathError{Op: "shared endpoint " + action, Err: os.ErrNotExist}
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("shared endpoint %s: %w", action, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("shared endpoint %s: %w", action, context.DeadlineExceeded)
	}
	// Neither untrusted JSON bytes nor token-bearing input errors escape here.
	return fmt.Errorf("shared endpoint %s refused", action)
}

func (s *Store) read(d *directory) (Record, error) {
	data, err := d.read(recordName, maxRecord)
	if err != nil {
		return Record{}, err
	}
	if err := strictjson.Validate(data, strictjson.Budget{Bytes: maxRecord, Depth: 1, Entries: 5}); err != nil {
		return Record{}, errors.New("invalid endpoint document")
	}
	// Typed JSON decoding accepts case aliases, so check the exact schema keys
	// before decoding. Strict validation above also rejects escaped duplicates.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || len(fields) != 5 {
		return Record{}, errors.New("invalid endpoint document")
	}
	for key := range fields {
		switch key {
		case "identity", "version", "address", "token", "pid":
		default:
			return Record{}, errors.New("invalid endpoint document")
		}
	}
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return Record{}, errors.New("invalid endpoint document")
	}
	if doc.Identity != s.identity || !validRecord(doc.Record) {
		return Record{}, errors.New("invalid endpoint document")
	}
	return doc.Record, nil
}

// Read returns os.ErrNotExist for an absent endpoint, and refuses invalid state.
func (s *Store) Read() (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, _, err := openDirectory(s, false)
	if err != nil {
		return Record{}, safeError("read", err)
	}
	defer d.close()
	r, err := s.read(d)
	if err != nil {
		return Record{}, safeError("read", err)
	}
	return r, nil
}

// Publish atomically replaces an absent or valid owned endpoint. The caller must
// hold LockServer through publication and the endpoint's entire serving lifetime.
// Corrupt and unowned state is preserved and never repaired implicitly.
func (s *Store) Publish(r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validRecord(r) {
		return errors.New("invalid shared endpoint record")
	}
	d, _, err := openDirectory(s, false)
	if err != nil {
		return safeError("publication", err)
	}
	defer d.close()
	if _, err = s.read(d); err != nil && !os.IsNotExist(err) {
		return safeError("publication", err)
	}
	data, err := json.Marshal(document{Identity: s.identity, Record: r})
	if err == nil {
		err = d.write(recordName, data)
	}
	if err != nil {
		return safeError("publication", err)
	}
	return nil
}

// Remove clears only the exact expected endpoint. A newer record is preserved.
// The caller must retain LockServer until removal completes.
func (s *Store) Remove(expected Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validRecord(expected) {
		return errors.New("invalid shared endpoint record")
	}
	d, _, err := openDirectory(s, false)
	if err != nil {
		return safeError("removal", err)
	}
	defer d.close()
	got, err := s.read(d)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return safeError("removal", err)
	}
	if got != expected {
		return nil
	}
	if err = d.remove(recordName); err != nil {
		return safeError("removal", err)
	}
	return nil
}

func (s *Store) LockStart(ctx context.Context) (func(), error)  { return s.lock(ctx, "start.lock") }
func (s *Store) LockServer(ctx context.Context) (func(), error) { return s.lock(ctx, "server.lock") }
func (s *Store) lock(ctx context.Context, name string) (func(), error) {
	d, _, err := openDirectory(s, false)
	if err != nil {
		return nil, safeError("lock", err)
	}
	if err = d.checkLock(name); err != nil {
		d.close()
		return nil, safeError("lock", err)
	}
	// Keeping directories pinned prevents Windows ancestor moves while acquiring.
	release, err := installruntime.LockExisting(ctx, filepath.Join(s.dir, name))
	if err != nil {
		d.close()
		return nil, safeError("lock", err)
	}
	checked, _, err := openDirectory(s, false)
	if err == nil {
		err = checked.checkLock(name)
		checked.close()
	}
	if err != nil {
		release()
		d.close()
		return nil, safeError("lock", err)
	}
	var once sync.Once
	return func() { once.Do(func() { release(); d.close() }) }, nil
}
