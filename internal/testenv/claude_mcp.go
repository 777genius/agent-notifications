package testenv

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ClaudeMCPHealth simulates only the fixture's exact installed local stdio
// server. It is a test runner response, not native Claude health evidence.
func ClaudeMCPHealth(profile string, argv []string) (body []byte, handled bool, err error) {
	if len(argv) < 3 || argv[1] != "mcp" || argv[2] != "get" {
		return nil, false, nil
	}
	if len(argv) != 4 || profile == "" {
		return nil, true, fmt.Errorf("invalid TEST Claude MCP command")
	}
	selector := argv[3]
	server, ok := strings.CutPrefix(selector, "plugin:agent-notify:")
	if !ok || (server != "agent-notify" && server != "agent-notifications") {
		return nil, true, fmt.Errorf("unknown TEST Claude MCP selector")
	}
	paths, err := filepath.Glob(filepath.Join(profile, "skills", "*", ".claude-plugin", "plugin.json"))
	if err != nil {
		return nil, true, err
	}
	var matches []string
	for _, path := range paths {
		root := filepath.Dir(filepath.Dir(path))
		if strings.HasPrefix(filepath.Base(root), ".") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, true, err
		}
		var manifest map[string]any
		if err := json.Unmarshal(data, &manifest); err != nil {
			return nil, true, err
		}
		if manifest["name"] == "agent-notify" {
			matches = append(matches, root)
		}
	}
	if len(matches) != 1 {
		return nil, true, fmt.Errorf("TEST Claude MCP requires one installed plugin")
	}
	data, err := os.ReadFile(filepath.Join(matches[0], ".mcp.json"))
	if err != nil {
		return nil, true, err
	}
	var servers map[string]map[string]any
	if err := json.Unmarshal(data, &servers); err != nil {
		return nil, true, err
	}
	entry := servers[server]
	command, ok := entry["command"].(string)
	if _, remote := entry["url"]; remote || !ok || strings.TrimSpace(command) == "" || (entry["type"] != nil && entry["type"] != "stdio") {
		return nil, true, fmt.Errorf("TEST Claude MCP requires the installed stdio projection")
	}
	return []byte(selector + ":\n  Scope: Dynamic config (from command line)\n  Status: ✔ Connected\n  Type: stdio\n"), true, nil
}
