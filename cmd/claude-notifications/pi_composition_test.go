package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/pievent"
)

func TestPiTemplateDataFillsGitFactsOnlyWhenReferenced(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := filepath.Join(t.TempDir(), "pi-repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v failed: %v: %s", args, err, out)
		}
	}
	git("init", "-b", "pi-branch")
	git("-c", "user.email=test@test.com", "-c", "user.name=Test", "commit", "--allow-empty", "-m", "initial")

	request := pievent.DesktopRequest{Status: analyzer.StatusTaskComplete, Body: "Done", SessionID: "s1", CWD: root}
	ctx := context.Background()

	cfg := config.DefaultConfig()
	data := piTemplateData(ctx, cfg, request)
	if data.Repo != "" || data.Branch != "" {
		t.Fatalf("git facts looked up without a template: %+v", data)
	}
	if data.SessionName != "Pi" || data.SessionID != "s1" || data.Folder != "pi-repo" || data.Body != "Done" || data.Agent != "pi" {
		t.Fatalf("base data = %+v", data)
	}

	header := "{repo} · {branch}"
	cfg.Notifications.Desktop.HeaderTemplate = &header
	data = piTemplateData(ctx, cfg, request)
	if data.Repo != "pi-repo" || data.Branch != "pi-branch" {
		t.Fatalf("git facts = repo %q branch %q, want pi-repo and pi-branch", data.Repo, data.Branch)
	}
}
