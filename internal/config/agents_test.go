package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentProfilesMergeAndRawPreservation(t *testing.T) {
	raw := []byte(`{"schemaVersion":2,"future":{"n":9007199254740993},"notifications":{"desktop":{"volume":0.8},"webhook":{"headers":{"Authorization":"secret"},"payloadFields":{"token":"secret"}},"suppressFilters":[{"folder":"x"}]},"statuses":{"question":{"title":"global"}},"agents":{"claude":{"notifications":{"desktop":{"volume":0.5}}},"codex":{"notifications":{"desktop":{"sound":false,"volume":0,"appIcon":""},"webhook":{"headers":{},"payloadFields":{}},"suppressFilters":[]},"statuses":{"question":{"title":""}}},"future-agent":{"debug":{"benchmark":true}}}}`)
	d, err := ParseDocument(raw, "/test", true)
	if err != nil {
		t.Fatal(err)
	}
	claude, err := d.Effective(AssetContext{Agent: AgentClaude})
	if err != nil {
		t.Fatal(err)
	}
	codex, err := d.Effective(AssetContext{Agent: AgentCodex})
	if err != nil {
		t.Fatal(err)
	}
	if claude.Notifications.Desktop.Volume != .5 || len(claude.Notifications.Webhook.Headers) != 1 {
		t.Fatal("claude inheritance")
	}
	if codex.Notifications.Desktop.Volume != 0 || codex.Notifications.Desktop.Sound || codex.Notifications.Desktop.AppIcon != "" || len(codex.Notifications.Webhook.Headers) != 0 || len(codex.Notifications.Webhook.PayloadFields) != 0 || len(codex.Notifications.SuppressFilters) != 0 || codex.Statuses["question"].Title != "" {
		t.Fatalf("codex merge: %+v", codex)
	}
	if !bytes.Equal(raw, d.Bytes()) {
		t.Fatal("runtime mutated source")
	}
	next, err := ApplyRawEdits(d, Edits{Set: map[string]json.RawMessage{"/debug/benchmark": json.RawMessage(`true`)}}, ValidationAssets(""))
	if err != nil {
		t.Fatal(err)
	}
	if !sameJSON(d.Raw()["agents"], next.Raw()["agents"]) || !sameJSON(d.Raw()["future"], next.Raw()["future"]) {
		t.Fatal("edit lost unknown data")
	}
	if _, err := ApplyRawEdits(d, Edits{Set: map[string]json.RawMessage{"/agents/codex/debug/benchmark": json.RawMessage(`true`)}}, AssetContext{}); err == nil {
		t.Fatal("agent edit allowed")
	}
}

