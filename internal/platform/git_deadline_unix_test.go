//go:build !windows

package platform

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Test a real spawned git-shaped child that keeps stdout open while blocked.
// Output and its inherited pipes must both be bounded without any model API.
func TestGetGitBranchContextBoundsStalledProcessAndPipe(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nsleep 5\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if got := GetGitBranchContext(ctx, bin); got != "" {
		t.Fatalf("stalled git returned branch %q", got)
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("stalled git kept hook blocked for %v", elapsed)
	}
}
