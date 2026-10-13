//go:build !linux

package main

import (
	"fmt"
	"os"
)

func linuxCallbackMain(string, []string) int {
	fmt.Fprintln(os.Stderr, "Linux callback unavailable on this platform")
	return 1
}
