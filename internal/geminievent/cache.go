package geminievent

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strconv"

	"github.com/777genius/agent-notifications/internal/geminisource"
	"github.com/777genius/agent-notifications/internal/notification/observation"
)

const ClaimBudget = observation.ClaimBudget

// RecentCache stores attempts, not successful deliveries or replayable events.
// Root must be an existing private local directory supplied by installation.
type RecentCache struct {
	Root  string
	Clock Clock
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

// claim retains Gemini's binding validation, marker and channel mapping.
// The shared cache releases its lock before returning to the consumer.
func (c *RecentCache) claim(ctx context.Context, binding Binding, facts geminisource.Facts, channel Channel) (bool, error) {
	if c == nil || c.Clock == nil || binding.InstallationID == "" || binding.Generation == 0 || facts.SessionID == "" || facts.Event == "" || facts.Timestamp == "" {
		return false, errors.New("cache_unavailable")
	}
	bit := uint8(1)
	if channel == WebhookChannel {
		bit = 2
	} else if channel != DesktopChannel {
		return false, errors.New("cache_unavailable")
	}
	cache := observation.RecentCache{Root: c.Root, Clock: c.Clock}
	return cache.Claim(ctx, marker(binding, facts), bit)
}
