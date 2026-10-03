package observation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

const cacheLimit = 256
const cacheBytes = 48 * 1024
const cacheWindow = 60.0
const ClaimBudget = 250 * time.Millisecond

// RecentCache stores attempts, not successful deliveries or replayable events.
// Root must be an existing private local directory supplied by installation.
type RecentCache struct {
	Root  string
	Clock Clock
}
type cacheEntry struct {
	Key   string  `json:"key"`
	Until float64 `json:"until"`
	Bits  uint8   `json:"bits"`
}
type cacheState struct {
	Boot    string       `json:"boot"`
	Entries []cacheEntry `json:"entries"`
}

// Claim atomically marks one channel attempted. Its lock is released before
// returning; no delivery or policy lease is entered under this lock.
// Key must already be a SHA-256 privacy hash encoded as hex. Bit must be 1
// (desktop) or 2 (webhook); adding a bit never extends the fixed 60-second window.
func (c *RecentCache) Claim(ctx context.Context, key string, bit uint8) (bool, error) {
	diag := startClaimDiagnostics(ctx)
	defer diag.end()
	defer diag.emitFailure()
	if c == nil || c.Clock == nil || len(key) != sha256.Size*2 || (bit != 1 && bit != 2) {
		return false, diag.fail(nil, "invalid_request")
	}
	if _, err := hex.DecodeString(key); err != nil {
		return false, diag.fail(nil, "invalid_request")
	}
	diag.enter("path")
	root, err := installruntime.PhysicalPath(c.Root)
	if err != nil || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return false, diag.fail(err, "invalid_path")
	}
	diag.enter("root")
	if err = checkCacheRoot(root); err != nil {
		return false, diag.fail(err, "validation")
	}
	lockCtx, cancel := context.WithTimeout(ctx, ClaimBudget)
	defer cancel()
	diag.budget, diag.budgetStarted = lockCtx, time.Now()
	diag.enter("lock")
	unlock, err := installruntime.Lock(lockCtx, filepath.Join(root, ".observations.lock"))
	if err != nil {
		return false, diag.fail(err, "validation")
	}
	defer unlock()
	diag.enter("clock")
	boot, now, err := c.Clock.Now()
	if !validTime(boot, now, err) {
		return false, diag.fail(err, "invalid_clock")
	}
	if err = lockCtx.Err(); err != nil {
		return false, diag.fail(err, "none")
	}
	diag.enter("read")
	data, err := readCache(root)
	if err != nil && !os.IsNotExist(err) {
		return false, diag.fail(err, "validation")
	}
	bootHash := sha256.Sum256([]byte(boot))
	bootKey := hex.EncodeToString(bootHash[:])
	state := cacheState{Boot: bootKey}
	if err == nil {
		diag.enter("decode")
		state, err = decodeCache(data, bootKey)
		if err != nil {
			return false, diag.fail(err, "invalid_document")
		}
	}
	diag.enter("record")
	if !state.recordAttempt(bootKey, key, bit, now) {
		return false, nil
	}
	diag.enter("encode")
	data, err = json.Marshal(state)
	if err != nil || len(data) > cacheBytes {
		return false, diag.fail(err, "invalid_document")
	}
	if err = lockCtx.Err(); err != nil {
		return false, diag.fail(err, "none")
	}
	diag.enter("publish")
	if err = writeCache(root, data); err != nil {
		return false, diag.fail(err, "validation")
	}
	diag.enter("published")
	if lockCtx.Err() != nil {
		return false, diag.fail(lockCtx.Err(), "none")
	}
	return true, nil
}

func decodeCache(data []byte, bootKey string) (cacheState, error) {
	state := cacheState{Boot: bootKey}
	if json.Unmarshal(data, &state) != nil || len(state.Entries) > cacheLimit {
		return cacheState{}, errors.New("cache_unavailable")
	}
	storedBoot, bootErr := hex.DecodeString(state.Boot)
	if bootErr != nil || len(storedBoot) != sha256.Size {
		return cacheState{}, errors.New("cache_unavailable")
	}
	seen := make(map[string]bool, len(state.Entries))
	for _, entry := range state.Entries {
		key, err := hex.DecodeString(entry.Key)
		if err != nil || len(key) != sha256.Size || seen[entry.Key] || entry.Bits == 0 || entry.Bits > 3 || !validTime(state.Boot, entry.Until, nil) {
			return cacheState{}, errors.New("cache_unavailable")
		}
		seen[entry.Key] = true
	}
	return state, nil
}

// recordAttempt drops entries outside the same-boot window, then changes only
// attempted bits for a retained key. A new key receives a fixed window after
// evicting the oldest entry when at capacity.
func (s *cacheState) recordAttempt(bootKey, key string, bit uint8, now float64) bool {
	kept := s.Entries[:0]
	for _, entry := range s.Entries {
		// New boot and clock rollback invalidate the bounded window, not events.
		if s.Boot == bootKey && entry.Until > now && entry.Until <= now+cacheWindow {
			kept = append(kept, entry)
		}
	}
	s.Boot, s.Entries = bootKey, kept

	found := false
	for i := range s.Entries {
		if s.Entries[i].Key != key {
			continue
		}
		if s.Entries[i].Bits&bit != 0 {
			return false
		}
		s.Entries[i].Bits |= bit
		found = true
		break
	}
	if !found {
		if len(s.Entries) == cacheLimit {
			oldest := 0
			for i := range s.Entries {
				if s.Entries[i].Until < s.Entries[oldest].Until {
					oldest = i
				}
			}
			s.Entries = append(s.Entries[:oldest], s.Entries[oldest+1:]...)
		}
		s.Entries = append(s.Entries, cacheEntry{key, now + cacheWindow, bit})
	}
	return true
}
