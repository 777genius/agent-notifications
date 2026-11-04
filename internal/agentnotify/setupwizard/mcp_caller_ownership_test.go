package setupwizard

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoveredMCPKeepsCallerMapAndFollowsReusedRequestProfile(t *testing.T) {
	ctx, control := commitControlRuntime(t)
	profiles := []string{t.TempDir(), t.TempDir()}
	for _, profile := range profiles {
		if err := os.WriteFile(filepath.Join(profile, "config.toml"), []byte("title = 'keep'\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	explicitClaude := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(explicitClaude, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{"plan", "inspect"} {
		t.Run(entry, func(t *testing.T) {
			req := Request{Action: ActionInspect, Agents: []string{"codex", "claude"}, ControlRoot: control,
				MCPConfig: map[string]string{"claude": explicitClaude},
			}
			before := maps.Clone(req.MCPConfig)
			for _, profile := range profiles {
				req.CodexHome = profile
				wantCodex := filepath.Join(profile, "config.toml")
				if entry == "plan" {
					plan, err := Plan(ctx, req)
					if err != nil || !plan.Ready {
						t.Fatalf("plan: %+v %v", plan, err)
					}
					if plan.Request.MCPConfig["codex"] != wantCodex || plan.Request.MCPConfig["claude"] != explicitClaude {
						t.Fatalf("discovered and explicit paths changed: %+v", plan.Request.MCPConfig)
					}
				} else {
					got, err := Run(ctx, req)
					if err != nil || got.Outcome != "completed" {
						t.Fatalf("inspect: %+v %v", got, err)
					}
					paths := map[string]string{}
					for _, target := range got.Targets {
						if target.Unit == "direct-mcp" {
							paths[target.Client] = target.ConfigPath
						}
					}
					if paths["codex"] != wantCodex || paths["claude"] != explicitClaude {
						t.Fatalf("inspection reused a stale discovered path: %+v", paths)
					}
				}
				if !maps.Equal(req.MCPConfig, before) {
					t.Errorf("%s mutated caller MCP configuration: got %v want %v", entry, req.MCPConfig, before)
				}
			}
		})
	}
}
