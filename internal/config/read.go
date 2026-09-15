package config

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Snapshot is a single regular-file read and its physical identity. Adapters
// must enforce limit before allocation and reject nonregular/dangling entries.
type Snapshot struct {
	Bytes        []byte
	PhysicalPath string
}

// HistoricalCandidate is supplied by the composition root, never discovered by
// Resolve. TrustedBaseline must be from the candidate's verified exact version;
// nil means unknown and cannot authorize ignoring historical user settings.
type HistoricalCandidate struct {
	Path            string `json:"path"`
	BaselinePath    string `json:"baselinePath,omitempty"`
	BaselineSHA256  string `json:"baselineSHA256,omitempty"`
	TrustedBaseline []byte `json:"-"`
}
type LegacyContext struct{ Candidates []HistoricalCandidate }
type ReadRequest struct {
	Env          EnvSnapshot
	Assets       AssetContext
	Legacy       LegacyContext
	ReadSnapshot func(path string, limit int) (Snapshot, error)
}

// ReadDocument is the transitional internal read API (Load(string) remains the
// legacy production API). It performs no writes, migration, or runtime effects.
// Managed Windows snapshots acquire existing shared locks without creating them.
func ReadDocument(r ReadRequest) (Document, *Config, error) {
	d, c, _, err := ReadDocumentSelection(r)
	return d, c, err
}

// ReadDocumentSelection returns the selection which was revalidated around
// the returned bytes. Consumers that display a path and revision must use this
// value instead of resolving separately.
func ReadDocumentSelection(r ReadRequest) (Document, *Config, Selection, error) {
	if r.ReadSnapshot == nil {
		return Document{}, nil, Selection{}, &Error{Code: ConfigInvalid}
	}
	selectedPreviously := false
	for attempt := 0; attempt < 3; attempt++ {
		s, err := resolveForRead(r.Env)
		if err != nil {
			return Document{}, nil, s, err
		}
		if !s.Exists {
			if selectedPreviously {
				return Document{}, nil, s, &Error{Code: ConfigChanged, Path: s.Path}
			}
			if s.Source == "explicit" {
				return Document{}, nil, s, &Error{Code: ConfigMissing, Path: s.Path}
			}
			if err := CheckHistorical(r.Legacy, r.ReadSnapshot); err != nil {
				return Document{}, nil, s, err
			}
			next, err := resolveForRead(r.Env)
			if err != nil {
				return Document{}, nil, next, err
			}
			if next.Path != s.Path || next.Exists {
				continue
			}
			d, err := SeedDocument(s.Path)
			if err != nil {
				return Document{}, nil, s, err
			}
			c, err := d.Effective(r.Assets)
			return d, c, s, err
		}
		selectedPreviously = true
		snap, err := r.ReadSnapshot(s.Path, MaxDocumentBytes)
		if err != nil {
			if os.IsNotExist(err) {
				return Document{}, nil, s, &Error{Code: ConfigChanged, Path: s.Path}
			}
			var ce *Error
			if errors.As(err, &ce) && ce.Code == ConfigChanged {
				continue
			}
			return Document{}, nil, s, pathError(s.Path, err)
		}
		d, err := ParseDocument(snap.Bytes, snap.PhysicalPath, true)
		if err != nil {
			return Document{}, nil, s, err
		}
		c, err := d.Effective(r.Assets)
		if err != nil {
			return Document{}, nil, s, &Error{Code: ConfigInvalid, Path: s.Path}
		}
		next, err := resolveForRead(r.Env)
		if err != nil {
			return Document{}, nil, next, err
		}
		if !next.Exists {
			return Document{}, nil, s, &Error{Code: ConfigChanged, Path: s.Path}
		}
		if next.Path != s.Path {
			continue
		}
		return d, c, s, nil
	}
	return Document{}, nil, Selection{}, &Error{Code: ConfigChanged}
}

// readFileSnapshotUnmanaged permits final links to regular files and verifies
// one opened inode. The OS-specific public adapter adds managed read locking.
func readFileSnapshotUnmanaged(p string, limit int) (Snapshot, error) {
	if !filepath.IsAbs(p) || limit < 0 || limit > MaxDocumentBytes {
		return Snapshot{}, &Error{Code: ConfigInvalid, Path: p}
	}
	info, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			if _, entryErr := os.Lstat(p); entryErr == nil {
				return Snapshot{}, &Error{Code: ConfigInvalid, Path: p}
			}
		}
		return Snapshot{}, err
	}
	if !info.Mode().IsRegular() {
		return Snapshot{}, &Error{Code: ConfigInvalid, Path: p}
	}
	physical, err := filepath.EvalSymlinks(p)
	if err != nil {
		return Snapshot{}, err
	}
	f, err := openSnapshotFile(physical)
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = f.Close() }()
	after, err := f.Stat()
	if err != nil {
		return Snapshot{}, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(info, after) {
		return Snapshot{}, &Error{Code: ConfigChanged, Path: p}
	}
	if after.Size() > int64(limit) {
		return Snapshot{}, &Error{Code: ConfigInvalid, Path: p}
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return Snapshot{}, err
	}
	if len(data) > limit {
		return Snapshot{}, &Error{Code: ConfigInvalid, Path: p}
	}
	return Snapshot{Bytes: data, PhysicalPath: physical}, nil
}
