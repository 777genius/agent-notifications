package notifier

import (
	"github.com/777genius/agent-notifications/internal/analyzer"
)

// presentationGoldenCase is one desktop presentation input. The case list is
// frozen together with testdata/presentation-defaults.golden.json, which was
// generated from the presentation code before desktop templates existed.
type presentationGoldenCase struct {
	Name        string            `json:"name"`
	Status      analyzer.Status   `json:"status"`
	StatusTitle string            `json:"statusTitle"`
	Message     string            `json:"message"`
	Hook        *HookPresentation `json:"hook,omitempty"`
	Labels      bool              `json:"labels"`
}

// presentationGoldenResult is everything a backend receives: the native
// three fields and the body a subtitleless backend shows.
type presentationGoldenResult struct {
	Name             string          `json:"name"`
	Status           analyzer.Status `json:"status"`
	StatusTitle      string          `json:"statusTitle"`
	Labels           bool            `json:"labels"`
	Title            string          `json:"title"`
	Subtitle         string          `json:"subtitle"`
	Body             string          `json:"body"`
	SubtitlelessBody string          `json:"subtitlelessBody"`
	TimeSensitive    bool            `json:"timeSensitive"`
}

func presentationGoldenCases() []presentationGoldenCase {
	statuses := []struct {
		status analyzer.Status
		title  string
	}{
		{analyzer.StatusTaskComplete, "✅ Completed"},
		{analyzer.StatusQuestion, "❓ Question"},
		{analyzer.StatusPlanReady, "📋 Plan ready"},
		{analyzer.StatusPermissionRequest, "🔐 Permission request"},
		{analyzer.StatusAPIError, "🔴 API error"},
		{analyzer.StatusTaskComplete, "Custom done"},
		{analyzer.StatusQuestion, "Action required"},
		{analyzer.StatusQuestion, ""},
	}
	legacy := []struct{ name, message string }{
		{"legacy-branch", "[bold-cat-42|feat/x agent-notifications] Edited 3 files 📝 1 new  ▶ 2 cmds"},
		{"legacy-no-branch", "[bold-cat-42 notes] Done"},
		{"legacy-no-metadata", "Plain message"},
		{"legacy-empty", ""},
	}
	hooks := []struct {
		name string
		hook HookPresentation
	}{
		{"hook-full", HookPresentation{SessionName: "Fix [SDK] | installer", Branch: "main", Folder: "agent-notifications", Body: "Restart Codex? ⏱ 41s", Question: "Restart Codex?"}},
		{"hook-no-question", HookPresentation{SessionName: "Refactor", Branch: "feat/y", Folder: "repo", Body: "All done"}},
		{"hook-no-branch", HookPresentation{SessionName: "Pi", Folder: "proj", Body: "Waiting"}},
		{"hook-dot-folder", HookPresentation{SessionName: "Pi", Folder: ".", Body: "Waiting"}},
		{"hook-long", HookPresentation{SessionName: "Перезапустить 🧠 очень длинное имя сессии которое не помещается в заголовок уведомления никак", Question: "Перезапустить 🧠 очень длинный вопрос который точно не поместится в восемьдесят символов заголовка", Body: "body"}},
	}
	var cases []presentationGoldenCase
	for _, s := range statuses {
		for _, labels := range []bool{true, false} {
			for _, l := range legacy {
				cases = append(cases, presentationGoldenCase{Name: l.name, Status: s.status, StatusTitle: s.title, Message: l.message, Labels: labels})
			}
			for _, h := range hooks {
				hook := h.hook
				cases = append(cases, presentationGoldenCase{Name: h.name, Status: s.status, StatusTitle: s.title, Message: "[ignored|legacy envelope] ignored", Hook: &hook, Labels: labels})
			}
		}
	}
	return cases
}

func subtitlelessBody(subtitle, body string, structured bool) string {
	if structured && subtitle != "" {
		return subtitle + "\n" + body
	}
	return body
}
