package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func privateBootstrapProgressFixture(t *testing.T, stage, dir, path string) {
	t.Helper()
	for _, owned := range []string{stage, dir, path} {
		if err := installruntime.RestrictPrivatePath(owned); err != nil {
			t.Fatal(err)
		}
	}
}

// The validator opens an intentionally write-exclusive reader. A persistent
// append handle used to make every event fail revalidation with a sharing
// violation, even though journal admission succeeded.
func TestBootstrapProgressReleasesWriterBeforePrivateRevalidation(t *testing.T) {
	intent, path := bootstrapProgressFixture(t)
	write, closeProgress, err := openBootstrapProgress(intent, path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeProgress()
	for _, client := range []string{"claude", "codex"} {
		if _, err := installruntime.ReadPrivateCacheDocument(filepath.Dir(path), "progress.events", maxBootstrapIntent); err != nil {
			t.Fatalf("idle progress observer retains a conflicting writer: %v", err)
		}
		write(client, "verification", "running")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "transition\tclaude\tverification\trunning\ntransition\tcodex\tverification\trunning\n"
	if string(got) != want {
		t.Fatalf("finite appends lost: %q", got)
	}
}
