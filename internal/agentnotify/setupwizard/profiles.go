package setupwizard

import (
	"context"
	"errors"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/adapters/clientdetect"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/pi"
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

// ResolvePiAgentDir uses the native provider's profile interpretation. Snapshot
// home, override and original cwd at the CLI boundary; Plan/Run never reread them.
func ResolvePiAgentDir(home, override, workingDir string) (string, error) {
	d := clientdetect.NewOS(home)
	d.Environment = map[string]string{"PI_CODING_AGENT_DIR": override}
	d.WorkingDir = workingDir
	d.LookPath = func(string) (string, error) { return "", errors.New("profile-only discovery") }
	var err error
	d.Registry, err = clients.NewRegistry(pi.New())
	if err != nil {
		return "", err
	}
	rows, err := d.Detect(context.Background())
	if err != nil {
		return "", err
	}
	if len(rows) != 1 || rows[0].DetectionError != nil {
		if len(rows) == 1 {
			return "", rows[0].DetectionError
		}
		return "", ErrRefused
	}
	return rows[0].ConfigRoot, nil
}
