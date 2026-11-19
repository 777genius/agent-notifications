package notifier

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
)

func ptr(s string) *string { return &s }

func goldenNotifier(labels bool, mutate func(*config.Config)) *Notifier {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.ShowSessionLabel = &labels
	if mutate != nil {
		mutate(cfg)
	}
	return New(cfg)
}

// Without templates for a status, every backend must receive exactly what it
// received before templates existed. The golden file was generated from the
// pre-template code; template data alone, or a template configured for an
// unrelated status, must not change the output.
func TestDesktopPresentationDefaultsGolden(t *testing.T) {
	want, err := os.ReadFile("testdata/presentation-defaults.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	variants := []struct {
		name   string
		mutate func(*config.Config)
		data   bool
	}{
		{"no templates", nil, false},
		{"template data without templates", nil, true},
		{"template on another status", func(c *config.Config) {
			info := c.Statuses["agent_error"]
			info.TitleTemplate = ptr("{repo}")
			info.HeaderTemplate = ptr("{branch}")
			info.BodyTemplate = ptr("{header}\n{body}")
			c.Statuses["agent_error"] = info
		}, true},
	}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			var out []presentationGoldenResult
			for _, c := range presentationGoldenCases() {
				n := goldenNotifier(c.Labels, v.mutate)
				var opts []SendOption
				if c.Hook != nil {
					opts = append(opts, WithHookPresentation(*c.Hook))
				}
				if v.data {
					opts = append(opts, WithTemplateData(TemplateData{SessionID: "sid", Repo: "repo", Branch: "b", Agent: "claude", Body: "x", Actions: "y"}))
				}
				p, structured := n.desktopPresentation(c.Status, c.Message, c.StatusTitle, resolveSendOptions(opts))
				out = append(out, presentationGoldenResult{Name: c.Name, Status: c.Status, StatusTitle: c.StatusTitle, Labels: c.Labels,
					Title: p.Title, Subtitle: p.Subtitle, Body: p.Body,
					SubtitlelessBody: subtitlelessBody(p.Subtitle, p.Body, structured), TimeSensitive: p.TimeSensitive})
			}
			got, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if string(append(got, '\n')) != string(want) {
				t.Fatal("default desktop presentation differs from the pre-template golden")
			}
		})
	}
}

func templatedPresentation(t *testing.T, status analyzer.Status, mutate func(*config.Config), opts ...SendOption) (legacyDesktopPresentation, string) {
	t.Helper()
	n := goldenNotifier(true, mutate)
	info, _ := n.cfg.GetStatusInfo(string(status))
	p, structured := n.desktopPresentation(status, "[bold-cat|main repo] legacy body", info.Title, resolveSendOptions(opts))
	return p, subtitlelessBody(p.Subtitle, p.Body, structured)
}

var fullData = TemplateData{
	SessionName: "Refactor", SessionID: "abc-123", Folder: "sub", Repo: "agent-notifications",
	Branch: "feat/x", Body: "Edited 3 files", Actions: "📝 1 new", Agent: "claude",
}

func TestDesktopTemplatesIssueExample(t *testing.T) {
	issue := func(c *config.Config) {
		c.Notifications.Desktop.TitleTemplate = ptr("{statusTitle} {repo}")
		c.Notifications.Desktop.HeaderTemplate = ptr("{branch} · {sessionName}")
		c.Notifications.Desktop.BodyTemplate = ptr("{header}\n{body}")
		q := c.Statuses["question"]
		q.TitleTemplate = ptr("{statusTitle} {repo}: {question}")
		c.Statuses["question"] = q
	}
	p, linux := templatedPresentation(t, analyzer.StatusTaskComplete, issue, WithTemplateData(fullData))
	if p.Title != "✅ Completed agent-notifications" || p.Subtitle != "" || p.Body != "feat/x · Refactor\nEdited 3 files" || linux != p.Body {
		t.Fatalf("completion = %+v, subtitleless body %q", p.Content, linux)
	}
	question := fullData
	question.Question = "Restart Codex?"
	p, _ = templatedPresentation(t, analyzer.StatusQuestion, issue, WithTemplateData(question))
	if p.Title != "❓ Question agent-notifications: Restart Codex?" {
		t.Fatalf("question title = %q", p.Title)
	}
	// Outside a git repository the empty repo collapses with its separator.
	question.Repo, question.Branch = "", ""
	p, _ = templatedPresentation(t, analyzer.StatusQuestion, issue, WithTemplateData(question))
	if p.Title != "❓ Question: Restart Codex?" || p.Body != "Refactor\nEdited 3 files" {
		t.Fatalf("collapsed = %+v", p.Content)
	}
}

