package testenv

// CodexNativeCLIProbeSource is a self-contained Go TEST process. When the
// installed client selects an exact CODEX_HOME, it models native marketplace,
// plugin and app-server lifecycle, including selected user skill/MCP discovery.
// Without CODEX_HOME it preserves the original inert helper fixture. It never
// asserts that a real Codex installation was observed.
const CodexNativeCLIProbeSource = `package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func fail() { os.Exit(99) }
func read(root, name string) []byte {
	body, err := os.ReadFile(filepath.Join(root, name))
	if err != nil { fail() }
	return body
}
func write(root, name string, body []byte) {
	if os.WriteFile(filepath.Join(root, name), body, 0600) != nil { fail() }
}
func version(source string) string {
	var manifest struct { Name, Version string }
	body, err := os.ReadFile(filepath.Join(source, ".codex-plugin", "plugin.json"))
	if err != nil { body = read(source, "plugin.json") }
	if json.Unmarshal(body, &manifest) != nil || manifest.Name != "agent-notify" || manifest.Version == "" { fail() }
	return manifest.Version
}
func installed(root string) (id, source, ver string) {
	id = string(read(root, "TEST-plugin"))
	source = string(read(root, "TEST-source"))
	ver = version(source)
	if !strings.Contains(id, "@") || !filepath.IsAbs(source) { fail() }
	return
}
func serve(root string) {
	id, source, ver := installed(root)
	if wd, err := os.Getwd(); err != nil || wd != source { fail() }
	name, marketplace, ok := strings.Cut(id, "@")
	if !ok || name != "agent-notify" || marketplace == "" { fail() }
	// The actual selected package supplies the MCP identity. Do not invent
	// a passing server when the package contains a different component.
	var declared map[string]any
	if json.Unmarshal(read(source, "mcp.json"), &declared) != nil { fail() }
	servers, ok := declared["mcpServers"].(map[string]any)
	if !ok || len(servers) != 1 { fail() }
	serverName := ""
	for selected := range servers {
		if selected != "agent-notify" && selected != "agent-notifications" { fail() }
		serverName = selected
	}
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 4096), 1<<20)
	out := json.NewEncoder(os.Stdout)
	for step := 0; scan.Scan(); step++ {
		var row struct { ID int; Method string; Params map[string]any }
		if json.Unmarshal(scan.Bytes(), &row) != nil { fail() }
		switch step {
		case 0:
			if row.ID != 1 || row.Method != "initialize" { fail() }
			if out.Encode(map[string]any{"id":1,"result":map[string]any{"userAgent":"agentplugins/0.162.0","codexHome":root}}) != nil { fail() }
		case 1:
			if row.ID != 0 || row.Method != "initialized" { fail() }
		case 2:
			if row.ID != 3 || row.Method != "skills/list" || row.Params["forceReload"] != true { fail() }
			dirs, ok := row.Params["cwds"].([]any)
			if !ok || len(dirs) != 1 || dirs[0] != source { fail() }
			skill := map[string]any{"name":name+":agent-notifications","scope":"user","enabled":true,"pluginId":id,
				"path":filepath.Join(root,"plugins","cache",marketplace,name,ver,"skills","agent-notifications","SKILL.md")}
			if out.Encode(map[string]any{"id":3,"result":map[string]any{"data":[]any{map[string]any{"cwd":source,"errors":[]any{},"skills":[]any{skill}}}}}) != nil { fail() }
		case 3:
			if row.ID != 4 || row.Method != "mcpServerStatus/list" || row.Params["serverName"] != serverName || row.Params["detail"] != "toolsAndAuthOnly" { fail() }
			mcp := map[string]any{"name":serverName,"pluginId":id,"authStatus":"unsupported","runtimeStatus":"connected",
				"serverInfo":map[string]any{"name":serverName,"version":ver},"tools":map[string]any{}}
			if out.Encode(map[string]any{"id":4,"result":map[string]any{"data":[]any{mcp},"nextCursor":nil}}) != nil { fail() }
			return
		default:
			fail()
		}
	}
	fail()
}
func main() {
	root := os.Getenv("CODEX_HOME")
	// The same TEST binary also supplies inert package/helper bytes. Only an
	// explicit Codex profile enables the stateful installed CLI protocol.
	if root == "" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true})
		return
	}
	if !filepath.IsAbs(root) { fail() }
	args := os.Args[1:]
	trace, err := os.OpenFile(filepath.Join(root, "TEST-trace"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil { fail() }
	_, err = trace.WriteString(strings.Join(args, " ") + "\n")
	closeErr := trace.Close()
	if err != nil || closeErr != nil { fail() }
	if len(args) == 1 && args[0] == "app-server" { serve(root); return }
	if len(args) == 5 && args[0] == "plugin" && args[1] == "marketplace" && args[2] == "add" && args[4] == "--json" {
		if !filepath.IsAbs(args[3]) { fail() }
		write(root, "TEST-source", []byte(args[3]))
	} else if len(args) == 4 && args[0] == "plugin" && args[1] == "add" && args[3] == "--json" {
		if !strings.Contains(args[2], "@") { fail() }
		write(root, "TEST-plugin", []byte(args[2]))
	} else if len(args) == 4 && args[0] == "plugin" && args[1] == "remove" && args[3] == "--json" {
		id, err := os.ReadFile(filepath.Join(root, "TEST-plugin"))
		if err != nil || string(id) != args[2] { fail() }
		if os.Remove(filepath.Join(root, "TEST-plugin")) != nil { fail() }
	} else if len(args) == 5 && args[0] == "plugin" && args[1] == "marketplace" && (args[2] == "remove" || args[2] == "update") && args[4] == "--json" {
		if args[2] == "remove" { _ = os.Remove(filepath.Join(root, "TEST-source")) }
	} else if len(args) == 3 && args[0] == "plugin" && args[1] == "list" && args[2] == "--json" {
		entries := []any{}
		if _, err := os.Stat(filepath.Join(root, "TEST-plugin")); err == nil {
			id, source, ver := installed(root)
			name, market, _ := strings.Cut(id, "@")
			entries = append(entries, map[string]any{"pluginId":id,"name":name,"marketplaceName":market,
				"installed":true,"enabled":true,"version":ver,"source":map[string]any{"source":"local","path":source}})
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"installed":entries})
		return
	} else { fail() }
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{})
}`
