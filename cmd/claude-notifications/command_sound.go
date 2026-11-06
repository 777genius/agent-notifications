package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/777genius/agent-notifications/internal/audio"
	"github.com/777genius/agent-notifications/internal/logging"
)

// runPlaySound plays a sound file and exits. Designed to be spawned as a detached
// child process so the parent hook process does not wait for audio to finish.
// The parent wires stderr into notification-debug.log, so diagnostics are
// written to stderr in the debug log format.
// Usage: play-sound <path> [--volume <0.0-1.0>] [--device <name>]
func runPlaySound(args []string) {
	logger := logging.NewLoggerFromFile(os.Stderr)
	logger.SetPrefix(fmt.Sprintf("play-sound pid=%d", os.Getpid()))

	if len(args) < 1 {
		logger.Error("sound file path required")
		os.Exit(1)
	}

	soundPath := args[0]
	volume := 1.0
	deviceName := ""

	// Parse optional flags
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--volume":
			if i+1 < len(args) {
				i++
				if v, err := strconv.ParseFloat(args[i], 64); err == nil {
					volume = v
				}
			}
		case "--device":
			if i+1 < len(args) {
				i++
				deviceName = args[i]
			}
		}
	}

	logger.Debug("starting: sound=%s volume=%.2f device=%q", soundPath, volume, deviceName)

	player, err := audio.NewPlayer(deviceName, volume)
	if err != nil {
		logger.Error("failed to init player: %v", err)
		os.Exit(1)
	}
	defer func() { _ = player.Close() }()

	if err := player.Play(soundPath); err != nil {
		logger.Error("failed to play %s: %v", soundPath, err)
		os.Exit(1)
	}
	logger.Debug("finished: sound=%s", soundPath)
}
