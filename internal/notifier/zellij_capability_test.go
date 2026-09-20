package notifier

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// stubZellij puts a fake zellij on PATH that accepts only the named subcommands.
//
// rejectStatus is what it returns for an unknown one: zellij's own has been both
// 1 (clap 2) and 2 (clap 3+), and the probe has to read either as unsupported.
func stubZellij(t *testing.T, rejectStatus int, supported ...string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub zellij is a /bin/sh script")
	}

	script := "#!/bin/sh\n"
	for _, subcommand := range supported {
		script += "if [ \"$1\" = \"action\" ] && [ \"$2\" = \"" + subcommand + "\" ]; then exit 0; fi\n"
	}
	script += "exit " + strconv.Itoa(rejectStatus) + "\n"

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "zellij"), []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write the stub zellij: %v", err)
	}
	t.Setenv("PATH", dir)
}

func TestZellijSupportsPaneFocus(t *testing.T) {
	t.Run("zellij carrying focus-pane-id", func(t *testing.T) {
		stubZellij(t, 2, "focus-pane-id")
		if !ZellijSupportsPaneFocus() {
			t.Error("ZellijSupportsPaneFocus() = false, want true when zellij accepts the subcommand")
		}
	})

	t.Run("zellij predating focus-pane-id", func(t *testing.T) {
		stubZellij(t, 2, "go-to-tab-name")
		if ZellijSupportsPaneFocus() {
			t.Error("ZellijSupportsPaneFocus() = true, want false when zellij rejects the subcommand")
		}
	})

	// The clap 2 era exited 1 rather than 2 for an unknown subcommand, and those
	// releases are exactly the ones too old to have focus-pane-id.
	t.Run("zellij rejecting with the older status", func(t *testing.T) {
		stubZellij(t, 1, "go-to-tab-name")
		if ZellijSupportsPaneFocus() {
			t.Error("ZellijSupportsPaneFocus() = true, want false when the rejection exits 1")
		}
	})

	t.Run("no zellij on PATH", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if ZellijSupportsPaneFocus() {
			t.Error("ZellijSupportsPaneFocus() = true, want false when zellij is not installed")
		}
	})

	// The probe sits ahead of the notification, so a zellij that never answers
	// has to be cut loose rather than waited on.
	t.Run("zellij that never answers", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the stub zellij is a /bin/sh script")
		}

		dir := t.TempDir()
		// exec replaces the shell, so the process the probe kills is the one that
		// hangs and nothing is orphaned. The probe reads no output, so unlike the
		// dump-layout timeout there is no pipe for a child to hold open.
		if err := os.WriteFile(filepath.Join(dir, "zellij"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
			t.Fatalf("failed to write the stub zellij: %v", err)
		}
		// Unlike the other stubs this one keeps the real $PATH behind it, because
		// sleep is a program rather than a shell builtin. The stub still wins for
		// the name zellij, which is the only one being looked up.
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

		original := zellijProbeTimeout
		zellijProbeTimeout = 50 * time.Millisecond
		t.Cleanup(func() { zellijProbeTimeout = original })

		start := time.Now()
		supported := ZellijSupportsPaneFocus()
		elapsed := time.Since(start)

		if supported {
			t.Error("ZellijSupportsPaneFocus() = true, want false when the probe times out")
		}
		if elapsed > time.Second {
			t.Errorf("ZellijSupportsPaneFocus() took %v, want it to give up at the timeout", elapsed)
		}
	})
}