func TestDesktopTemplatesKeepUnconfiguredComponents(t *testing.T) {
	titleOnly := func(c *config.Config) { c.Notifications.Desktop.TitleTemplate = ptr("{statusTitle}") }
	p, linux := templatedPresentation(t, analyzer.StatusTaskComplete, titleOnly, WithTemplateData(fullData))
	if p.Title != "✅ Completed" || p.Subtitle != "main · repo" || p.Body != "legacy body" {
		t.Fatalf("title-only = %+v", p.Content)
	}
	// Legacy path without an explicit header keeps its historical layout.
	if linux != "legacy body" {
		t.Fatalf("title-only subtitleless body = %q", linux)
	}
	header := func(c *config.Config) { c.Notifications.Desktop.HeaderTemplate = ptr("{repo} · {branch}") }
	p, linux = templatedPresentation(t, analyzer.StatusTaskComplete, header, WithTemplateData(fullData))
	if p.Subtitle != "agent-notifications · feat/x" || linux != "agent-notifications · feat/x\nlegacy body" {
		t.Fatalf("explicit header = %+v, %q", p.Content, linux)
	}
	// An empty header template removes the context line.
	empty := func(c *config.Config) { c.Notifications.Desktop.HeaderTemplate = ptr("") }
	p, linux = templatedPresentation(t, analyzer.StatusTaskComplete, empty, WithTemplateData(fullData))
	if p.Subtitle != "" || linux != "legacy body" {
		t.Fatalf("empty header = %+v, %q", p.Content, linux)
	}
}

func TestDesktopTemplatesPerStatusOverride(t *testing.T) {
	mutate := func(c *config.Config) {
		c.Notifications.Desktop.TitleTemplate = ptr("{repo}")
		info := c.Statuses["plan_ready"]
		info.TitleTemplate = ptr("{statusTitle} [{agent}]")
		c.Statuses["plan_ready"] = info
	}
	p, _ := templatedPresentation(t, analyzer.StatusPlanReady, mutate, WithTemplateData(fullData))
	if p.Title != "📋 Plan [claude]" {
		t.Fatalf("override title = %q", p.Title)
	}
	p, _ = templatedPresentation(t, analyzer.StatusTaskComplete, mutate, WithTemplateData(fullData))
	if p.Title != "agent-notifications" {
		t.Fatalf("global title = %q", p.Title)
	}
}

func TestDesktopTemplatesAllPlaceholders(t *testing.T) {
	mutate := func(c *config.Config) {
		c.Notifications.Desktop.BodyTemplate = ptr("{statusTitle}|{status}|{sessionName}|{sessionId}|{folder}|{repo}|{branch}|{question}|{body}|{actions}|{agent}")
	}
	data := fullData
	data.Question = "Q?"
	p, _ := templatedPresentation(t, analyzer.StatusQuestion, mutate, WithTemplateData(data))
	want := "❓ Question|question|Refactor|abc-123|sub|agent-notifications|feat/x|Q?|Edited 3 files|📝 1 new|claude"
	if p.Body != want {
		t.Fatalf("body = %q, want %q", p.Body, want)
	}
}

func TestDesktopTemplatesLengthLimitsAndEmptyTitle(t *testing.T) {
	long := fullData
	long.Repo = strings.Repeat("репо🧠", 40)
	mutate := func(c *config.Config) {
		c.Notifications.Desktop.TitleTemplate = ptr("{statusTitle} {repo}")
		c.Notifications.Desktop.HeaderTemplate = ptr("{repo}\n{branch}")
	}
	p, _ := templatedPresentation(t, analyzer.StatusTaskComplete, mutate, WithTemplateData(long))
	for _, line := range []string{p.Title, p.Subtitle} {
		if !utf8.ValidString(line) || utf8.RuneCountInString(line) > 100 || !strings.HasSuffix(line, "…") || strings.Contains(line, "\n") {
			t.Fatalf("line not limited: %q", line)
		}
	}
	repoOnly := func(c *config.Config) { c.Notifications.Desktop.TitleTemplate = ptr("{repo}") }
	noRepo := fullData
	noRepo.Repo = ""
	p, _ = templatedPresentation(t, analyzer.StatusTaskComplete, repoOnly, WithTemplateData(noRepo))
	if p.Title != "✅ [bold-cat]" {
		t.Fatalf("empty rendered title should keep the default, got %q", p.Title)
	}
}

// Callers without explicit template data (Pi, direct sends) still get the
// hook presentation values; "." is never shown as a folder.
func TestDesktopTemplatesFallBackToHookPresentation(t *testing.T) {
	mutate := func(c *config.Config) {
		c.Notifications.Desktop.TitleTemplate = ptr("{sessionName} · {folder} · {branch}")
	}
	p, _ := templatedPresentation(t, analyzer.StatusTaskComplete, mutate,
		WithHookPresentation(HookPresentation{SessionName: "Pi", Folder: ".", Body: "Waiting"}))
	if p.Title != "Pi" || p.Body != "Waiting" {
		t.Fatalf("fallback = %+v", p.Content)
	}
}
