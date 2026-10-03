//go:build windows

package installruntime

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Measure the actual kernel-lock path in a fresh test directory. This is a
// diagnostic benchmark, not a replacement for strict production claim tests.
func BenchmarkWindowsPrivateLock(b *testing.B) {
	for _, warm := range []bool{false, true} {
		name := "cold"
		if warm {
			name = "warm"
		}
		b.Run(name, func(b *testing.B) {
			root := b.TempDir()
			path := filepath.Join(root, "lock")
			if warm {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				release, err := Lock(ctx, path)
				if err == nil {
					release()
				}
				cancel()
				if err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if !warm {
					path = filepath.Join(root, fmt.Sprintf("lock-%d", i))
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				release, err := Lock(ctx, path)
				if err == nil {
					release()
				}
				cancel()
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
