package notifier

import (
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
)

// Regression: literal title delimiters must not be reinterpreted as legacy
// branch metadata, and hiding the session label must also hide the native name.
func TestHookPresentationLiteralNativeTitleAndQuestion(t *testing.T) {
	content := HookPresentation{
		SessionName: "Fix [SDK] | installer", Branch: "feat/very-long-branch",
		Folder: "agent-notifications", Body: "Restart Codex?", Question: "Restart Codex?",
	}
	question := hookPresentation(analyzer.StatusQuestion, content, "❓ Question", true)
	if question.Title != "❓ Question: Restart Codex?" || question.Subtitle != "Fix [SDK] | installer · agent-notifications" || question.Body != "Restart Codex?" {
		t.Fatalf("question presentation = %+v", question)
	}
	complete := hookPresentation(analyzer.StatusTaskComplete, content, "✅ Completed", true)
	if complete.Title != "✅ Completed [Fix [SDK] | installer]" || complete.Subtitle != "feat/very-long-branch · agent-notifications" {
		t.Fatalf("completion presentation = %+v", complete)
	}
	for _, status := range []analyzer.Status{analyzer.StatusQuestion, analyzer.StatusTaskComplete} {
		hidden := hookPresentation(status, content, "Status", false)
		if strings.Contains(hidden.Title+hidden.Subtitle+hidden.Body, content.SessionName) {
			t.Fatalf("hidden session identity leaked: %+v", hidden)
		}
	}
}

// Regression: shortening a Unicode question must not split UTF-8 bytes or
// shorten the body together with the headline.
func TestHookPresentationShortensOnlyHeadline(t *testing.T) {
	question := strings.Repeat("Перезапустить 🧠 ", 20)
	content := HookPresentation{Question: question, Body: question}
	got := hookPresentation(analyzer.StatusQuestion, content, "Question", true)
	if !utf8.ValidString(got.Title) || !strings.HasSuffix(got.Title, "…") || utf8.RuneCountInString(got.Title) > 90 {
		t.Fatalf("invalid headline: %q", got.Title)
	}
	if got.Body != question {
		t.Fatalf("headline shortening changed body: %q", got.Body)
	}
}

// Regression: subtitleless delivery must retain native/generated question
// identity, hide it when disabled, and never parse the stale legacy envelope.
func TestSendDesktopUsesStructuredHookPresentation(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("this test exercises the beeep delivery boundary")
	}
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.ClickToFocus = false
	cfg.Notifications.Desktop.Sound = false
	n := New(cfg)
	t.Cleanup(func() { _ = n.Close() })
	bell := false
	cfg.Notifications.Desktop.TerminalBell = &bell
	for _, tc := range []struct {
		name, label, wantBody string
		show                  bool
	}{
		{"native", "SDK | [release]", "SDK | [release] · sandbox\nUse the new installer?", true},
		{"generated fallback", "bold 00000000", "bold 00000000 · sandbox\nUse the new installer?", true},
		{"hidden", "SDK | [release]", "sandbox\nUse the new installer?", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg.Notifications.Desktop.ShowSessionLabel = &tc.show
			var title, body string
			withBeeepNotify(t, func(t, b string, _ any) error { title, body = t, b; return nil })
			err := n.SendDesktop(analyzer.StatusQuestion, "[OLD|wrong folder] stale question", "original-id", t.TempDir(), WithHookPresentation(HookPresentation{
				Question: "Use the new installer?", Body: "Use the new installer?", SessionName: tc.label, Folder: "sandbox",
			}))
			if err != nil {
				t.Fatal(err)
			}
			if title != cfg.Statuses["question"].Title+": Use the new installer?" || body != tc.wantBody {
				t.Fatalf("delivered title=%q body=%q", title, body)
			}
		})
	}
}
