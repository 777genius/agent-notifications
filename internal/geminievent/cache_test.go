package geminievent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// These test-only DTOs describe persisted bytes without granting cache authority.
type cacheEntry struct {
	Key   string  `json:"key"`
	Until float64 `json:"until"`
	Bits  uint8   `json:"bits"`
}
type cacheState struct {
	Boot    string       `json:"boot"`
	Entries []cacheEntry `json:"entries"`
}

// Fixtures prepare bytes outside the production claim budget. Windows tests
// replace only this fixture writer with the existing private publication helper.
var writeCacheFixture = func(root string, data []byte) error {
	return os.WriteFile(filepath.Join(root, "observations.json"), data, 0600)
}

// Red condition: changing an installed generation, installation or distinct
// native marker keeps an old suppression. Shared tests own eviction bounds.
func TestCacheWindowBoundAndBindingIsolation(t *testing.T) {
	c, facts, _ := consumerFixture(t)
	ctx := context.Background()
	claim := func(want bool) {
		t.Helper()
		start := time.Now()
		got, err := c.Cache.claim(ctx, c.Binding, facts, DesktopChannel)
		if err != nil || got != want {
			t.Fatalf("claim marker %q = %v/%v, want %v, after %s", facts.Timestamp, got, err, want, time.Since(start))
		}
	}
	claim(true)
	claim(false)
	c.Binding.Generation++
	claim(true)
	c.Binding.InstallationID = "other-installation"
	claim(true)
	c.Clock.(*testClock).seconds.Store(70)
	claim(true) // Window expires at sixty seconds; no replay of prior observations.
}
