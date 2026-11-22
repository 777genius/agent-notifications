package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGetGitBranch(t *testing.T) {
	tests := []struct {
		name     string
		cwd      string
		wantNone bool // if true, expect empty string
	}{
		{
			name:     "Empty cwd",
			cwd:      "",
			wantNone: true,
		},
		{
			name:     "Non-existent directory",
			cwd:      "/non/existent/path",
			wantNone: true,
		},
		{
			name:     "Temp directory (not a git repo)",
			cwd:      os.TempDir(),
			wantNone: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetGitBranch(tt.cwd)
			if tt.wantNone && result != "" {
				t.Errorf("GetGitBranch(%q) = %q, want empty string", tt.cwd, result)
			}
		})
	}
}

func TestGetGitBranch_RealRepo(t *testing.T) {
	// Create a temporary git repository with a known branch
	// This ensures consistent behavior regardless of CI environment (detached HEAD, etc.)
	tmpDir, err := os.MkdirTemp("", "git-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Initialize git repo
	if err := runGitCommand(tmpDir, "init"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	// Configure git user for commit (required in CI)
	_ = runGitCommand(tmpDir, "config", "user.email", "test@test.com")
	_ = runGitCommand(tmpDir, "config", "user.name", "Test")

	// Create initial commit (required to have a branch)
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	if err := runGitCommand(tmpDir, "add", "."); err != nil {
		t.Fatalf("git add failed: %v", err)
	}
	if err := runGitCommand(tmpDir, "commit", "-m", "initial"); err != nil {
		t.Fatalf("git commit failed: %v", err)
	}

	// Rename branch to known name (handles both old 'master' and new 'main' defaults)
	_ = runGitCommand(tmpDir, "branch", "-M", "test-branch")

	branch := GetGitBranch(tmpDir)
	if branch != "test-branch" {
		t.Errorf("GetGitBranch() = %q, want %q", branch, "test-branch")
	}
}

func TestGetGitBranch_DetachedHead(t *testing.T) {
	// Test that detached HEAD returns empty string (as documented)
	tmpDir, err := os.MkdirTemp("", "git-test-detached-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Initialize and create commits
	if err := runGitCommand(tmpDir, "init"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	_ = runGitCommand(tmpDir, "config", "user.email", "test@test.com")
	_ = runGitCommand(tmpDir, "config", "user.name", "Test")

	// Create two commits so we can checkout a specific one
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("v1"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	_ = runGitCommand(tmpDir, "add", ".")
	_ = runGitCommand(tmpDir, "commit", "-m", "first")

	if err := os.WriteFile(testFile, []byte("v2"), 0644); err != nil {
		t.Fatalf("Failed to update test file: %v", err)
	}
	_ = runGitCommand(tmpDir, "add", ".")
	_ = runGitCommand(tmpDir, "commit", "-m", "second")

	// Checkout first commit (detached HEAD)
	if err := runGitCommand(tmpDir, "checkout", "HEAD~1"); err != nil {
		t.Fatalf("git checkout failed: %v", err)
	}

	branch := GetGitBranch(tmpDir)
	if branch != "" {
		t.Errorf("GetGitBranch() in detached HEAD = %q, want empty string", branch)
	}
}

func TestGetGitMetadata_RealRepo(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "git-test-metadata-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := runGitCommand(tmpDir, "init"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	_ = runGitCommand(tmpDir, "config", "user.email", "test@test.com")
	_ = runGitCommand(tmpDir, "config", "user.name", "Test User")

	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	if err := runGitCommand(tmpDir, "add", "."); err != nil {
		t.Fatalf("git add failed: %v", err)
	}
	if err := runGitCommand(tmpDir, "commit", "-m", "initial"); err != nil {
		t.Fatalf("git commit failed: %v", err)
	}
	_ = runGitCommand(tmpDir, "branch", "-M", "metadata-branch")

	metadata := GetGitMetadata(tmpDir)
	if metadata.Branch != "metadata-branch" {
		t.Errorf("Branch = %q, want %q", metadata.Branch, "metadata-branch")
	}
	if metadata.UserEmail != "test@test.com" {
		t.Errorf("UserEmail = %q, want %q", metadata.UserEmail, "test@test.com")
	}
	if metadata.UserName != "Test User" {
		t.Errorf("UserName = %q, want %q", metadata.UserName, "Test User")
	}
	if metadata.CommitHash == "" {
		t.Error("CommitHash should not be empty")
	}
	if metadata.CommitShortHash == "" {
		t.Error("CommitShortHash should not be empty")
	}
	if metadata.CommitAuthorName != "Test User" {
		t.Errorf("CommitAuthorName = %q, want %q", metadata.CommitAuthorName, "Test User")
	}
	if metadata.CommitAuthorEmail != "test@test.com" {
		t.Errorf("CommitAuthorEmail = %q, want %q", metadata.CommitAuthorEmail, "test@test.com")
	}
}

// runGitCommand executes a git command in the specified directory
func runGitCommand(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(gitDiscoveryEnv(), "GIT_TERMINAL_PROMPT=0")
	return cmd.Run()
}
