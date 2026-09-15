package config

import (
	"encoding/json"
	configtemplate "github.com/777genius/agent-notifications/config"
	"os"
	"path/filepath"
)

// ConsumerVersion identifies the template compiled into this binary. The CLI
// reports this same build value; a bundle with another version has no baseline.
var ConsumerVersion = "1.43.0"

// ConsumerContext keeps resource discovery separate from canonical selection.
// A bundle is historical evidence, never a runtime fallback. Without verified
// exact-version provenance its baseline remains unknown.
func ConsumerContext(pluginRoot string) (AssetContext, LegacyContext) {
	assets := AssetContext{PluginRoot: pluginRoot, LookupEnv: os.LookupEnv}
	legacy := LegacyContext{}
	seen := map[string]bool{}
	add := func(p string) {
		if !filepath.IsAbs(p) || seen[p] {
			return
		}
		seen[p] = true
		legacy.Candidates = append(legacy.Candidates, HistoricalCandidate{Path: p})
	}
	if pluginRoot != "" {
		add(filepath.Join(pluginRoot, "config", "config.json"))
		selection, selectionErr := Resolve(SnapshotEnv())
		if selectionErr == nil && !selection.Exists && selection.Source != "explicit" && len(legacy.Candidates) > 0 && currentBundleVersion(pluginRoot) {
			legacy.Candidates[0].TrustedBaseline = configtemplate.Bytes()
		}
	}
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CLAUDE_HOME"} {
		if root := os.Getenv(key); root != "" {
			add(filepath.Join(root, "claude-notifications-go", "config.json"))
		}
	}
	return assets, legacy
}

// ConsumerDiagnostics reports legacy metadata conflicts by names only.
func ConsumerDiagnostics() []Diagnostic {
	a, b := os.Getenv("CLAUDE_CONFIG_DIR"), os.Getenv("CLAUDE_HOME")
	if a != "" && b != "" && a != b {
		return []Diagnostic{{Code: ConfigEnvConflict}}
	}
	return nil
}

// ValidationAssets validates storage structure without requiring the invoking
// shell to possess runtime credentials. The effective value is discarded; raw
// placeholders stay in the document. Hook reads still use ConsumerContext.
func ValidationAssets(pluginRoot string) AssetContext {
	return AssetContext{PluginRoot: pluginRoot, LookupEnv: func(string) (string, bool) { return "CONFIG_ENV_PLACEHOLDER", true }}
}

// currentBundleVersion requires explicit installed version provenance. Matching
// template bytes alone cannot bless a bundle from an unknown/older release.
func currentBundleVersion(root string) bool {
	snapshot, err := ReadFileSnapshot(filepath.Join(root, ".claude-plugin", "plugin.json"), MaxDocumentBytes)
	if err != nil {
		return false
	}
	document, err := ParseDocument(snapshot.Bytes, snapshot.PhysicalPath, true)
	if err != nil {
		return false
	}
	var manifest struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if json.Unmarshal(document.Bytes(), &manifest) != nil {
		return false
	}
	return manifest.Name == "claude-notifications-go" && manifest.Version == ConsumerVersion
}
