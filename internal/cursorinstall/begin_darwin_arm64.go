//go:build darwin && arm64

package cursorinstall

import "github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"

// Reuse the qualified Darwin ordinary-file owner; no plist/metadata projection.
func beginCursorProfile(kernel nativeconfig.Kernel, path string) (*nativeconfig.ExactFile, error) {
	return kernel.BeginPlainExactFile(path)
}
