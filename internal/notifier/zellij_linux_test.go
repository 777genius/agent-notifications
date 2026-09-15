//go:build linux

package notifier

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/daemon"
)

func stubZellijLayout(t *testing.T, layout string) {
	t.Helper()

	// $PATH is the stub's own directory and nothing else, so the script can use
	// shell builtins only — echo prints the layout, cat would not be found.
	// Single quotes are safe: KDL layouts quote with ".
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"action\" ] && [ \"$2\" = \"dump-layout\" ]; then\n" +
		"echo '" + layout + "'\n" +
		"exit 0\nfi\nexit 2\n"

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "zellij"), []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write the stub zellij: %v", err)
	}
	t.Setenv("PATH", dir)
}

func zellijFocusConfig(mode string) *config.Config {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.ZellijFocus = mode
	return cfg
}

// The stub rejects every subcommand, so an exec that should not have happened
// shows up as a mode rather than passing quietly.
func TestApplyZellijFocusHints_OutsideZellij(t *testing.T) {
	stubZellij(t, 2)
	t.Setenv("ZELLIJ", "")
	t.Setenv("ZELLIJ_SESSION_NAME", "")
	t.Setenv("ZELLIJ_PANE_ID", "")

	var hints daemon.FocusHints
	applyZellijFocusHints(zellijFocusConfig("auto"), &hints)

	if hints != (daemon.FocusHints{}) {
		t.Errorf("applyZellijFocusHints() left %+v, want the zero value", hints)
	}
}

func TestApplyZellijFocusHints_PaneNeedsNoLayout(t *testing.T) {
	stubZellij(t, 2)
	t.Setenv("ZELLIJ_SESSION_NAME", "cubic-weasel")
	t.Setenv("ZELLIJ_PANE_ID", "2")

	var hints daemon.FocusHints
	applyZellijFocusHints(zellijFocusConfig("pane"), &hints)

	want := daemon.FocusHints{ZellijSession: "cubic-weasel", ZellijPaneID: "2", ZellijMode: daemon.ZellijFocusModePane}
	if hints != want {
		t.Errorf("applyZellijFocusHints() = %+v, want %+v", hints, want)
	}
}

func TestApplyZellijFocusHints_TabCapturesTabName(t *testing.T) {
	stubZellijLayout(t, `layout {
    tab name="claude" focus=true {
        pane
    }
}`)
	t.Setenv("ZELLIJ_SESSION_NAME", "cubic-weasel")
	t.Setenv("ZELLIJ_PANE_ID", "2")

	var hints daemon.FocusHints
	applyZellijFocusHints(zellijFocusConfig("tab"), &hints)

	want := daemon.FocusHints{
		ZellijSession: "cubic-weasel",
		ZellijPaneID:  "2",
		ZellijTabName: "claude",
		ZellijMode:    daemon.ZellijFocusModeTab,
	}
	if hints != want {
		t.Errorf("applyZellijFocusHints() = %+v, want %+v", hints, want)
	}
}

// The layout parses but names no focused tab, which is the same dead end as a
// dump-layout that fails outright.
func TestApplyZellijFocusHints_TabWithoutTabNameDegradesToOff(t *testing.T) {
	stubZellijLayout(t, `layout {
    tab name="claude" {
        pane
    }
}`)
	t.Setenv("ZELLIJ_SESSION_NAME", "cubic-weasel")
	t.Setenv("ZELLIJ_PANE_ID", "2")

	var hints daemon.FocusHints
	applyZellijFocusHints(zellijFocusConfig("tab"), &hints)

	want := daemon.FocusHints{ZellijSession: "cubic-weasel", ZellijPaneID: "2", ZellijMode: daemon.ZellijFocusModeOff}
	if hints != want {
		t.Errorf("applyZellijFocusHints() = %+v, want %+v", hints, want)
	}
}
