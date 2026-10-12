//go:build !windows

package main

import (
	"context"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/clientdetect"
	"path/filepath"
)

func probeCursorVersion(ctx context.Context, executable string, environment []string) (string, error) {
	return clientdetect.NewOS(filepath.Dir(executable)).ProbeVersionWithEnvironment(ctx, executable, environment)
}
