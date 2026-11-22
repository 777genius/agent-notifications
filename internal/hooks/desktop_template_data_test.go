package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/config"
)

func templateTestHandler(labels bool, title string) *Handler {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.ShowSessionLabel = &labels
	cfg.Notifications.Desktop.TitleTemplate = &title
	return &Handler{cfg: cfg, product: ProductCodex}
}

func TestTemplateDataValues(t *testing.T) {
	h := templateTestHandler(true, "{sessionName}")
	templates := h.cfg.DesktopTemplatesFor("task_complete")
	data := h.templateData(templates, "bold-cat-42", "sid", "main", "proj", t.TempDir(), "Edited", "📝 1 new",
		hookNotificationDetails{title: "Native title", question: "Q?"})
	if data.SessionName != "Native title" || data.Question != "Q?" || data.SessionID != "sid" || data.Branch != "main" ||
		data.Folder != "proj" || data.Body != "Edited" || data.Actions != "📝 1 new" || data.Agent != "codex" || data.Repo != "" {
		t.Fatalf("template data = %+v", data)
	}
	// The placeholder "unknown" session name is never shown.
	data = h.templateData(templates, "unknown", "", "", ".", "", "x", "")
	if data.SessionName != "" {
		t.Fatalf("unknown session name leaked: %q", data.SessionName)
	}
	// With labels off, the native name is still available to templates.
	data = h.templateData(templates, "bold-cat-42", "sid", "", "proj", "", "x", "", hookNotificationDetails{templateName: "Native"})
	if data.SessionName != "Native" {
		t.Fatalf("templateName ignored: %q", data.SessionName)
	}
}

// The repository lookup runs only when a template uses {repo}.
func TestTemplateDataRepoOnlyWhenReferenced(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := filepath.Join(t.TempDir(), "repo-name")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", root, "init")
	// Isolate the child process without modifying the test runner's Git state.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES":
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0")
	if err := cmd.Run(); err != nil {
		t.Skipf("git init failed: %v", err)
	}
	without := templateTestHandler(true, "{branch}")
	if got := without.templateData(without.cfg.DesktopTemplatesFor("task_complete"), "s", "", "", "", root, "", "").Repo; got != "" {
		t.Fatalf("repo looked up without {repo}: %q", got)
	}
	with := templateTestHandler(true, "{repo}")
	if got := with.templateData(with.cfg.DesktopTemplatesFor("task_complete"), "s", "", "", "", root, "", "").Repo; got != "repo-name" {
		t.Fatalf("repo = %q, want repo-name", got)
	}
}

// showSessionLabel=false keeps the default layout free of the native name,
// while a template that asks for {sessionName} still receives it.
func TestNotificationDetailsTemplateName(t *testing.T) {
	ev := Event{Product: ProductClaude}
	ev.Session.Title = "Native title"
	if d := templateTestHandler(false, "{sessionName}").notificationDetails(ev, nil); d.title != "" || d.templateName != "Native title" {
		t.Fatalf("labels off + template = %+v", d)
	}
	if d := templateTestHandler(false, "{repo}").notificationDetails(ev, nil); d.title != "" || d.templateName != "" {
		t.Fatalf("labels off without {sessionName} = %+v", d)
	}
	if d := templateTestHandler(true, "{repo}").notificationDetails(ev, nil); d.title != "Native title" || d.templateName != "" {
		t.Fatalf("labels on = %+v", d)
	}
}
