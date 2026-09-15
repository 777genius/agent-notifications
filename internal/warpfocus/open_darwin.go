//go:build darwin

package warpfocus

import "os/exec"

func openURL(focusURL string) error {
	return exec.Command("open", focusURL).Run()
}
