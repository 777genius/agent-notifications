package notifier

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier/nativeprotocol"
)

var errSpoolChanging = errors.New("native receipt publication in progress")

const maxSpoolAttempts = 64
const maxSpoolBytes = 2 * 1024 * 1024

// PrivateNativeSpool is used only by notify/setup, never read-only status.
// Root must already be a setup-owned private directory on a local filesystem.
// All physical paths are derived from EvalSymlinks before creating files.
type PrivateNativeSpool struct {
	Root  string
	Clock BootClock
}
type spoolOwner struct {
	SchemaVersion                int
	CorrelationID, Nonce, BootID string
	NotAfter                     float64
}

func uuidString() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') && !(c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
func readPrivate(path string, limit int) ([]byte, error) {
	f, err := openPrivate(path, false, false, false)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() <= 0 || info.Size() > int64(limit) {
		return nil, errors.New("invalid private file size")
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil || len(data) > limit || int64(len(data)) != info.Size() {
		return nil, errors.New("invalid private file")
	}
	return data, nil
}
func writePrivate(path string, data []byte) error {
	f, err := openPrivate(path, false, true, true)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func (s *PrivateNativeSpool) root() (string, error) {
	if !filepath.IsAbs(s.Root) {
		return "", errors.New("spool root must be absolute")
	}
	root, err := filepath.EvalSymlinks(s.Root)
	if err != nil {
		return "", err
	}
	f, err := openPrivate(root, true, false, false)
	if err != nil {
		return "", err
	}
	f.Close()
	return root, nil
}

// sweepLocked is bounded in entries AND bytes, under a permanent kernel lock.
// Unknown entries are never deleted; an overfull/corrupt spool fails closed.
func (s *PrivateNativeSpool) sweepLocked(ctx context.Context, root string) (int, int64, error) {
	boot, now, err := s.Clock.Now()
	if err != nil || boot == "" || now < 0 || math.IsNaN(now) || math.IsInf(now, 0) {
		return 0, 0, errors.New("clock unavailable")
	}
	f, err := openPrivate(root, true, false, false)
	if err != nil {
		return 0, 0, err
	}
	entries, err := f.ReadDir(maxSpoolAttempts + 2)
	f.Close()
	if err != nil && err != io.EOF {
		return 0, 0, err
	}
	if len(entries) > maxSpoolAttempts+1 {
		return 0, 0, errors.New("spool full")
	}
	count := 0
	var size int64
	for _, entry := range entries {
		if ctx.Err() != nil {
			return 0, 0, ctx.Err()
		}
		if entry.Name() == ".spool.lock" {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".prepare.") || strings.HasPrefix(entry.Name(), ".expired.") {
			parts := strings.Split(strings.TrimPrefix(strings.TrimPrefix(entry.Name(), ".prepare."), ".expired."), ".")
			if len(parts) != 2 || !isUUID(parts[0]) || !isUUID(parts[1]) {
				return 0, 0, errors.New("invalid staging entry")
			}
			// Native can only receive the final UUID directory. While holding
			// the spool lock no creator is alive in this staging transaction.
			if err := s.removeAttempt(filepath.Join(root, entry.Name()), parts[1]); err != nil {
				if !errors.Is(err, errSpoolChanging) {
					return 0, 0, err
				}
				count++
				size += 2*nativeprotocol.MaxEnvelopeBytes + 1024
			}
			continue
		}
		if !isUUID(entry.Name()) || !entry.IsDir() {
			return 0, 0, errors.New("foreign spool entry")
		}
		dir := filepath.Join(root, entry.Name())
		data, err := readPrivate(filepath.Join(dir, "owner.json"), 1024)
		if err != nil {
			return 0, 0, err
		}
		var owner spoolOwner
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&owner) != nil || owner.SchemaVersion != 1 || owner.CorrelationID != entry.Name() || !isUUID(owner.Nonce) || owner.BootID == "" || len(owner.BootID) > 256 || owner.NotAfter <= 0 || math.IsNaN(owner.NotAfter) || math.IsInf(owner.NotAfter, 0) {
			return 0, 0, errors.New("invalid spool owner")
		}
		if owner.BootID != boot || now >= owner.NotAfter {
			if err = s.expireLocked(root, NativeAttempt{Directory: dir, Nonce: owner.Nonce}); err != nil {
				if !errors.Is(err, errSpoolChanging) {
					return 0, 0, err
				}
				count++
				size += 2*nativeprotocol.MaxEnvelopeBytes + 1024
			}
			continue
		}
		df, err := openPrivate(dir, true, false, false)
		if err != nil {
			return 0, 0, err
		}
		files, err := df.ReadDir(9)
		df.Close()
		if (err != nil && err != io.EOF) || len(files) > 8 {
			return 0, 0, errors.New("spool attempt full")
		}
		for _, file := range files {
			bytes, err := spoolEntrySize(file)
			if err != nil {
				return 0, 0, err
			}
			size += bytes
		}
		count++
	}
	return count, size, nil
}
func (s *PrivateNativeSpool) Prepare(ctx context.Context, r notification.Request, encode func(string) ([]byte, error)) (NativeAttempt, error) {
	var attempt NativeAttempt
	if ctx.Err() != nil {
		return attempt, ctx.Err()
	}
	if s.Clock == nil || !isUUID(r.CorrelationID) {
		return attempt, errors.New("invalid attempt")
	}
	root, err := s.root()
	if err != nil {
		return attempt, err
	}
	unlock, err := lockPrivate(ctx, filepath.Join(root, ".spool.lock"), true)
	if err != nil {
		return attempt, err
	}
	defer unlock()
	count, size, err := s.sweepLocked(ctx, root)
	if err != nil {
		return attempt, err
	}
	if count >= maxSpoolAttempts || size+2*nativeprotocol.MaxEnvelopeBytes+1024 > maxSpoolBytes {
		return attempt, errors.New("spool full")
	}
	boot, now, err := s.Clock.Now()
	if err != nil || boot != r.Deadline.BootID || now >= r.Deadline.NotAfter || r.Deadline.NotAfter-now > 15 || ctx.Err() != nil {
		return attempt, errors.New("expired")
	}
	nonce, err := uuidString()
	if err != nil {
		return attempt, err
	}
	data, err := encode(nonce)
	if err != nil || len(data) > nativeprotocol.MaxEnvelopeBytes {
		return attempt, errors.New("invalid request")
	}
	dir := filepath.Join(root, r.CorrelationID)
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		return attempt, errors.New("attempt already exists")
	}
	stage := filepath.Join(root, ".prepare."+r.CorrelationID+"."+nonce)
	if err = os.Mkdir(stage, 0700); err != nil {
		return attempt, err
	}
	owner, _ := json.Marshal(spoolOwner{1, r.CorrelationID, nonce, r.Deadline.BootID, r.Deadline.NotAfter})
	if err = writePrivate(filepath.Join(stage, "owner.json"), owner); err == nil {
		err = writePrivate(filepath.Join(stage, nonce+".request"), data)
	}
	if err == nil {
		err = os.Rename(stage, dir)
	}
	if err != nil {
		_ = s.removeAttempt(stage, nonce)
		return NativeAttempt{}, err
	}
	attempt = NativeAttempt{Directory: dir, RequestPath: filepath.Join(dir, nonce+".request"), ReceiptPath: filepath.Join(dir, nonce+".receipt"), Nonce: nonce}
	return attempt, nil
}
func (s *PrivateNativeSpool) Receipt(a NativeAttempt) ([]byte, error) {
	return readPrivate(a.ReceiptPath, nativeprotocol.MaxEnvelopeBytes)
}
func (s *PrivateNativeSpool) Expire(a NativeAttempt) error {
	root, err := s.root()
	if err != nil {
		return err
	}
	// Cleanup has a short separate bound after delivery cancellation. It does
	// not grant a new send budget, and a failed cleanup never enables retry.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	unlock, err := lockPrivate(ctx, filepath.Join(root, ".spool.lock"), false)
	if err != nil {
		return err
	}
	defer unlock()
	return s.expireLocked(root, a)
}
func (s *PrivateNativeSpool) expireLocked(root string, a NativeAttempt) error {
	if filepath.Dir(a.Directory) != root || !isUUID(filepath.Base(a.Directory)) || !isUUID(a.Nonce) {
		return errors.New("foreign attempt")
	}
	data, err := readPrivate(filepath.Join(a.Directory, "owner.json"), 1024)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var owner spoolOwner
	if json.Unmarshal(data, &owner) != nil {
		return errors.New("invalid attempt owner")
	}
	// A caller's delayed cleanup must not delete a newer attempt at the same
	// correlation path. Compare nonce while holding the same lock as Prepare.
	if owner.CorrelationID != filepath.Base(a.Directory) || owner.Nonce != a.Nonce {
		return nil
	}
	// Rename first so a late LaunchServices reader sees a missing request. A
	// still-running native writer holds a directory descriptor; any late temporary
	// receipt stays in a recognizable tombstone, never an ownerless live entry.
	tombstone := filepath.Join(root, ".expired."+filepath.Base(a.Directory)+"."+a.Nonce)
	if err := os.Rename(a.Directory, tombstone); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return s.removeAttempt(tombstone, a.Nonce)
}

