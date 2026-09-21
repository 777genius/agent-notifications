//go:build !darwin

package installruntime

import "fmt"

func swapBundles(staged, live string, expected ...[]PathAnchor) error {
	return fmt.Errorf("atomic callback bundle swap requires qualified Darwin filesystem semantics")
}
