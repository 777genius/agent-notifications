package config

import (
	"os"
	"path/filepath"
	"testing"
)

func loadTemplateConfig(t *testing.T, raw string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestDesktopTemplatesLoadAndResolve(t *testing.T) {
	cfg := loadTemplateConfig(t, `{
		"notifications": {"desktop": {
			"titleTemplate": "{statusTitle} {repo}",
			"headerTemplate": "{branch} · {sessionName}",
			"bodyTemplate": "{header}\n{body}"
		}},
		"statuses": {"question": {"titleTemplate": "{statusTitle} {repo}: {question}", "headerTemplate": ""}}
	}`)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	// A per-status template must not reset the status's shipped title/sound.
	q := cfg.Statuses["question"]
	if q.Title != "❓ Question" || q.Sound == "" {
		t.Fatalf("per-status template dropped defaults: %+v", q)
	}
	got := cfg.DesktopTemplatesFor("question")
	if *got.Title != "{statusTitle} {repo}: {question}" || *got.Header != "" || *got.Body != "{header}\n{body}" {
		t.Fatalf("question templates = %q %q %q", *got.Title, *got.Header, *got.Body)
	}
	got = cfg.DesktopTemplatesFor("task_complete")
	if *got.Title != "{statusTitle} {repo}" || *got.Header != "{branch} · {sessionName}" {
		t.Fatalf("global templates = %q %q", *got.Title, *got.Header)
	}
	if !got.References("repo") || got.References("agent") {
		t.Fatal("References mismatch")
	}
	if !cfg.AnyDesktopTemplateReferences("question") || cfg.AnyDesktopTemplateReferences("sessionId") {
		t.Fatal("AnyDesktopTemplateReferences mismatch")
	}
}

func TestDesktopTemplatesDefaultsUnset(t *testing.T) {
	cfg := DefaultConfig()
	for status := range cfg.Statuses {
		if cfg.DesktopTemplatesFor(status).Any() {
			t.Fatalf("shipped defaults configure a template for %s", status)
		}
	}
	if cfg.AnyDesktopTemplateReferences("sessionName") {
		t.Fatal("shipped defaults reference a placeholder")
	}
}

// Unknown placeholders are rejected rather than shown literally, so a typo
// such as {sessionID} fails loudly at validation instead of in a banner.
func TestDesktopTemplatesValidation(t *testing.T) {
	for _, tc := range []struct{ raw, wantErr string }{
		{`{"notifications":{"desktop":{"titleTemplate":"{statusTitle} {sessionID}"}}}`, "notifications.desktop.titleTemplate: unknown placeholder {sessionID}"},
		{`{"notifications":{"desktop":{"headerTemplate":"{header}"}}}`, "notifications.desktop.headerTemplate: placeholder {header} is only valid in bodyTemplate"},
		{`{"statuses":{"plan_ready":{"bodyTemplate":"{plan}"}}}`, "statuses.plan_ready.bodyTemplate: unknown placeholder {plan}"},
		{`{"statuses":{"question":{"titleTemplate":"{header}"}}}`, "statuses.question.titleTemplate: placeholder {header} is only valid in bodyTemplate"},
	} {
		err := loadTemplateConfig(t, tc.raw).Validate()
		if err == nil || err.Error() != tc.wantErr {
			t.Errorf("%s: err = %v, want %q", tc.raw, err, tc.wantErr)
		}
	}
	ok := loadTemplateConfig(t, `{"notifications":{"desktop":{"titleTemplate":"{ literal } {agent}"}}}`)
	if err := ok.Validate(); err != nil {
		t.Fatalf("literal braces rejected: %v", err)
	}
}

func TestDesktopTemplatesInvalidRejectedByEffective(t *testing.T) {
	d, err := ParseDocument([]byte(`{"notifications":{"desktop":{"titleTemplate":"{nope}"}}}`), "/fixture", true)
	if err != nil {
		t.Fatal(err)
	}
	if d.ValidateEditable() == nil {
		t.Fatal("invalid template accepted")
	}
	good, err := ParseDocument([]byte(`{"notifications":{"desktop":{"titleTemplate":"{repo}"}}}`), "/fixture", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := good.ValidateEditable(); err != nil {
		t.Fatalf("valid template rejected: %v", err)
	}
}
