package analyzer

import (
	"strings"
	"testing"
)

func TestClassifyLastMessage(t *testing.T) {
	cases := []struct {
		name string
		text string
		want Status
	}{
		{name: "plain completion", text: "Done, all tests pass.", want: StatusTaskComplete},
		{name: "empty", text: "", want: StatusTaskComplete},
		{name: "whitespace only", text: "  \n\t ", want: StatusTaskComplete},
		{name: "question", text: "Should I also update the README?", want: StatusQuestion},
		{name: "question with trailing space", text: "Which option do you prefer? ", want: StatusQuestion},
		{name: "question mark inside", text: "Fixed the what? handling in the parser", want: StatusTaskComplete},
		{name: "multibyte", text: "Готово ✅", want: StatusTaskComplete},
		{name: "multibyte question", text: "Продолжать?", want: StatusQuestion},
		{name: "fullwidth question", text: "続行しますか？", want: StatusQuestion},
		{name: "arabic question", text: "هل أكمل؟", want: StatusQuestion},
		{name: "semicolon is not a question", text: "Added the missing semicolon;", want: StatusTaskComplete},
		{name: "rate limit error", text: "Rate limit reached, please retry later.", want: StatusAPIErrorOverloaded},
		{name: "quota error", text: "Quota exceeded for this billing period.", want: StatusAPIErrorOverloaded},
		{name: "auth error", text: "Authentication failed: invalid API key.", want: StatusAPIError},
		{name: "session limit", text: "Session limit reached.", want: StatusSessionLimitReached},
		{name: "error takes precedence over question", text: "Rate limit reached — retry?", want: StatusAPIErrorOverloaded},
		{name: "long summary mentioning rate limit stays complete", text: strings.Repeat("Implemented the retry middleware. ", 12) + "It now backs off when the upstream reports rate limit responses.", want: StatusTaskComplete},
		{name: "plain word error is not enough", text: "Fixed the error in the parser.", want: StatusTaskComplete},
		{name: "short summary about rate limit stays complete", text: "Fixed the rate limit bug.", want: StatusTaskComplete},
		{name: "short summary about session limit stays complete", text: "Raised the session limit to 50.", want: StatusTaskComplete},
		// >300 bytes but <300 runes: byte counting would wrongly skip the
		// error heuristic for non-ASCII scripts.
		{name: "cyrillic error within rune budget", text: strings.Repeat("Запрос отклонён провайдером. ", 8) + "Rate limit reached, please retry.", want: StatusAPIErrorOverloaded},
	}
	for _, tc := range cases {
		if got := ClassifyLastMessage(tc.text); got != tc.want {
			t.Errorf("%s: ClassifyLastMessage(%q) = %q, want %q", tc.name, tc.text, got, tc.want)
		}
	}
}
