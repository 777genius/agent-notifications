//go:build linux || darwin || windows

package observation

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Red condition: canceled publication replaces the previous attempt document,
// burning a channel attempt even though the claim cannot grant delivery.
func TestCacheCanceledPublicationPreservesDocument(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name := "canceled"
		if deadline {
			name = "expired"
		}
		t.Run(name, func(t *testing.T) {
			fixture, _ := cacheFixture(t)
			root, err := installruntime.PhysicalPath(fixture.Root)
			if err != nil {
				t.Fatal(err)
			}
			previous := []byte(`{"previous":"attempts"}`)
			if err := writeCache(root, previous); err != nil {
				t.Fatal(err)
			}
			cache, err := openCache(root)
			if err != nil {
				t.Fatal(err)
			}
			defer cache.Close()
			ctx, cancel := context.WithCancel(context.Background())
			want := context.Canceled
			if deadline {
				cancel()
				ctx, cancel = context.WithDeadline(context.Background(), time.Unix(1, 0))
				want = context.DeadlineExceeded
			} else {
				cancel()
			}
			defer cancel()
			if err := cache.Write(ctx, []byte(`{"new":"attempts"}`)); !errors.Is(err, want) {
				t.Fatalf("canceled publication: got %v, want %v", err, want)
			}
			got, err := cache.Read()
			if err != nil || !bytes.Equal(got, previous) {
				t.Fatalf("previous document changed: %q, %v", got, err)
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != "observations.json" {
				t.Fatalf("publication left temporary files: %v", entries)
			}
		})
	}
}
