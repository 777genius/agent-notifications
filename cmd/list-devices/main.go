// ABOUTME: CLI tool to list available audio output devices.
// ABOUTME: Used to find device names for the audioDevice config option.

package main

import (
	"fmt"
	"os"

	"github.com/777genius/agent-notifications/internal/audio"
)

func main() {
	devices, err := audio.ListDevices()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing audio devices: %v\n", err)
		os.Exit(1)
	}

	if len(devices) == 0 {
		fmt.Println("No audio output devices found.")
		os.Exit(0)
	}

	fmt.Println("Available audio output devices:")
	fmt.Println()

	for i, dev := range devices {
		defaultMarker := ""
		if dev.IsDefault {
			defaultMarker = " (default)"
		}
		fmt.Printf("  %d: %s%s\n", i, dev.Name, defaultMarker)
	}

	fmt.Println()
	fmt.Println("To use a specific device, add to config.json:")
	fmt.Println(`  {`)
	fmt.Println(`    "notifications": {`)
	fmt.Println(`      "desktop": {`)
	fmt.Println(`        "audioDevice": "DEVICE_NAME"`)
	fmt.Println(`      }`)
	fmt.Println(`    }`)
	fmt.Println(`  }`)
}
