package platform

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRepoNameFromCommonDir(t *testing.T) {
	for _, tc := range []struct{ dir, want string }{
		{filepath.FromSlash("/src/agent-notifications/.git"), "agent-notifications"},
		{filepath.FromSlash("/src/agent-notifications/.git/"), "agent-notifications"},
		{filepath.FromSlash("/srv/git/agent-notifications.git"), "agent-notifications"},
		{filepath.FromSlash("/src/parent/.git/modules/sub"), "sub"},
	} {
		if got := repoNameFromCommonDir(tc.dir); got != tc.want {
			t.Errorf("repoNameFromCommonDir(%q) = %q, want %q", tc.dir, got, tc.want)
		}
	}
}

func TestGetGitRepoNameContext(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	if got := GetGitRepoNameContext(ctx, t.TempDir()); got != "" {
		t.Fatalf("non-repo = %q", got)
	}
	if got := GetGitRepoNameContext(ctx, ""); got != "" {
		t.Fatalf("empty cwd = %q", got)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	root := filepath.Join(t.TempDir(), "my-repo")
	sub := filepath.Join(root, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGitCommand(root, "init"); err != nil {
		t.Skipf("git init failed: %v", err)
	}
	if got := GetGitRepoNameContext(cancelled, root); got != "" {
		t.Fatalf("cancelled context = %q", got)
	}
	// A subdirectory reports the repository, not its own folder.
	if got := GetGitRepoNameContext(ctx, sub); got != "my-repo" {
		t.Fatalf("subdirectory = %q, want my-repo", got)
	}

	// A linked worktree reports the main repository name.
	_ = runGitCommand(root, "config", "user.email", "test@test.com")
	_ = runGitCommand(root, "config", "user.name", "Test")
	if err := runGitCommand(root, "commit", "--allow-empty", "-m", "initial"); err != nil {
		t.Skipf("fixture commit failed: %v", err)
	}
	worktree := filepath.Join(t.TempDir(), "my-repo-feature")
	if err := runGitCommand(root, "worktree", "add", "-b", "feature", worktree); err != nil {
		t.Skipf("git worktree add failed: %v", err)
	}
	if got := GetGitRepoNameContext(ctx, worktree); got != "my-repo" {
		t.Fatalf("worktree = %q, want my-repo", got)
	}
}
