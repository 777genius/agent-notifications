//go:build darwin

package notifier

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
)

// These contract tests intercept every launch, including detached audio, so
// neither native delivery nor a regression can emit real banners or sound.
func TestSendDesktopMacNativeCustomSound(t *testing.T) {
	for _, tc := range []struct {
		name       string
		volume     float64
		sound      bool
		mute       bool
		missing    bool
		wantVolume string
	}{
		{name: "custom sound at quarter volume", volume: .25, sound: true, wantVolume: "0.25"},
		{name: "full volume", volume: 1, sound: true, wantVolume: "1"},
		{name: "small nonzero volume stays nonzero", volume: .001, sound: true, wantVolume: "0.001"},
		{name: "zero volume", volume: 0, sound: true},
		{name: "sound disabled", volume: .25},
		{name: "muted delivery", volume: .25, sound: true, mute: true},
		{name: "missing custom sound", volume: .25, sound: true, missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, soundPath, captured := captureMacSoundDelivery(t, false)
			cfg.Notifications.Desktop.Sound = tc.sound
			cfg.Notifications.Desktop.Volume = tc.volume
			if tc.missing {
				if err := os.Remove(soundPath); err != nil {
					t.Fatal(err)
				}
			}
			n := New(cfg)
			t.Cleanup(func() { _ = n.Close() })
			var opts []SendOption
			if tc.mute {
				opts = append(opts, WithoutSound())
			}
			if err := n.SendDesktop(analyzer.StatusAPIError, "-nosound", "session", "", opts...); err != nil {
				t.Fatal(err)
			}
			args := *captured
			if getArgValue(args, "-message") != "-nosound" {
				t.Fatalf("flag-like message was changed: %v", args)
			}
			if tc.wantVolume != "" {
				if getArgValue(args, "-soundFile") != soundPath || getArgValue(args, "-soundVolume") != tc.wantVolume {
					t.Fatalf("native sound arguments = %v, want %q at %s", args, soundPath, tc.wantVolume)
				}
			} else if slices.Contains(args, "-soundFile") || slices.Contains(args, "-soundVolume") {
				t.Fatalf("quiet delivery forwarded a custom sound: %v", args)
			}
			if slices.Contains(args, "-timeSensitive") {
				t.Fatalf("ordinary delivery must respect Focus: %v", args)
			}
			// One literal value and one real boolean flag. Keeping the latter
			// makes old helpers that ignore custom-sound flags fail quietly.
			if countFlag(args, "-nosound") != 2 {
				t.Fatalf("silent compatibility flag or literal message lost: %v", args)
			}
		})
	}
}

func TestSendDesktopMacNativeFailureDoesNotLaunchGoAudio(t *testing.T) {
	cfg, _, _ := captureMacSoundDelivery(t, true)
	cfg.Notifications.Desktop.Sound = true
	cfg.Notifications.Desktop.Volume = .25
	fallbackCalled := false
	withBeeepNotify(t, func(string, string, any) error {
		fallbackCalled = true
		return nil
	})
	n := New(cfg)
	t.Cleanup(func() { _ = n.Close() })
	if err := n.SendDesktop(analyzer.StatusAPIError, "fallback", "session", ""); err != nil {
		t.Fatal(err)
	}
	if !fallbackCalled {
		t.Fatal("native failure did not use the desktop fallback")
	}
	if n.audioPlayer != nil {
		t.Fatal("macOS fallback initialized the Go audio player")
	}
}

func captureMacSoundDelivery(t *testing.T, failNative bool) (*config.Config, string, *[]string) {
	t.Helper()
	withBeeepNotify(t, func(string, string, any) error {
		t.Error("unexpected native delivery fallback")
		return nil
	})
	root := t.TempDir()
	helper := filepath.Join(root, "bin", "ClaudeNotifier.app", "Contents", "MacOS", "terminal-notifier-modern")
	if err := os.MkdirAll(filepath.Dir(helper), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, []byte("inert helper"), 0600); err != nil {
		t.Fatal(err)
	}
	soundPath := filepath.Join(root, "custom cue.wav")
	if err := os.WriteFile(soundPath, []byte("inert audio fixture; never decoded"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PLUGIN_ROOT", root)
	t.Setenv("TMUX", "")
	t.Setenv("STY", "")
	cfg := config.DefaultConfig()
	cfg.Notifications.Desktop.ClickToFocus = false
	cfg.Notifications.Desktop.TerminalBundleID = "com.apple.Terminal"
	bell := false
	cfg.Notifications.Desktop.TerminalBell = &bell
	info := cfg.Statuses[string(analyzer.StatusAPIError)]
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// A relative configured path must become absolute at the native boundary.
	info.Sound, err = filepath.Rel(cwd, soundPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Statuses[string(analyzer.StatusAPIError)] = info
	previous := execCommand
	var captured []string
	execCommand = func(name string, args ...string) *exec.Cmd {
		if name != "open" {
			t.Errorf("macOS notification tried to launch detached audio: %s %v", name, args)
			return exec.Command("/usr/bin/true")
		}
		captured = append([]string(nil), args...)
		if failNative {
			return exec.Command("/usr/bin/false")
		}
		return exec.Command("/usr/bin/true")
	}
	t.Cleanup(func() { execCommand = previous })
	return cfg, soundPath, &captured
}