func TestAgentStructureRejected(t *testing.T) {
	for _, raw := range []string{
		`{"agents":{}}`, `{"schemaVersion":1,"agents":{}}`,
		`{"schemaVersion":2,"agents":null}`, `{"schemaVersion":2,"agents":[]}`,
		`{"schemaVersion":2,"agents":{"Bad":{}}}`, `{"schemaVersion":2,"agents":{"a":{"schemaVersion":2}}}`,
		`{"schemaVersion":2,"agents":{"a":{"agents":{}}}}`, `{"schemaVersion":2,"agents":{"a":{"future":true}}}`,
		`{"schemaVersion":2,"agents":{"a":{"notifications":{"desktop":{"volume":null}}}}}`,
		`{"schemaVersion":2,"agents":{"a":{"notifications":{"webhook":{"payloadFields":{"x":[null]}}}}}}`,
		`{"schemaVersion":2,"agents":{"a":{"debug":{"benchmark":"yes"}}}}`,
		`{"schemaVersion":2,"agents":{"a":{"debug":{"benchmark":true,"Benchmark":false}}}}`,
		`{"schemaVersion":2,"agents":{},"Agents":{}}`,
		`{"schemaVersion":2,"notifications":{"desktop":{"volume":1,"Volume":0}}}`,
	} {
		if _, err := ParseDocument([]byte(raw), "/test", true); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

func TestAgentValidationAllProfilesWithoutForeignSecrets(t *testing.T) {
	raw := `{"schemaVersion":2,"agents":{"claude":{"notifications":{"webhook":{"enabled":true,"url":"${CLAUDE_ONLY_SECRET}"}}}}}`
	d, err := ParseDocument([]byte(raw), "/test", true)
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(string) (string, bool) { return "", false }
	if _, err = d.Effective(AssetContext{Agent: AgentCodex, LookupEnv: lookup}); err != nil {
		t.Fatal("foreign secret required", err)
	}
	if _, err = d.Effective(AssetContext{Agent: AgentClaude, LookupEnv: lookup}); err == nil {
		t.Fatal("missing active secret accepted")
	}
	d, err = ParseDocument([]byte(`{"schemaVersion":2,"agents":{"claude":{"notifications":{"desktop":{"volume":3}}}}}`), "/test", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Effective(AssetContext{Agent: AgentCodex}); err == nil {
		t.Fatal("invalid inactive profile accepted")
	}
}

func TestSchemaTwoPresenceAndKnownCasing(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		volume float64
	}{
		{`{"schemaVersion":2}`, 1}, {`{"schemaVersion":2,"notifications":{"desktop":{"volume":0}}}`, 0},
		{`{"schemaVersion":1,"notifications":{"desktop":{"volume":0}}}`, 1},
		{`{"schemaVersion":2,"Notifications":{"Desktop":{"Volume":0.7}},"Agents":{"codex":{"Notifications":{"Desktop":{"Volume":0}}}}}`, 0},
	} {
		d, err := ParseDocument([]byte(tc.raw), "/test", true)
		if err != nil {
			t.Fatal(err)
		}
		c, err := d.Effective(AssetContext{Agent: AgentCodex})
		if err != nil || c.Notifications.Desktop.Volume != tc.volume {
			t.Fatalf("%s: %+v %v", tc.raw, c, err)
		}
	}
}

func TestAssetAliasesSingleExpansion(t *testing.T) {
	for _, root := range []string{"/assets/space Unicode 日本/$literal", `C:\space 日本\$literal`} {
		for _, explicit := range []bool{false, true} {
			a := AssetContext{LookupEnv: func(k string) (string, bool) {
				if k == AssetRootPlaceholder {
					return root, true
				}
				if k == LegacyAssetRootPlaceholder {
					return "/wrong", true
				}
				return "expanded", true
			}}
			if explicit {
				a.PluginRoot = root
				a.LookupEnv = func(string) (string, bool) { return "/wrong", true }
			}
			d, err := ParseDocument([]byte(`{"notifications":{"desktop":{"appIcon":"${AGENT_NOTIFICATIONS_ROOT}/icon.png"}},"statuses":{"question":{"sound":"${CLAUDE_PLUGIN_ROOT}/icon.png"}}}`), "/test", true)
			if err != nil {
				t.Fatal(err)
			}
			c, err := d.Effective(a)
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Clean(root + "/icon.png")
			if c.Notifications.Desktop.AppIcon != want || c.Statuses["question"].Sound != want {
				t.Fatalf("double expansion or alias mismatch: %+v", c)
			}
		}
	}
}

func TestFutureAgentValidatedButNotApplied(t *testing.T) {
	for _, volume := range []string{"0", "3"} {
		d, err := ParseDocument([]byte(`{"schemaVersion":2,"agents":{"future":{"notifications":{"desktop":{"volume":`+volume+`}}}}}`), "/test", true)
		if err != nil {
			t.Fatal(err)
		}
		c, err := d.Effective(AssetContext{Agent: AgentID("future")})
		if volume == "3" {
			if err == nil {
				t.Fatal("invalid future profile accepted")
			}
			continue
		}
		if err != nil || c.Notifications.Desktop.Volume != 1 {
			t.Fatal("unknown runtime applied profile", err)
		}
	}
}

func TestPreparedProfilesExcludeMetadataAndIsolateMerges(t *testing.T) {
	profiles := make(map[string]any, 300)
	for i := 0; i < 300; i++ {
		profiles[fmt.Sprintf("agent-%03d", i)] = map[string]any{"notifications": map[string]any{"desktop": map[string]any{"volume": float64(i%10) / 10}}}
	}
	raw, err := json.Marshal(map[string]any{
		"schemaVersion": 2, "agents": profiles,
		"futureMetadata": strings.Repeat("large unknown metadata", 16000),
		"notifications":  map[string]any{"futureMetadata": strings.Repeat("nested unknown", 16000)},
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err := ParseDocument(raw, "/test", true)
	if err != nil {
		t.Fatal(err)
	}
	prepared := d.prepareProfiles()
	global, err := prepared.mergedProfile("")
	if err != nil {
		t.Fatal(err)
	}
	if len(global) > 10000 || bytes.Contains(global, []byte("futureMetadata")) || bytes.Contains(global, []byte(`"agents"`)) || bytes.Contains(global, []byte(`"schemaVersion"`)) {
		t.Fatal("runtime body retains document metadata")
	}
	if len(prepared.agents) != len(profiles) {
		t.Fatal("lost stored profiles")
	}
	// Cached profiles remain usable without the original bytes. No profile merge
	// can reparse the full document, and view mutations cannot pollute the cache.
	saved := d.original
	d.original = nil
	for _, id := range prepared.validationAgents() {
		body, err := prepared.mergedProfile(id)
		if err != nil {
			t.Fatal(err)
		}
		if len(body) > 10000 || bytes.Contains(body, []byte("futureMetadata")) {
			t.Fatal("per-profile work includes metadata")
		}
	}
	after, err := prepared.mergedProfile("")
	if err != nil || !bytes.Equal(global, after) {
		t.Fatal("profile merges mutated shared global")
	}
	d.original = saved
	if !bytes.Equal(raw, d.Bytes()) {
		t.Fatal("preparation rewrote raw document")
	}
	if _, err := d.Effective(AssetContext{Agent: AgentCodex}); err != nil {
		t.Fatal(err)
	}
}
