package geminievent

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/777genius/agent-notifications/internal/geminisource"
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

func marker(binding Binding, facts geminisource.Facts) string {
	h := sha256.New()
	for _, field := range []string{binding.InstallationID, strconv.FormatUint(binding.Generation, 10), "gemini", facts.SessionID, facts.Event, facts.Subtype, strconv.FormatBool(facts.StopHookActive), facts.Timestamp} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(field))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// claim atomically marks one channel attempted. Its lock is released before
// returning; no delivery or policy lease is entered under this lock.
func (c *RecentCache) claim(ctx context.Context, binding Binding, facts geminisource.Facts, channel Channel) (bool, error) {
	if c == nil || c.Clock == nil || binding.InstallationID == "" || binding.Generation == 0 || facts.SessionID == "" || facts.Event == "" || facts.Timestamp == "" {
		return false, errors.New("cache_unavailable")
	}
	root, err := installruntime.PhysicalPath(c.Root)
	if err != nil || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return false, errors.New("cache_unavailable")
	}
	if err = checkCacheRoot(root); err != nil {
		return false, errors.New("cache_unavailable")
	}
	lockCtx, cancel := context.WithTimeout(ctx, ClaimBudget)
	defer cancel()
	unlock, err := installruntime.Lock(lockCtx, filepath.Join(root, ".observations.lock"))
	if err != nil {
		return false, errors.New("cache_unavailable")
	}
	defer unlock()
	boot, now, err := c.Clock.Now()
	if !validTime(boot, now, err) || lockCtx.Err() != nil {
		return false, errors.New("cache_unavailable")
	}
	data, err := readCache(root)
	if err != nil && !os.IsNotExist(err) {
		return false, errors.New("cache_unavailable")
	}
	bootHash := sha256.Sum256([]byte(boot))
	bootKey := hex.EncodeToString(bootHash[:])
	state := cacheState{Boot: bootKey}
	if err == nil {
		if json.Unmarshal(data, &state) != nil || len(state.Entries) > cacheLimit {
			return false, errors.New("cache_unavailable")
		}
		storedBoot, bootErr := hex.DecodeString(state.Boot)
		if bootErr != nil || len(storedBoot) != sha256.Size {
			return false, errors.New("cache_unavailable")
		}
		seen := make(map[string]bool, len(state.Entries))
		for _, entry := range state.Entries {
			key, err := hex.DecodeString(entry.Key)
			if err != nil || len(key) != sha256.Size || seen[entry.Key] || entry.Bits == 0 || entry.Bits > 3 || !validTime(state.Boot, entry.Until, nil) {
				return false, errors.New("cache_unavailable")
			}
			seen[entry.Key] = true
		}
	}
	kept := state.Entries[:0]
	for _, entry := range state.Entries {
		// New boot and clock rollback invalidate the bounded window, not events.
		if state.Boot == bootKey && entry.Until > now && entry.Until <= now+cacheWindow {
			kept = append(kept, entry)
		}
	}
	state.Boot, state.Entries = bootKey, kept
	bit := uint8(1)
	if channel == WebhookChannel {
		bit = 2
	} else if channel != DesktopChannel {
		return false, errors.New("cache_unavailable")
	}
	key := marker(binding, facts)
	found := false
	for i := range state.Entries {
		if state.Entries[i].Key != key {
			continue
		}
		if state.Entries[i].Bits&bit != 0 {
			return false, nil
		}
		state.Entries[i].Bits |= bit
		found = true
		break
	}
	if !found {
		if len(state.Entries) == cacheLimit {
			oldest := 0
			for i := range state.Entries {
				if state.Entries[i].Until < state.Entries[oldest].Until {
					oldest = i
				}
			}
			state.Entries = append(state.Entries[:oldest], state.Entries[oldest+1:]...)
		}
		state.Entries = append(state.Entries, cacheEntry{key, now + cacheWindow, bit})
	}
	data, err = json.Marshal(state)
	if err != nil || len(data) > cacheBytes || lockCtx.Err() != nil {
		return false, errors.New("cache_unavailable")
	}
	if err = writeCache(root, data); err != nil {
		return false, errors.New("cache_unavailable")
	}
	if lockCtx.Err() != nil {
		return false, errors.New("cache_unavailable")
	}
	return true, nil
}