func (s *PrivateNativeSpool) removeAttempt(directory, nonce string) error {
	f, err := openPrivate(directory, true, false, false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	files, err := f.ReadDir(9)
	f.Close()
	if (err != nil && err != io.EOF) || len(files) > 8 {
		return errors.New("unsafe attempt")
	}
	for _, entry := range files {
		name := entry.Name()
		allowed := name == "owner.json" || name == nonce+".request" || name == nonce+".receipt" || name == nonce+".claim" || (strings.HasPrefix(name, nonce+".") && strings.HasSuffix(name, ".tmp") && isUUID(strings.TrimSuffix(strings.TrimPrefix(name, nonce+"."), ".tmp")))
		if !allowed || !entry.Type().IsRegular() {
			return errors.New("foreign attempt file")
		}
	}
	for _, entry := range files {
		if err = os.Remove(filepath.Join(directory, entry.Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	err = os.Remove(directory)
	if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
		return errSpoolChanging
	}
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Cleanup is an explicit bounded notify/setup operation. It is not a status API.
func (s *PrivateNativeSpool) Cleanup(ctx context.Context) error {
	root, err := s.root()
	if err != nil {
		return err
	}
	unlock, err := lockPrivate(ctx, filepath.Join(root, ".spool.lock"), true)
	if err != nil {
		return err
	}
	defer unlock()
	_, _, err = s.sweepLocked(ctx, root)
	return err
}

// Native consumes the request and unlinks receipt temporaries without taking
// the Go spool lock. A vanished entry contributes no bytes; it is not corruption.
func spoolEntrySize(entry os.DirEntry) (int64, error) {
	info, err := entry.Info()
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() || info.Size() > nativeprotocol.MaxEnvelopeBytes {
		return 0, errors.New("unsafe spool entry")
	}
	return info.Size(), nil
}
