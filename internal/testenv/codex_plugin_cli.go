package testenv

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// CodexPluginCLI models only the exact CLI registry commands exercised by
// test-only native runners. The state belongs to the selected CODEX_HOME and
// changes only after a successful matching command; it never edits real profiles.
// It is not evidence that an installed Codex executable was exercised.
var codexCLIState = struct {
	sync.Mutex
	byProfile map[string]map[string]bool
}{byProfile: make(map[string]map[string]bool)}

func CodexPluginCLI(env, argv []string) ([]byte, bool, error) {
	if len(argv) < 2 || argv[1] != "plugin" {
		return nil, false, nil
	}
	home := ""
	for _, entry := range env {
		if !strings.HasPrefix(entry, "CODEX_HOME=") {
			continue
		}
		if home != "" {
			return nil, true, fmt.Errorf("duplicate TEST Codex profile")
		}
		home = strings.TrimPrefix(entry, "CODEX_HOME=")
	}
	if home == "" {
		return nil, false, nil // Claude runner, not Codex.
	}
	if !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return nil, true, fmt.Errorf("invalid TEST Codex profile")
	}
	args := argv[1:]
	codexCLIState.Lock()
	defer codexCLIState.Unlock()
	installed := codexCLIState.byProfile[home]
	if installed == nil {
		installed = make(map[string]bool)
	}
	switch {
	case len(args) == 3 && args[0] == "plugin" && args[1] == "list" && args[2] == "--json":
		keys := make([]string, 0, len(installed))
		for spec, present := range installed {
			if present {
				keys = append(keys, spec)
			}
		}
		sort.Strings(keys)
		entries := make([]map[string]any, 0, len(keys))
		for _, spec := range keys {
			name, marketplace, ok := strings.Cut(spec, "@")
			if !ok || name == "" || marketplace == "" {
				return nil, true, fmt.Errorf("invalid TEST Codex registry identity")
			}
			entries = append(entries, map[string]any{
				"pluginId": spec, "name": name, "marketplaceName": marketplace,
				"installed": true, "enabled": true,
			})
		}
		body, err := json.Marshal(map[string]any{"installed": entries})
		return body, true, err
	case len(args) == 5 && args[0] == "plugin" && args[1] == "marketplace" &&
		args[2] == "add" && args[4] == "--json":
		if !filepath.IsAbs(args[3]) || filepath.Clean(args[3]) != args[3] {
			return nil, true, fmt.Errorf("invalid TEST Codex marketplace source")
		}
	case len(args) == 5 && args[0] == "plugin" && args[1] == "marketplace" &&
		(args[2] == "remove" || args[2] == "update") && args[4] == "--json":
		if args[3] == "" || strings.ContainsAny(args[3], "/\\@") {
			return nil, true, fmt.Errorf("invalid TEST Codex marketplace identity")
		}
		if args[2] == "remove" {
			for spec, present := range installed {
				if present && strings.HasSuffix(spec, "@"+args[3]) {
					return nil, true, fmt.Errorf("TEST Codex marketplace still has installed plugin")
				}
			}
		}
	case len(args) == 4 && args[0] == "plugin" &&
		(args[1] == "add" || args[1] == "remove") && args[3] == "--json":
		name, marketplace, ok := strings.Cut(args[2], "@")
		if !ok || name == "" || marketplace == "" || strings.Contains(marketplace, "@") {
			return nil, true, fmt.Errorf("invalid TEST Codex plugin identity")
		}
		installed[args[2]] = args[1] == "add"
		codexCLIState.byProfile[home] = installed
	default:
		return nil, true, fmt.Errorf("unknown TEST Codex CLI command")
	}
	return []byte("{\"ok\":true}"), true, nil
}
