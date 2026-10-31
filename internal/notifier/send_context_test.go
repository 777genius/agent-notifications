package notifier

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
)

func TestSendDesktopContextPolicyAndDeadline(t *testing.T) {
	cfg := config.DefaultConfig()
	n := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := n.SendDesktopContext(ctx, analyzer.StatusTaskComplete, "fixed", "session", t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	cfg.Notifications.Desktop.Enabled = false
	if err := n.SendDesktopContext(context.Background(), analyzer.StatusTaskComplete, "fixed", "session", t.TempDir()); !errors.Is(err, ErrDesktopSuppressed) {
		t.Fatal(err)
	}
	cfg.Notifications.Desktop.Enabled = true
	delay := 10
	cfg.Notifications.NotifyDelaySeconds = &delay
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := n.SendDesktopContext(ctx, analyzer.StatusTaskComplete, "fixed", "session", t.TempDir()); !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("delay escaped context: %v", err)
	}
}

func TestContextSoundHonorsConfiguredCueAndSilence(t *testing.T) {
	previous := runContextSound
	t.Cleanup(func() { runContextSound = previous })
	calls := 0
	cue := filepath.Join(t.TempDir(), "configured cue.wav")
	if err := os.WriteFile(cue, []byte("inert fixture, never played"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Sound = true
	cfg.Notifications.Desktop.Volume = .25
	cfg.Notifications.Desktop.AudioDevice = "selected-output"
	n := New(cfg)
	runContextSound = func(ctx context.Context, cmd *exec.Cmd) error {
		calls++
		if !slices.Equal(cmd.Args[1:], []string{"play-sound", cue, "--volume", "0.25", "--device", "selected-output"}) {
			t.Fatalf("sound args=%q", cmd.Args)
		}
		return ctx.Err()
	}
	if err := n.playSoundContext(context.Background(), cue, sendOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := n.playSoundContext(context.Background(), cue, sendOptions{muteSound: true}); err != nil {
		t.Fatal(err)
	}
	cfg.Notifications.Desktop.Sound = false
	if err := n.playSoundContext(context.Background(), cue, sendOptions{}); err != nil {
		t.Fatal(err)
	}
	cfg.Notifications.Desktop.Sound = true
	cfg.Notifications.Desktop.Volume = 0
	if err := n.playSoundContext(context.Background(), cue, sendOptions{}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("played %d cues", calls)
	}
}

func TestRemoteProcessNeverAdmitsLocalDesktop(t *testing.T) {
	for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "native remote context")
			if HasLocalTerminalContext(context.Background()) {
				t.Fatal("SSH context became local")
			}
		})
	}
}
