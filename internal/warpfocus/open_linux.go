//go:build linux

package warpfocus

import (
	"fmt"
	"os/exec"
)

func openURL(focusURL string) error {
	if _, err := exec.LookPath("xdg-open"); err == nil {
		return exec.Command("xdg-open", focusURL).Run()
	}
	if _, err := exec.LookPath("gio"); err == nil {
		return exec.Command("gio", "open", focusURL).Run()
	}
	return fmt.Errorf("warpfocus: xdg-open/gio not found")
}
