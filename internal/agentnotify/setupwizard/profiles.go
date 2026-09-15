package setupwizard

import (
	"os"
	"strings"
)

// ApplyEnvDefaults fills omitted Codex/Claude profile roots from the process
// environment once. Explicit Request fields win. HOME is not a fallback.
// The CLI snapshots these values onto EnvCodexHome/EnvClaudeConfig; Run and
// Plan apply that snapshot after resume instead of rereading the environment.
func ApplyEnvDefaults(req Request) Request {
	if req.CodexHome == "" {
		req.CodexHome = strings.TrimSpace(os.Getenv("CODEX_HOME"))
	}
	if req.ClaudeConfig == "" {
		req.ClaudeConfig = strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	}
	return req
}
