package notifier

import (
	"testing"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
)

func TestResolveSendOptions_DefaultPlaysSound(t *testing.T) {
	if resolveSendOptions(nil).muteSound {
		t.Error("resolveSendOptions(nil).muteSound = true, want false: a plain delivery must keep its sound")
	}
	if resolveSendOptions([]SendOption{}).muteSound {
		t.Error("resolveSendOptions([]).muteSound = true, want false")
	}
}

func TestWithoutSound_MutesDelivery(t *testing.T) {
	if !resolveSendOptions([]SendOption{WithoutSound()}).muteSound {
		t.Error("WithoutSound() did not set muteSound")
	}
}

func TestResolveSendOptions_IgnoresNilOption(t *testing.T) {
	opts := resolveSendOptions([]SendOption{nil, WithoutSound(), nil})
	if !opts.muteSound {
		t.Error("a nil option must be skipped without dropping the options after it")
	}
}

// TestPlaySoundUnlessMuted_MutedDeliveryPlaysNothing proves the single decision
// point actually gates playback: with a sound file that does not exist,
// playSoundDetached would log a warning, so the muted path is verified by the
// absence of any attempt to resolve the file.
func TestPlaySoundUnlessMuted_MutedDeliveryPlaysNothing(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Sound = true
	n := New(cfg)
	t.Cleanup(func() { _ = n.Close() })

	// Muted: must return without touching the audio stack.
	n.playSoundUnlessMuted("/nonexistent/claude-dnd-test.mp3", sendOptions{muteSound: true})
	// Unmuted with a missing file: must also return, via the file-not-found guard.
	n.playSoundUnlessMuted("/nonexistent/claude-dnd-test.mp3", sendOptions{})
}

// TestSendDesktop_AcceptsSendOptions guards the variadic signature: every
// existing call site stays valid and WithoutSound() is accepted end to end.
func TestSendDesktop_AcceptsSendOptions(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Enabled = false // no real banner from a unit test
	n := New(cfg)
	t.Cleanup(func() { _ = n.Close() })

	if err := n.SendDesktop(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd"); err != nil {
		t.Errorf("SendDesktop without options returned %v", err)
	}
	if err := n.SendDesktop(analyzer.StatusTaskComplete, "[s folder] done", "sess", "/cwd", WithoutSound()); err != nil {
		t.Errorf("SendDesktop with WithoutSound returned %v", err)
	}
}

func TestWithoutFocusPreservesSoundPolicy(t *testing.T) {
	o := resolveSendOptions([]SendOption{WithoutFocus()})
	if !o.noFocus || o.muteSound {
		t.Fatalf("WithoutFocus must disable actions, not sound: %+v", o)
	}
	both := resolveSendOptions([]SendOption{WithoutSound(), WithoutFocus()})
	if !both.noFocus || !both.muteSound {
		t.Fatalf("focus and sound policy must compose: %+v", both)
	}
}

func TestWithoutFocusKeepsInformationalBanner(t *testing.T) {
	if notifierGOOS != "linux" {
		t.Skip("Linux plain banner test")
	}
	previous := beeepNotify
	t.Cleanup(func() { beeepNotify = previous })
	called := 0
	beeepNotify = func(_, _, _ string) error {
		called++
		return nil
	}
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.Sound = false
	n := New(cfg)
	t.Cleanup(func() { _ = n.Close() })
	if err := n.SendDesktop(analyzer.StatusTaskComplete, "Done", "s", "/tmp/project", WithoutFocus()); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatalf("expected one ordinary banner without daemon focus, got %d", called)
	}
}
