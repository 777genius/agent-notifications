// ABOUTME: CLI tool for previewing notification sounds with optional device selection.
// ABOUTME: Supports MP3, WAV, FLAC, OGG/Vorbis, AIFF formats via malgo audio backend.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/audio"
)

func main() {
	// Define flags
	volumeFlag := flag.Float64("volume", 1.0, "Volume level (0.0 to 1.0)")
	deviceFlag := flag.String("device", "", "Audio output device name (empty = system default)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: sound-preview [options] <path-to-audio-file>\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nSupported formats: MP3, WAV, FLAC, OGG/Vorbis, AIFF\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  sound-preview sounds/task-complete.mp3\n")
		fmt.Fprintf(os.Stderr, "  sound-preview --volume 0.3 /System/Library/Sounds/Glass.aiff\n")
		fmt.Fprintf(os.Stderr, "  sound-preview --device \"MacBook Pro-Lautsprecher\" sounds/question.mp3\n")
		fmt.Fprintf(os.Stderr, "\nList available devices:\n")
		fmt.Fprintf(os.Stderr, "  list-devices\n")
	}
	flag.Parse()

	// Validate volume range
	if *volumeFlag < 0.0 || *volumeFlag > 1.0 {
		fmt.Fprintf(os.Stderr, "Error: Volume must be between 0.0 and 1.0 (got %.2f)\n", *volumeFlag)
		os.Exit(1)
	}

	// Check if sound path is provided
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	soundPath := flag.Arg(0)

	// Check if file exists
	if _, err := os.Stat(soundPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: Sound file not found: %s\n", soundPath)
		os.Exit(1)
	}

	// Show info
	volumePercent := int(*volumeFlag * 100)
	if *deviceFlag != "" {
		fmt.Printf("🔊 Playing: %s (volume: %d%%, device: %s)\n", filepath.Base(soundPath), volumePercent, *deviceFlag)
	} else if *volumeFlag < 1.0 {
		fmt.Printf("🔉 Playing: %s (volume: %d%%)\n", filepath.Base(soundPath), volumePercent)
	} else {
		fmt.Printf("🔊 Playing: %s\n", filepath.Base(soundPath))
	}

	// Create audio player with device selection
	player, err := audio.NewPlayer(*deviceFlag, *volumeFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating audio player: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = player.Close() }()

	// Play the sound
	if err := player.Play(soundPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error playing sound: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✓ Playback completed")
}
