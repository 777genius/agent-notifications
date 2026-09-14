package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/managedstdio"
)

// dispatchManagedStdio recognizes the UAP private helper protocol before any
// config, log, prompt, or notification initialization. Unknown versions do not
// fall through to the ordinary CLI.
func dispatchManagedStdio(args []string, stderr io.Writer) (bool, int) {
	handled, code := managedstdio.Dispatch(args, stderr)
	if handled {
		return true, code
	}
	if len(args) > 0 && strings.HasPrefix(args[0], "--internal-stdio-") {
		fmt.Fprintln(stderr, "managed stdio: unknown protocol version")
		return true, 126
	}
	return false, 0
}
