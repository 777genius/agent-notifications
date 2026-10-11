package cursorinstall

import (
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/cursorhooks"
	"runtime"
)

const cursorVersion = "2026.09.28-64d2043"

// CurrentTuple is the closed native user Stop contract for this host. It grants
// no consent, receipt or physical authority; all existing proof still applies.
func CurrentTuple() (string, cursorhooks.ShellContract) {
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		return cursorVersion, cursorhooks.LinuxUserShell32212
	}
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		return "2026.09.18-9a7762b", cursorhooks.DarwinUserShell20260918
	}
	return "", ""
}
