package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	configtemplate "github.com/777genius/agent-notifications/config"
)

func TestSeedMatchesShippedEffectiveDefaults(t *testing.T) {
	setTestHome(t, t.TempDir())
	root := filepath.Join(t.TempDir(), "assets")
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, configtemplate.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	shipped, err := load(p, root)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := SeedDocument("/future/config.json")
	if err != nil {
		t.Fatal(err)
	}
	effective, err := seed.Effective(AssetContext{PluginRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(shipped, effective) {
		a, _ := json.Marshal(shipped)
		b, _ := json.Marshal(effective)
		t.Fatalf("seed drift\n%s\n%s", a, b)
	}
	if seed.SchemaVersion() != 1 || !bytes.Contains(seed.Bytes(), []byte(`"schemaVersion": 1`)) {
		t.Fatal("seed schema")
	}
	if !bytes.Contains(configtemplate.Bytes(), []byte("${CLAUDE_PLUGIN_ROOT}")) ||
		bytes.Contains(configtemplate.Bytes(), []byte("${AGENT_NOTIFICATIONS_ROOT}")) ||
		!bytes.Contains(seed.Bytes(), []byte("${CLAUDE_PLUGIN_ROOT}")) ||
		bytes.Contains(seed.Bytes(), []byte("${AGENT_NOTIFICATIONS_ROOT}")) {
		t.Fatal("schema 1 template must retain the legacy asset placeholder")
	}
	if !bytes.HasSuffix(seed.Bytes(), []byte("\n")) || bytes.HasSuffix(seed.Bytes(), []byte("\n\n")) {
		t.Fatal("seed newline")
	}
	// Freeze intentional historical differences instead of changing runtime defaults.
	old := buildDefaultConfig(root)
	if old.Notifications.Webhook.Preset != "custom" || effective.Notifications.Webhook.Preset != "slack" {
		t.Fatal("preset characterization changed")
	}
	for _, k := range []string{"session_limit_reached", "api_error", "api_error_overloaded"} {
		if filepath.Base(old.Statuses[k].Sound) != "error.mp3" || filepath.Base(effective.Statuses[k].Sound) != "question.mp3" {
			t.Fatal("sound characterization changed")
		}
	}
}
func TestEffectivePreservesRawAndHistoricalZero(t *testing.T) {
	raw := []byte(`{"notifications":{"desktop":{"volume":0},"suppressQuestionAfterTaskCompleteSeconds":0,"webhook":{"url":"${SECRET}"}},"statuses":{"question":{"enabled":false,"desktop":{"enabled":false}}},"future":{"n":9007199254740993001}}`)
	d, err := ParseDocument(raw, "/fixture", true)
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.Effective(AssetContext{PluginRoot: "/assets/$literal", LookupEnv: func(k string) (string, bool) { return "synthetic-secret", true }})
	if err != nil {
		t.Fatal(err)
	}
	if c.Notifications.Desktop.Volume != 1 || *c.Notifications.SuppressQuestionAfterTaskCompleteSeconds != 0 {
		t.Fatal("historical zero semantics changed")
	}
	if *c.Statuses["question"].Enabled || *c.Statuses["question"].Desktop.Enabled {
		t.Fatal("pointer semantics changed")
	}
	if !strings.Contains(c.Statuses["question"].Sound, "$literal") {
		t.Fatal("double expansion")
	}
	if !bytes.Equal(raw, d.Bytes()) || bytes.Contains(d.Bytes(), []byte("synthetic-secret")) {
		t.Fatal("persisted expansion")
	}
	out, _ := json.Marshal(InspectDocument(Selection{Path: "/fixture", Exists: true}, "/fixture", raw))
	for _, secret := range []string{"SECRET", "synthetic-secret", "future", "9007199254740993001"} {
		if bytes.Contains(out, []byte(secret)) {
			t.Fatal("diagnostic leaked content")
		}
	}
}
func TestInspectRejectsTypesAndSemanticErrors(t *testing.T) {
	for _, raw := range []string{`{"notifications":{"desktop":{"volume":"secret"}}}`, `{"notifications":{"desktop":{"volume":2}}}`, `{"notifications":{"webhook":{"enabled":true,"preset":"secret"}}}`} {
		out := InspectDocument(Selection{Path: "/fixture", Exists: true}, "/fixture", []byte(raw))
		if out.Valid || out.ErrorCode != ConfigInvalid {
			t.Fatalf("accepted invalid %+v", out)
		}
	}
}

func TestEffectiveNullStatusesUseAssetRoot(t *testing.T) {
	d, err := ParseDocument([]byte(`{"statuses":null}`), "/fixture", true)
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.Effective(AssetContext{PluginRoot: "/assets/$literal"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.Statuses["question"].Sound, "$literal") {
		t.Fatal("null statuses lost asset context")
	}
}

func TestInspectAllowlistOmitsAllFreeFormSecrets(t *testing.T) {
	raw := []byte(`{"notifications":{"desktop":{"appIcon":"SECRET_CANARY"},"webhook":{"url":"SECRET_CANARY","headers":{"SECRET_CANARY":"SECRET_CANARY"},"payloadFields":{"SECRET_CANARY":9007199254740993001}}},"statuses":{"question":{"title":"SECRET_CANARY","sound":"SECRET_CANARY","desktop":{"future":"SECRET_CANARY"}},"SECRET_CANARY":{"title":"SECRET_CANARY"}},"SECRET_CANARY":{"value":"SECRET_CANARY"}}`)
	out := InspectDocument(Selection{Path: "/fixture", Exists: true}, "/fixture", raw)
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Valid || out.Settings == nil {
		t.Fatal("valid inspection unavailable")
	}
	if bytes.Contains(b, []byte("SECRET_CANARY")) {
		t.Fatal("inspection leaked content")
	}
	bad := InspectDocument(Selection{Path: "/fixture", Exists: true}, "/fixture", []byte(`{"SECRET_CANARY":`))
	b, _ = json.Marshal(bad)
	if bad.Valid || bytes.Contains(b, []byte("SECRET_CANARY")) {
		t.Fatal("malformed diagnostic leaked")
	}
	d, err := ParseDocument([]byte(`{"notifications":{"desktop":{"volume":2}}}`), "/fixture", true)
	if err != nil {
		t.Fatal(err)
	}
	if d.ValidateEditable() == nil {
		t.Fatal("semantic-invalid edit accepted")
	}
}
