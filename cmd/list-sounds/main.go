// ABOUTME: CLI tool to list available notification sounds and optionally play them.
// ABOUTME: Discovers built-in and system sounds, supports JSON output and playback.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/audio"
	"github.com/777genius/agent-notifications/internal/sounds"
)

func main() {
	playFlag := flag.String("play", "", "Play a sound by name")
	volumeFlag := flag.Float64("volume", 0.3, "Volume level for playback (0.0 to 1.0)")
	jsonFlag := flag.Bool("json", false, "Output in JSON format")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: list-sounds [options]\n\n")
		fmt.Fprintf(os.Stderr, "List available notification sounds (built-in and system).\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  list-sounds                         # List all available sounds\n")
		fmt.Fprintf(os.Stderr, "  list-sounds --json                  # Output as JSON\n")
		fmt.Fprintf(os.Stderr, "  list-sounds --play task-complete     # Play a sound\n")
		fmt.Fprintf(os.Stderr, "  list-sounds --play Glass --volume 0.5  # Play at 50%% volume\n")
	}
	flag.Parse()

	// Validate volume
	if *volumeFlag < 0.0 || *volumeFlag > 1.0 {
		fmt.Fprintf(os.Stderr, "Error: Volume must be between 0.0 and 1.0 (got %.2f)\n", *volumeFlag)
		os.Exit(1)
	}

	pluginRoot := getPluginRoot()

	available := sounds.Discover(sounds.DiscoverOptions{
		PluginRoot:     pluginRoot,
		IncludeBuiltIn: true,
		IncludeSystem:  true,
	})

	// --play mode
	if *playFlag != "" {
		s, found := sounds.FindByName(*playFlag, available)
		if !found {
			fmt.Fprintf(os.Stderr, "Error: sound %q not found\n\n", *playFlag)
			fmt.Fprintf(os.Stderr, "Available sounds:\n")
			for _, s := range available {
				fmt.Fprintf(os.Stderr, "  %s\n", s.Name)
			}
			os.Exit(1)
		}

		volumePercent := int(*volumeFlag * 100)
		fmt.Printf("Playing: %s (volume: %d%%)\n", s.Name, volumePercent)

		player, err := audio.NewPlayer("", *volumeFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating audio player: %v\n", err)
			os.Exit(1)
		}
		defer func() { _ = player.Close() }()

		if err := player.Play(s.Path); err != nil {
			fmt.Fprintf(os.Stderr, "Error playing sound: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("Playback completed")
		return
	}

	// --json mode
	if *jsonFlag {
		if available == nil {
			available = []sounds.SoundInfo{}
		}
		data, err := json.MarshalIndent(available, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error marshaling JSON: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(data))
		return
	}

	// Default: grouped text output
	if len(available) == 0 {
		fmt.Println("No sounds found.")
		os.Exit(0)
	}

	// Group by source
	var builtIn, system []sounds.SoundInfo
	for _, s := range available {
		switch s.Source {
		case "builtin":
			builtIn = append(builtIn, s)
		case "system":
			system = append(system, s)
		}
	}

	if len(builtIn) > 0 {
		fmt.Println("Built-in sounds:")
		fmt.Println()
		for _, s := range builtIn {
			desc := ""
			if s.Description != "" {
				desc = " - " + s.Description
			}
			fmt.Printf("  %s.%s%s\n", s.Name, s.Format, desc)
		}
	}

	if len(system) > 0 {
		fmt.Println()
		fmt.Println("System sounds:")
		fmt.Println()
		for _, s := range system {
			desc := ""
			if s.Description != "" {
				desc = " - " + s.Description
			}
			fmt.Printf("  %s.%s%s\n", s.Name, s.Format, desc)
		}
	}

	fmt.Println()
	fmt.Println("To preview a sound:")
	fmt.Println("  list-sounds --play <name>")
	fmt.Println()
	fmt.Println("To use a sound in config.json:")
	fmt.Println(`  {`)
	fmt.Println(`    "statuses": {`)
	fmt.Println(`      "task_complete": {`)
	fmt.Println(`        "sound": "${CLAUDE_PLUGIN_ROOT}/sounds/task-complete.mp3"`)
	fmt.Println(`      }`)
	fmt.Println(`    }`)
	fmt.Println(`  }`)
}

func getPluginRoot() string {
	// Try CLAUDE_PLUGIN_ROOT environment variable first
	if root := os.Getenv("CLAUDE_PLUGIN_ROOT"); root != "" {
		return root
	}

	// Try to find plugin root relative to executable
	exe, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exe)
		if filepath.Base(exeDir) == "bin" {
			return filepath.Dir(exeDir)
		}
		return filepath.Dir(exeDir)
	}

	// Fallback to current directory
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}
