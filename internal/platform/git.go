package platform

import (
	"context"
	"os/exec"
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
