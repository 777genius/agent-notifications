package setupwizard

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
)

const wizardPluginName = "agent-notify"

type codexListStatus int

const (
	codexListUnknown codexListStatus = iota
	codexListAbsent
	codexListPresent
)

// attestCodexExternalUninstall observes `plugin list --json` and, when that
// contract is recognized, runs `plugin remove` for still-present managed
// entries. Unknown output never becomes --external-uninstalled.
func attestCodexExternalUninstall(ctx context.Context, executable, codexHome string) bool {
	if ctx == nil || !explicitAbs(executable) {
		return false
	}
	status, specs := observeCodexPluginList(ctx, executable, codexHome)
	if status == codexListAbsent {
		return true
	}
	if status != codexListPresent {
		return false
	}
	for _, spec := range specs {
		if !removeCodexPlugin(ctx, executable, codexHome, spec) {
			return false
		}
	}
	status, _ = observeCodexPluginList(ctx, executable, codexHome)
	return status == codexListAbsent
}

func observeCodexPluginList(ctx context.Context, executable, codexHome string) (codexListStatus, []string) {
	body, ok := runCodexPluginJSON(ctx, executable, codexHome, "plugin", "list", "--json")
	if !ok {
		return codexListUnknown, nil
	}
	return parseCodexPluginList(body)
}

func removeCodexPlugin(ctx context.Context, executable, codexHome, spec string) bool {
	if spec == "" {
		return false
	}
	body, ok := runCodexPluginJSON(ctx, executable, codexHome, "plugin", "remove", spec, "--json")
	if ok {
		return true
	}
	return strings.Contains(string(body), "not configured or installed")
}

func runCodexPluginJSON(ctx context.Context, executable, codexHome string, args ...string) ([]byte, bool) {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = codexChildEnv(codexHome)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	body := stdout.Bytes()
	if len(body) > 1<<20 {
		return nil, false
	}
	if err != nil {
		return append(body, stderr.Bytes()...), false
	}
	return body, true
}

// codexChildEnv is a dedicated allowlist. It does not inherit the parent
// process environment or call os.Setenv. CODEX_HOME is the explicit profile
// only; HOME is not a fallback.
func codexChildEnv(codexHome string) []string {
	env := make([]string, 0, 10)
	for _, key := range []string{"PATH", "SystemRoot", "WINDIR", "ComSpec", "PATHEXT", "TMP", "TEMP", "TMPDIR"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	if explicitAbs(codexHome) {
		env = append(env, "CODEX_HOME="+codexHome)
	}
	return env
}

func parseCodexPluginList(body []byte) (codexListStatus, []string) {
	if len(body) == 0 {
		return codexListUnknown, nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	var parsed any
	if err := decoder.Decode(&parsed); err != nil {
		return codexListUnknown, nil
	}
	if _, err := decoder.Token(); err != io.EOF {
		return codexListUnknown, nil
	}
	document, ok := parsed.(map[string]any)
	if !ok {
		return codexListUnknown, nil
	}
	installedValue, ok := document["installed"]
	if !ok {
		return codexListUnknown, nil
	}
	entries, ok := installedValue.([]any)
	if !ok {
		return codexListUnknown, nil
	}
	required := []string{"pluginId", "name", "marketplaceName", "installed", "enabled"}
	var specs []string
	identities := map[string]struct{}{}
	for _, value := range entries {
		entry, ok := value.(map[string]any)
		if !ok {
			return codexListUnknown, nil
		}
		for _, field := range required {
			if _, present := entry[field]; !present {
				return codexListUnknown, nil
			}
		}
		pluginID, pluginIDOK := entry["pluginId"].(string)
		name, nameOK := entry["name"].(string)
		marketplace, marketplaceOK := entry["marketplaceName"].(string)
		installed, installedOK := entry["installed"].(bool)
		enabled, enabledOK := entry["enabled"].(bool)
		if !pluginIDOK || !nameOK || !marketplaceOK || !installedOK || !enabledOK ||
			pluginID == "" || name == "" || marketplace == "" ||
			pluginID != name+"@"+marketplace {
			return codexListUnknown, nil
		}
		if _, duplicate := identities[pluginID]; duplicate {
			return codexListUnknown, nil
		}
		identities[pluginID] = struct{}{}
		if name == wizardPluginName && installed && enabled {
			specs = append(specs, pluginID)
		}
	}
	if len(specs) > 0 {
		return codexListPresent, specs
	}
	return codexListAbsent, nil
}
