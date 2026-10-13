//go:build !darwin || !arm64

package cursorinstall

import "github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/nativeconfig"

func beginCursorProfile(kernel nativeconfig.Kernel, path string) (*nativeconfig.ExactFile, error) {
	return kernel.BeginExactFile(path)
}
