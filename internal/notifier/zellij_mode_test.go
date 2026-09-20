package notifier

import (
	"testing"

	"github.com/777genius/agent-notifications/internal/daemon"
)

// Explicit values are honoured verbatim, without consulting the installed
// zellij — that is the whole point of being able to force the legacy path.
func TestResolveZellijFocusMode_ExplicitValues(t *testing.T) {
	cases := []struct {
		configured string
		want       string
	}{
		{"pane", daemon.ZellijFocusModePane},
		{"tab", daemon.ZellijFocusModeTab},
		{"off", daemon.ZellijFocusModeOff},
		{"  TAB  ", daemon.ZellijFocusModeTab},
		{"Off", daemon.ZellijFocusModeOff},
	}

	for _, testCase := range cases {
		t.Run(testCase.configured, func(t *testing.T) {
			if got := resolveZellijFocusMode(testCase.configured); got != testCase.want {
				t.Errorf("resolveZellijFocusMode(%q) = %q, want %q", testCase.configured, got, testCase.want)
			}
		})
	}
}

// Unset, "auto" and unrecognised values all resolve by capability: pane wins
// only when the session exports a pane ID and the installed zellij accepts
// focus-pane-id.
func TestResolveZellijFocusMode_AutoPicksPaneWhenTargetable(t *testing.T) {
	stubZellij(t, 2, "focus-pane-id")
	t.Setenv("ZELLIJ_SESSION_NAME", "cubic-weasel")
	t.Setenv("ZELLIJ_PANE_ID", "2")

	for _, configured := range []string{"", "auto", "AUTO", "nonsense"} {
		t.Run(configured, func(t *testing.T) {
			if got := resolveZellijFocusMode(configured); got != daemon.ZellijFocusModePane {
				t.Errorf("resolveZellijFocusMode(%q) = %q, want %q", configured, got, daemon.ZellijFocusModePane)
			}
		})
	}
}

func TestResolveZellijFocusMode_AutoFallsBackToTab(t *testing.T) {
	t.Run("zellij predating focus-pane-id", func(t *testing.T) {
		stubZellij(t, 2, "go-to-tab-name")
		t.Setenv("ZELLIJ_SESSION_NAME", "cubic-weasel")
		t.Setenv("ZELLIJ_PANE_ID", "2")

		if got := resolveZellijFocusMode("auto"); got != daemon.ZellijFocusModeTab {
			t.Errorf("resolveZellijFocusMode(auto) = %q, want %q", got, daemon.ZellijFocusModeTab)
		}
	})

	// A session recognised by the $ZELLIJ marker alone has no pane for
	// focus-pane-id to name, however capable the binary is.
	t.Run("session exporting no pane ID", func(t *testing.T) {
		stubZellij(t, 2, "focus-pane-id")
		t.Setenv("ZELLIJ", "0")
		t.Setenv("ZELLIJ_SESSION_NAME", "cubic-weasel")
		t.Setenv("ZELLIJ_PANE_ID", "")

		if got := resolveZellijFocusMode("auto"); got != daemon.ZellijFocusModeTab {
			t.Errorf("resolveZellijFocusMode(auto) = %q, want %q", got, daemon.ZellijFocusModeTab)
		}
	})

	// The mirror image: $ZELLIJ alone makes the session recognisable, so the
	// hints can name a pane with no session to run the action against.
	t.Run("session exporting no session name", func(t *testing.T) {
		stubZellij(t, 2, "focus-pane-id")
		t.Setenv("ZELLIJ", "0")
		t.Setenv("ZELLIJ_SESSION_NAME", "")
		t.Setenv("ZELLIJ_PANE_ID", "2")

		if got := resolveZellijFocusMode("auto"); got != daemon.ZellijFocusModeTab {
			t.Errorf("resolveZellijFocusMode(auto) = %q, want %q", got, daemon.ZellijFocusModeTab)
		}
	})
}
