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
	if question.Title != "❓ Restart Codex?" || question.Subtitle != "Fix [SDK] | installer · agent-notifications" || question.Body != "Restart Codex?" {
		t.Fatalf("question presentation = %+v", question)
	}
	complete := hookPresentation(analyzer.StatusTaskComplete, content, "✅ Completed", true)
	if complete.Title != "✅ [Fix [SDK] | installer]" || complete.Subtitle != "feat/very-long-branch · agent-notifications" {
		t.Fatalf("completion presentation = %+v", complete)
	}
	for _, status := range []analyzer.Status{analyzer.StatusQuestion, analyzer.StatusTaskComplete} {
		hidden := hookPresentation(status, content, "Status", false)
		if strings.Contains(hidden.Title+hidden.Subtitle+hidden.Body, content.SessionName) {
			t.Fatalf("hidden session identity leaked: %+v", hidden)
		}
	}
}

func TestHookPresentationQuestionStatusTitles(t *testing.T) {
	for _, tc := range []struct {
		statusTitle, question, want string
	}{
		{"Question", "Restart Codex?", "Restart Codex?"},
		{"", "Restart Codex?", "Restart Codex?"},
		{"Action required", "Restart Codex?", "Action required: Restart Codex?"},
		{"❓ Question", "", "❓ Question [Installer]"},
	} {
		got := hookPresentation(analyzer.StatusQuestion, HookPresentation{
			SessionName: "Installer", Question: tc.question,
		}, tc.statusTitle, true)
		if got.Title != tc.want {
			t.Fatalf("status title %q, question %q: got %q, want %q", tc.statusTitle, tc.question, got.Title, tc.want)
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
			if title != "❓ Use the new installer?" || body != tc.wantBody {
				t.Fatalf("delivered title=%q body=%q", title, body)
			}
		})
	}
}

// Regression: default completion titles become concise only when a visible
// session label is appended; custom titles and other statuses retain their text.
func TestCompletionSessionLabelTitles(t *testing.T) {
	for _, tc := range []struct {
		name, title, label, want string
		status                   analyzer.Status
		show                     bool
	}{
		{"default", "✅ Completed", "Понятные уведомления", "✅ [Понятные уведомления]", analyzer.StatusTaskComplete, true},
		{"custom", "✅ Shipped", "Session", "✅ Shipped [Session]", analyzer.StatusTaskComplete, true},
		{"custom near default", "✅ Completed!", "Session", "✅ Completed! [Session]", analyzer.StatusTaskComplete, true},
		{"hidden", "✅ Completed", "Session", "✅ Completed", analyzer.StatusTaskComplete, false},
		{"absent", "✅ Completed", "", "✅ Completed", analyzer.StatusTaskComplete, true},
		{"other status", "✅ Completed", "Session", "✅ Completed [Session]", analyzer.StatusReviewComplete, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := "Exact body"
			if tc.label != "" {
				message = "[" + tc.label + "|main sandbox] " + message
			}
			for path, got := range map[string]legacyDesktopPresentation{
				"structured": hookPresentation(tc.status, HookPresentation{SessionName: tc.label, Branch: "main", Folder: "sandbox", Body: "Exact body"}, tc.title, tc.show),
				"legacy":     legacyPresentation(tc.status, message, tc.title, tc.show),
			} {
				if got.Title != tc.want || got.Body != "Exact body" {
					t.Fatalf("%s presentation = %+v, want title %q and unchanged body", path, got, tc.want)
				}
				wantSubtitle := "main · sandbox"
				if path == "legacy" && tc.label == "" {
					wantSubtitle = ""
				}
				if got.Subtitle != wantSubtitle {
					t.Fatalf("%s subtitle = %q, want %q", path, got.Subtitle, wantSubtitle)
				}
			}
		})
	}
}

// Session shortening belongs to structured presentation only; the concise
// completion prefix must neither change its rune limit nor shorten legacy labels.
func TestCompletionSessionLabelTruncation(t *testing.T) {
	label := strings.Repeat("界", 101)
	structured := hookPresentation(analyzer.StatusTaskComplete, HookPresentation{SessionName: label}, "✅ Completed", true)
	if structured.Title != "✅ ["+strings.Repeat("界", 99)+"…]" {
		t.Fatalf("structured completion title = %q", structured.Title)
	}
	legacy := legacyPresentation(analyzer.StatusTaskComplete, "["+label+"] body", "✅ Completed", true)
	if legacy.Title != "✅ ["+label+"]" {
		t.Fatalf("legacy completion title = %q", legacy.Title)
	}
	legacyBlank := legacyPresentation(analyzer.StatusTaskComplete, "[ \t ] body", "✅ Completed", true)
	if legacyBlank.Title != "✅ Completed [ \t ]" {
		t.Fatalf("blank legacy label changed default title: %q", legacyBlank.Title)
	}
	blank := hookPresentation(analyzer.StatusTaskComplete, HookPresentation{SessionName: " \t "}, "✅ Completed", true)
	if blank.Title != "✅ Completed []" {
		t.Fatalf("empty normalized label changed default title: %q", blank.Title)
	}
}
