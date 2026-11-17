//go:build linux || darwin

package setupwizard

import (
	"errors"
	"maps"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
)

func TestPendingMCPRestorationDoesNotMutateCallerBeforeConflict(t *testing.T) {
	ctx := testCtx(t)
	control, runtime, _, _, generation := managedRuntime(t)
	root := t.TempDir()
	codexMCP := filepath.Join(root, "pending-codex.toml")
	claudeMCP := filepath.Join(root, "pending-claude.json")
	plantPendingIntent(t, ctx, control, runtime, generation, portablesetup.Intent{
		Version: 1, SetupIntentID: "pending-mcp-caller-ownership", Action: "install", Stage: "retire-direct",
		ExpectedGeneration: generation,
		Targets: []portablesetup.IntentTarget{
			{Client: "codex", MCPConfig: codexMCP, Units: []string{"direct-mcp"}},
			{Client: "claude", MCPConfig: claudeMCP, Units: []string{"direct-mcp"}},
		},
	})
	for _, entry := range []string{"plan", "run"} {
		t.Run(entry, func(t *testing.T) {
			req := Request{Action: ActionInstall, Agents: []string{"codex", "claude"}, Yes: true,
				ControlRoot: control, RuntimeRoot: runtime,
				MCPConfig: map[string]string{"claude": filepath.Join(root, "explicit-claude.json")},
			}
			before := maps.Clone(req.MCPConfig)
			var got Result
			var err error
			if entry == "plan" {
				var plan SetupPlan
				plan, err = Plan(ctx, req)
				got = plan.Result
				if plan.Ready || plan.Request.MCPConfig["codex"] != codexMCP {
					t.Fatalf("pending path was not restored before the later conflict: %+v", plan)
				}
			} else {
				got, err = Run(ctx, req)
			}
			if !errors.Is(err, ErrRefused) || got.Outcome != "conflict" || got.Reason != "pending_intent_conflict" {
				t.Fatalf("pending explicit path conflict: %+v %v", got, err)
			}
			if !maps.Equal(req.MCPConfig, before) {
				t.Fatalf("%s changed caller MCP paths before refusing the pending intent: got %v want %v", entry, req.MCPConfig, before)
			}
		})
	}
}
