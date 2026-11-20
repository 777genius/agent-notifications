package platform

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// GitMetadata contains commonly requested git values for a working tree.
type GitMetadata struct {
	Branch            string
	CommitHash        string
	CommitShortHash   string
	CommitAuthorName  string
	CommitAuthorEmail string
	UserName          string
	UserEmail         string
}

// GetGitBranch returns the current git branch name for the given directory.
// Returns empty string if not in a git repository or on error.
func GetGitBranch(cwd string) string {
	return getGitOutput(cwd, "rev-parse", "--abbrev-ref", "HEAD")
}

// GetGitBranchContext bounds a hook's optional git metadata lookup. A git
// subprocess (or a descendant holding stdout) must not consume the host hook
// deadline. Fail open with no branch label; never delay the notification.
func GetGitBranchContext(ctx context.Context, cwd string) string {
	if ctx == nil || ctx.Err() != nil || cwd == "" {
		return ""
	}
	op, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	cmd := exec.CommandContext(op, "git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD")
	cmd.WaitDelay = 200 * time.Millisecond
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(string(output))
	if value == "HEAD" {
		return ""
	}
	return value
}

// GetGitRepoNameContext returns the repository name for cwd: the basename of
// the main working tree, so linked worktrees and subdirectories report the
// repository rather than their own folder. Like GetGitBranchContext it is
// bounded by ctx and fails open with "".
func GetGitRepoNameContext(ctx context.Context, cwd string) string {
	if ctx == nil || ctx.Err() != nil || cwd == "" {
		return ""
	}
	// These rev-parse options are all older than git 2.13. git prints each
	// on its own line; --git-dir and --git-common-dir may be relative to cwd.
	out := gitOutputContext(ctx, cwd, "rev-parse", "--show-toplevel", "--git-dir", "--git-common-dir")
	lines := strings.Split(out, "\n")
	if len(lines) != 3 || lines[0] == "" {
		return ""
	}
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	top := lines[0]
	gitDir, commonDir := absUnder(cwd, lines[1]), absUnder(cwd, lines[2])
	// A linked worktree has its own git dir inside the shared common dir,
	// which belongs to the main working tree. Otherwise cwd is in the main
	// working tree, even when its git dir lives elsewhere (--separate-git-dir).
	if gitDir != commonDir {
		return repoNameFromCommonDir(commonDir)
	}
	return filepath.Base(top)
}

// absUnder resolves a path printed by git relative to cwd, following symlinks
// where possible so equal directories compare equal.
func absUnder(cwd, p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return filepath.Clean(p)
}

// repoNameFromCommonDir maps "<repo>/.git" to "<repo>" and a bare
// "<repo>.git" to "<repo>".
func repoNameFromCommonDir(dir string) string {
	dir = filepath.Clean(dir)
	base := filepath.Base(dir)
	if base == ".git" {
		return filepath.Base(filepath.Dir(dir))
	}
	return strings.TrimSuffix(base, ".git")
}

func gitOutputContext(ctx context.Context, cwd string, args ...string) string {
	op, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	cmd := exec.CommandContext(op, "git", append([]string{"-C", cwd}, args...)...)
	cmd.WaitDelay = 200 * time.Millisecond
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// GetGitMetadata returns commonly used git metadata for the given directory.
// Empty fields indicate the directory is not a git repo or the value is unavailable.
func GetGitMetadata(cwd string) GitMetadata {
	if cwd == "" {
		return GitMetadata{}
	}

	metadata := GitMetadata{
		Branch:    GetGitBranch(cwd),
		UserName:  getGitOutput(cwd, "config", "--get", "user.name"),
		UserEmail: getGitOutput(cwd, "config", "--get", "user.email"),
	}

	logOutput := getGitOutput(cwd, "log", "-1", "--pretty=format:%H%n%h%n%an%n%ae")
	if logOutput == "" {
		return metadata
	}

	parts := strings.Split(logOutput, "\n")
	if len(parts) > 0 {
		metadata.CommitHash = strings.TrimSpace(parts[0])
	}
	if len(parts) > 1 {
		metadata.CommitShortHash = strings.TrimSpace(parts[1])
	}
	if len(parts) > 2 {
		metadata.CommitAuthorName = strings.TrimSpace(parts[2])
	}
	if len(parts) > 3 {
		metadata.CommitAuthorEmail = strings.TrimSpace(parts[3])
	}

	return metadata
}

func getGitOutput(cwd string, args ...string) string {
	if cwd == "" {
		return ""
	}

	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	value := strings.TrimSpace(string(output))

	// "HEAD" is returned when in detached HEAD state
	if value == "HEAD" {
		return ""
	}

	return value
}
