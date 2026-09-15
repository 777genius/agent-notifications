//go:build !darwin && !linux && !windows

package warpfocus

import "fmt"

func openURL(focusURL string) error {
	return fmt.Errorf("warpfocus: opening %s is not supported on this platform", focusURL)
}
