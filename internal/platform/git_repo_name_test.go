package platform

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

	// A working tree whose git dir is stored elsewhere reports the working
	// tree's name, not the git dir's.
	separate := filepath.Join(t.TempDir(), "separate-project")
	meta := filepath.Join(t.TempDir(), "project-meta")
	if err := os.MkdirAll(filepath.Join(separate, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGitCommand(separate, "init", "--separate-git-dir", meta); err != nil {
		t.Skipf("git init --separate-git-dir failed: %v", err)
	}
	for _, dir := range []string{separate, filepath.Join(separate, "src")} {
		if got := GetGitRepoNameContext(ctx, dir); got != "separate-project" {
			t.Fatalf("separate git dir from %s = %q, want separate-project", dir, got)
		}
	}
}

func TestGitMetadataContextIgnoresInheritedRepoSelection(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	wrong := filepath.Join(t.TempDir(), "wrong-repo")
	wanted := filepath.Join(t.TempDir(), "wanted-repo")
	for _, root := range []string{wrong, wanted} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := runGitCommand(root, "init"); err != nil {
			t.Skipf("git init failed: %v", err)
		}
	}
	if err := runGitCommand(wanted, "-c", "user.email=test@test.com", "-c", "user.name=Test", "commit", "--allow-empty", "-m", "initial"); err != nil {
		t.Skipf("git fixture commit failed: %v", err)
	}
	if err := runGitCommand(wanted, "branch", "-m", "wanted-branch"); err != nil {
		t.Skipf("git branch rename failed: %v", err)
	}
	t.Setenv("GIT_DIR", filepath.Join(wrong, ".git"))
	t.Setenv("GIT_WORK_TREE", wrong)
	t.Setenv("GIT_COMMON_DIR", filepath.Join(wrong, ".git"))
	ctx := context.Background()
	if got := GetGitRepoNameContext(ctx, wanted); got != "wanted-repo" {
		t.Fatalf("repo name = %q, want wanted-repo", got)
	}
	if got := GetGitBranchContext(ctx, wanted); got != "wanted-branch" {
		t.Fatalf("branch = %q, want wanted-branch", got)
	}
	if got := GetGitBranch(wanted); got != "wanted-branch" {
		t.Fatalf("legacy branch = %q, want wanted-branch", got)
	}
	if got := GetGitMetadata(wanted); got.Branch != "wanted-branch" || got.CommitHash == "" {
		t.Fatalf("legacy metadata from wrong repo: %+v", got)
	}
}

// Windows treats environment variable names case-insensitively. A mixed-case
// GIT_DIR must not override the repository selected by the caller's cwd.
func TestGitDiscoveryEnvIgnoresMixedCaseOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows environment-key matching")
	}
	for _, key := range []string{"Git_Dir", "Git_Work_Tree", "Git_Common_Dir", "Git_Index_File", "Git_Object_Directory", "Git_Alternate_Object_Directories"} {
		t.Setenv(key, filepath.Join(t.TempDir(), "foreign-git"))
	}
	for _, entry := range gitDiscoveryEnv() {
		key, _, _ := strings.Cut(entry, "=")
		for _, forbidden := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES"} {
			if strings.EqualFold(key, forbidden) {
				t.Fatalf("repository override %q retained in git discovery environment", key)
			}
		}
	}
}
