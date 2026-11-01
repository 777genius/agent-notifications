//go:build windows

package notifier

import (
	"context"
	"testing"

	"github.com/777genius/agent-notifications/internal/winfocus"
)

func TestContextWindowsLocalityRequiresNativeTerminalEvidence(t *testing.T) {
	previous := captureLocalWindowsTerminal
	t.Cleanup(func() { captureLocalWindowsTerminal = previous })
	for _, test := range []struct {
		name, term, wt string
		focus          winfocus.FocusContext
		want           bool
	}{
		{name: "missing"},
		{name: "unknown", term: "TEST-unknown-terminal"},
		{name: "windows_terminal", wt: "native-session", want: true},
		{name: "native_ancestor_window", focus: winfocus.FocusContext{HWND: 123, PID: 456}, want: true},
		{name: "folder_is_not_a_window", focus: winfocus.FocusContext{Folder: "project"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
				t.Setenv(key, "")
			}
			t.Setenv("TERM_PROGRAM", test.term)
			t.Setenv("WT_SESSION", test.wt)
			captureLocalWindowsTerminal = func(context.Context, string) (winfocus.FocusContext, bool) { return test.focus, test.focus.HasTarget() }
			if got := HasLocalTerminalContext(context.Background()); got != test.want {
				t.Fatalf("local=%t, want %t", got, test.want)
			}
		})
	}
}
