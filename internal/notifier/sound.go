package notifier

import (
	"fmt"
	"os"
	"github.com/777genius/agent-notifications/internal/audio"
	"github.com/777genius/agent-notifications/internal/errorhandler"
	"github.com/777genius/agent-notifications/internal/logging"
	"github.com/777genius/agent-notifications/internal/platform"
)

// playSoundUnlessMuted routes every sound-playing path through one decision
// point, so a muted delivery cannot leak audio via a branch that was missed.
func (n *Notifier) playSoundUnlessMuted(sound string, opts sendOptions) {
	// Detached/inline playback bypasses Focus. Even a failed native delivery
	// must stay quiet on macOS rather than falling back to the Go player.
	if platform.IsMacOS() {
		return
	}
	if opts.muteSound {
		logging.Debug("Sound suppressed for this notification (muted delivery)")
		return
	}
	n.playSoundDetached(sound)
}

// playSoundDetached spawns a detached child process to play the sound.
// The parent hook process does not wait for audio to finish, eliminating
// the ~3.6s delay from notifier.Close() wg.Wait().
// Falls back to playSoundAsync (inline playback) if spawn fails.
func (n *Notifier) playSoundDetached(sound string) {
	if !n.cfg.Notifications.Desktop.Sound || sound == "" {
		return
	}

	if !platform.FileExists(sound) {
		logging.Warn("Sound file not found: %s", sound)
		return
	}

	exe, err := os.Executable()
	if err != nil {
		logging.Warn("Cannot resolve executable for detached sound, falling back to inline: %v", err)
		n.playSoundAsync(sound)
		return
	}

	args := []string{"play-sound", sound}
	volume := n.cfg.Notifications.Desktop.Volume
	if volume >= 0 && volume < 1.0 {
		args = append(args, "--volume", fmt.Sprintf("%.2f", volume))
	}
	if device := n.cfg.Notifications.Desktop.AudioDevice; device != "" {
		args = append(args, "--device", device)
	}

	cmd := execCommand(exe, args...)
	platform.SetDetachedProcAttr(cmd)
	// Hand the child the debug log file as stderr so its playback outcome is
	// not lost to /dev/null. It must be an *os.File: any other writer makes
	// exec copy through a pipe that dies with this short-lived parent.
	if logFile := logging.File(); logFile != nil {
		cmd.Stderr = logFile
	}

	if err := cmd.Start(); err != nil {
		logging.Warn("Failed to spawn detached sound process, falling back to inline: %v", err)
		n.playSoundAsync(sound)
		return
	}

	logging.Debug("Detached sound process spawned: pid=%d sound=%s", cmd.Process.Pid, sound)
	// Do NOT call cmd.Wait() — child process runs independently
}

// playSoundAsync plays sound asynchronously if enabled (inline fallback)
func (n *Notifier) playSoundAsync(sound string) {
	if n.cfg.Notifications.Desktop.Sound && sound != "" {
		// Check if notifier is closing to prevent WaitGroup race
		n.mu.Lock()
		if n.closing {
			n.mu.Unlock()
			logging.Warn("Sound blocked: notifier is closing (notification may have been sent without sound)")
			return
		}
		n.wg.Add(1)
		n.mu.Unlock()

		// Use SafeGo to protect against panics in sound playback goroutine
		errorhandler.SafeGo(func() {
			defer n.wg.Done()
			n.playSound(sound)
		})
	}
}

// initPlayer initializes the audio player once
func (n *Notifier) initPlayer() error {
	n.playerInit.Do(func() {
		deviceName := n.cfg.Notifications.Desktop.AudioDevice
		volume := n.cfg.Notifications.Desktop.Volume

		player, err := audio.NewPlayer(deviceName, volume)
		if err != nil {
			n.playerErr = err
			logging.Error("Failed to initialize audio player: %v", err)
			return
		}

		n.audioPlayer = player

		if deviceName != "" {
			logging.Debug("Audio player initialized with device: %s, volume: %.0f%%", deviceName, volume*100)
		} else {
			logging.Debug("Audio player initialized with default device, volume: %.0f%%", volume*100)
		}
	})

	return n.playerErr
}

// playSound plays a sound file using the audio module
func (n *Notifier) playSound(soundPath string) {
	if !platform.FileExists(soundPath) {
		logging.Warn("Sound file not found: %s", soundPath)
		return
	}

	// Initialize player once
	if err := n.initPlayer(); err != nil {
		logging.Error("Failed to initialize audio player: %v", err)
		return
	}

	// Play sound
	if err := n.audioPlayer.Play(soundPath); err != nil {
		logging.Error("Failed to play sound %s: %v", soundPath, err)
		return
	}

	volume := n.cfg.Notifications.Desktop.Volume
	logging.Debug("Sound played successfully: %s (volume: %.0f%%)", soundPath, volume*100)
}

// Close waits for all sounds to finish playing and cleans up resources
func (n *Notifier) Close() error {
	// Set closing flag to prevent new sounds from being enqueued
	n.mu.Lock()
	n.closing = true
	n.mu.Unlock()

	// Wait for all sounds to finish
	n.wg.Wait()

	// Close audio player if it was initialized
	n.mu.Lock()
	if n.audioPlayer != nil {
		if err := n.audioPlayer.Close(); err != nil {
			logging.Warn("Failed to close audio player: %v", err)
		}
		n.audioPlayer = nil
		logging.Debug("Audio player closed")
	}
	n.mu.Unlock()

	return nil
}
