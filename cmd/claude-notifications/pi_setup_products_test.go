package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
)

func TestPiUnselectedProfileCannotBlockOtherProducts(t *testing.T) {
	e, a := setupProductsDiscoveryFixture(t)
	invalid := filepath.Join(e.Home, "TEST-foreign-pi-file")
	if err := os.WriteFile(invalid, []byte("TEST foreign user bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	e.Values["PI_CODING_AGENT_DIR"] = invalid
	a.Products = []string{"claude"}
	scopes, err := productScopes(a, e)
	if err != nil || scopes["claude-config"] == "" {
		t.Fatalf("unselected Pi state blocked Claude: %v", err)
	}
	facts, _, err := discoverProducts(context.Background(), a, e)
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range facts {
		if fact.ID == "pi" && (fact.Selectable || fact.Reason == "") {
			t.Fatal("invalid Pi profile did not remain an unavailable row")
		}
	}
	a.Products = []string{"pi"}
	if _, err := productScopes(a, e); err == nil {
		t.Fatal("selected invalid Pi authority admitted")
	}
	if body, err := os.ReadFile(invalid); err != nil || string(body) != "TEST foreign user bytes" {
		t.Fatal("foreign Pi bytes changed", err)
	}
}

func TestPiProductDiscoveryUsesNativeEnvironmentProfile(t *testing.T) {
	e, a := setupProductsDiscoveryFixture(t)
	physical, err := filepath.EvalSymlinks(e.Home)
	if err != nil {
		t.Fatal(err)
	}
	e.Home, e.WorkingDir = physical, physical
	e.PATH = filepath.Join(physical, "TEST-PATH")
	cli := filepath.Join(e.PATH, fixtureProductName("pi"))
	if err := os.WriteFile(cli, []byte("TEST never execute or probe"), 0700); err != nil {
		t.Fatal(err)
	}
	e.Values["PI_CODING_AGENT_DIR"] = "TEST-relative-pi"
	a.Products = []string{"pi"}
	facts, scopes, err := discoverProducts(context.Background(), a, e)
	if err != nil {
		t.Fatal(err)
	}
	wanted := filepath.Join(physical, "TEST-relative-pi")
	if scopes["pi-agent-dir"] != wanted {
		t.Fatalf("relative native Pi profile lost: %v", scopes)
	}
	found := false
	for _, f := range facts {
		if f.ID == "pi" {
			found = true
			if !f.Present || !f.Selectable || f.Profile != wanted || f.Executable != cli {
				t.Fatalf("Pi discovery: %+v", f)
			}
		}
	}
	if !found {
		t.Fatal("Pi omitted")
	}
	a.Scopes["pi-agent-dir"] = filepath.Join(physical, "TEST-explicit-pi")
	scopes, err = productScopes(a, e)
	if err != nil || scopes["pi-agent-dir"] != a.Scopes["pi-agent-dir"] {
		t.Fatal("explicit profile lost to environment", err)
	}
	if _, err := os.Stat(wanted); !os.IsNotExist(err) {
		t.Fatal("read-only detection created Pi profile", err)
	}
}

func TestPiFrozenIntentRetainsNativeScopeAndConsent(t *testing.T) {
	i, path := bootstrapCodecFixture(t)
	r, err := parseSetupProducts([]string{"confirm", "--products", "pi", "--webhook"})
	if err != nil {
		t.Fatal(err)
	}
	r.Mode, r.Scopes, r.ConfigureArgs = "", nil, nil
	i.Request = r
	delete(i.Scopes, "gemini-config-root")
	delete(i.Scopes, "gemini-executable")
	i.Scopes["pi-agent-dir"] = []byte(filepath.Join(string(i.Provenance.Stage), "TEST-pi-agent"))
	i.Scopes["pi-executable"] = []byte(filepath.Join(string(i.Provenance.Stage), fixtureProductName("pi")))
	i.MCP.Selected = []string{"pi"}
	i.MCP.Projection.Profiles = map[string][]byte{"pi": i.Scopes["pi-agent-dir"]}
	i.Units = []bootstrapProductUnits{{Product: "pi", Native: true, Webhook: true}}
	if err := writeBootstrapIntent(path, i); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadBootstrapIntent(path, i.Provenance)
	if err != nil {
		t.Fatal(err)
	}
	want := intentWizardRequest(loaded)
	if strings.Join(want.Agents, ",") != "pi" || want.PiAgentDir != string(i.Scopes["pi-agent-dir"]) || want.PiDesktop == nil || *want.PiDesktop || want.PiWebhook == nil || !*want.PiWebhook || want.PiChannelPreimage != "absent" || want.ClaudeConfig != "" || want.CodexHome != "" || len(want.MCPConfig) != 0 {
		t.Fatalf("Pi intent changed: %+v", want)
	}
	if !sameWizardBootstrapScope(want, want) {
		t.Fatal("exact Pi scope rejected")
	}
	changed := want
	off := false
	changed.PiWebhook = &off
	if sameWizardBootstrapScope(changed, want) {
		t.Fatal("changed Pi channel admitted")
	}
	changed = want
	changed.PiAgentDir += "-other"
	if sameWizardBootstrapScope(changed, want) {
		t.Fatal("changed Pi profile admitted")
	}
	var values bytes.Buffer
	if err := writeIntentScalars(&values, loaded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"pi-agent-dir\x00", "pi-executable\x00", "pi-desktop\x00false\x00pi-webhook\x00true\x00", "pi-channel-preimage\x00absent\x00"} {
		if !bytes.Contains(values.Bytes(), []byte(key)) {
			t.Fatalf("frozen Pi scalar missing: %q", key)
		}
	}
}

func TestPiInstallerResultUsesNativeRegistrationPhase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	body, err := json.Marshal(setupwizard.Result{Action: "install", Outcome: "completed", Targets: []setupwizard.TargetResult{{Client: "pi", Unit: "agent-notify", Outcome: "completed"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := writeInstallerResult(path, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "pi\tregistration\tcompleted\n") || strings.Contains(out.String(), "MCP") {
		t.Fatalf("Pi presented as MCP: %q", out.String())
	}
}
