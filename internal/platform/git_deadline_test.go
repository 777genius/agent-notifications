package platform

import (
	"context"
	"testing"
)

func TestGetGitBranchContextRejectsCancelledLookup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := GetGitBranchContext(ctx, t.TempDir()); got != "" {
		t.Fatalf("cancelled Git branch lookup = %q", got)
	}
}
