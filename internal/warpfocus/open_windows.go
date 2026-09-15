//go:build windows

package warpfocus

import "os/exec"

func openURL(focusURL string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", focusURL).Run()
}
