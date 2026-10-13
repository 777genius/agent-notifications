//go:build linux || darwin || windows

package observation

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Red condition: a duplicate claim burns another bit or leaves its eager temp.
func TestDuplicateClaimCleansPreparation(t *testing.T) {
	c, key := cacheFixture(t)
	if got, err := c.Claim(context.Background(), key, 1); !got || err != nil {
		t.Fatalf("seed: %v/%v", got, err)
	}
	path := filepath.Join(c.Root, "observations.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := c.Claim(context.Background(), key, 1); got || err != nil {
		t.Fatalf("duplicate: %v/%v", got, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("duplicate changed history: %q/%v", after, err)
	}
	entries, err := os.ReadDir(c.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "observations.json" && entry.Name() != ".observations.lock" {
			t.Fatalf("duplicate left temp: %s", entry.Name())
		}
	}
}

// Red condition: canceled preflight creates files or enters a transaction.
func TestCanceledClaimDoesNotPrepare(t *testing.T) {
	for _, expired := range []bool{false, true} {
		name := "canceled"
		if expired {
			name = "expired"
		}
		t.Run(name, func(t *testing.T) {
			c, key := cacheFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			want := "canceled"
			cancel()
			if expired {
				ctx, cancel = context.WithDeadline(context.Background(), time.Unix(1, 0))
				want = "deadline"
			}
			defer cancel()
			got, err := c.Claim(ctx, key, 1)
			var failure *ClaimFailure
			if got || !errors.As(err, &failure) || failure.Phase != "prepare" || failure.Class != want || failure.BudgetState != "not_started" || failure.MayHavePublished {
				t.Fatalf("canceled preflight: %v/%#v", got, err)
			}
			entries, err := os.ReadDir(c.Root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("canceled preflight left files: %v/%v", entries, err)
			}
		})
	}
}
