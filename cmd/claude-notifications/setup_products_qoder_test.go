package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/config"
)

// Qoder must be selectable from its CLI alone, including unqualified versions;
// missing CLI stays an offered nondefault choice and no detector runs the CLI.
func TestSetupProductsQoderPresence(t *testing.T) {
	e, a := setupProductsDiscoveryFixture(t)
	root, err := filepath.EvalSymlinks(e.Home)
	if err != nil {
		t.Fatal(err)
	}
	e.Home, e.PATH, e.DefaultControlRoot = root, filepath.Join(root, "TEST-PATH"), filepath.Join(root, "control")
	e.Values["QODER_CONFIG_DIR"] = filepath.Join(root, "TEST-env-qoder")
	for _, present := range []bool{false, true} {
		if present {
			// Deliberately unusable program content detects accidental execution.
			if err := os.WriteFile(filepath.Join(e.PATH, fixtureProductName("qoder")), []byte("TEST never execute or version-probe"), 0700); err != nil {
				t.Fatal(err)
			}
		}
		facts, scopes, err := discoverProducts(context.Background(), a, e)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, f := range facts {
			if f.ID != "qoder" {
				continue
			}
			found = true
			supported := (runtime.GOOS == "darwin" || runtime.GOOS == "linux" || runtime.GOOS == "windows") && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64") && !(runtime.GOOS == "windows" && runtime.GOARCH == "arm64")
			if f.Label != "Qoder" || f.Present != present || f.Selectable != supported || f.Profile != e.Values["QODER_CONFIG_DIR"] || scopes["qoder-config-root"] != f.Profile || present && f.Executable != filepath.Join(e.PATH, fixtureProductName("qoder")) {
				t.Fatalf("Qoder presence/default authority: %+v %v", f, scopes)
			}
		}
		if !found {
			t.Fatal("Qoder omitted from product discovery")
		}
	}
	// Official archives use qodercli while package-manager installs expose qoder.
	qoderPath := filepath.Join(e.PATH, fixtureProductName("qoder"))
	aliasPath := filepath.Join(e.PATH, fixtureProductName("qodercli"))
	if err := os.Rename(qoderPath, aliasPath); err != nil {
		t.Fatal(err)
	}
	facts, scopes, err := discoverProducts(context.Background(), a, e)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range facts {
		if f.ID == "qoder" && (!f.Present || f.Executable != aliasPath || scopes["qoder-executable"] != aliasPath) {
			t.Fatalf("official qodercli alias missed: %+v %v", f, scopes)
		}
	}
	// An unusable explicit path cannot silently substitute the installed alias.
	a.Scopes["qoder-executable"] = filepath.Join(root, "TEST-missing-qoder")
	if _, _, err := discoverProducts(context.Background(), a, e); err == nil {
		t.Fatal("invalid explicit Qoder executable fell back to qodercli")
	}
	delete(a.Scopes, "qoder-executable")
	a.Scopes["qoder-config-root"] = filepath.Join(root, "TEST-explicit-qoder")
	scopes, err = productScopes(a, e)
	if err != nil || scopes["qoder-config-root"] != a.Scopes["qoder-config-root"] {
		t.Fatalf("explicit profile lost to environment: %v %v", scopes, err)
	}
	a.Scopes = nil
	e.Values["QODER_CONFIG_DIR"] = "relative"
	if _, err := productScopes(a, e); err == nil {
		t.Fatal("relative Qoder authority fell back to default")
	}
}

// The real confirmation/codec/preflight boundary must retain Qoder's authority
// and channel consent without granting an MCP unit or rereading ambient paths.
func TestSetupProductsQoderFrozenNativeIntent(t *testing.T) {
	i, intentPath := bootstrapCodecFixture(t)
	root := string(i.Provenance.Stage)
	cli := filepath.Join(root, fixtureProductName("qoder"))
	if err := os.WriteFile(cli, []byte("TEST never execute or version-probe"), 0700); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(root, "TEST-qoder-profile")
	a, err := parseSetupProducts([]string{"confirm", "--products", "qoder", "--qoder-config-root", profile, "--qoder-executable", cli, "--desktop=false", "--webhook", "--control-root", filepath.Join(root, "control")})
	if err != nil {
		t.Fatal(err)
	}
	e := productEnvironment{Home: root, DefaultControlRoot: a.Scopes["control-root"], Values: map[string]string{}, Config: config.EnvSnapshot{GOOS: runtime.GOOS, Vars: map[string]string{config.OverrideEnv: filepath.Join(root, "TEST-notifications.json")}, Lstat: os.Lstat, ReadDir: os.ReadDir}}
	i, rows, err := buildConfirmedBootstrapIntent(context.Background(), a, e, i.Provenance)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeBootstrapIntent(intentPath, i); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadBootstrapIntent(intentPath, i.Provenance)
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded.Scopes["qoder-config-root"]) != profile || string(loaded.Scopes["qoder-executable"]) != cli || !reflect.DeepEqual(loaded.Units, []bootstrapProductUnits{{Product: "qoder", Native: true, Webhook: true}}) || len(loaded.MCP.Selected)+len(loaded.MCP.Skipped)+len(intentWizardRequest(loaded).Agents) != 0 {
		t.Fatalf("Qoder gained MCP authority or lost frozen scope: %+v", loaded)
	}
	if !strings.Contains(strings.Join(rows, "\n"), "Qoder: native plugin/hooks") || !strings.Contains(strings.Join(rows, "\n"), "Channels: Desktop off, Webhook on") {
		t.Fatalf("summary omitted Qoder native consent: %v", rows)
	}
	var scalars bytes.Buffer
	if err := writeIntentScalars(&scalars, loaded); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"qoder-config-root\x00" + profile + "\x00", "qoder-executable\x00" + cli + "\x00", "qoder-channel-preimage\x00", "qoder-desktop\x00false\x00qoder-webhook\x00true\x00"} {
		if !strings.Contains(scalars.String(), want) {
			t.Fatalf("native dispatch lost %q: %q", want, scalars.String())
		}
	}
	t.Setenv("QODER_CONFIG_DIR", filepath.Join(root, "TEST-changed-env"))
	t.Setenv("PATH", filepath.Join(root, "TEST-changed-PATH"))
	preflight, err := parseSetupProducts([]string{"preflight", "--intent-file", intentPath, "--products", "qoder", "--desktop=false", "--webhook"})
	if err != nil {
		t.Fatal(err)
	}
	if err := preflightBootstrapIntent(context.Background(), loaded, preflight); err != nil {
		t.Fatalf("unchanged frozen Qoder scope rejected: %v", err)
	}
	if err := os.Remove(cli); err != nil {
		t.Fatal(err)
	}
	if err := preflightBootstrapIntent(context.Background(), loaded, preflight); err == nil || !strings.Contains(err.Error(), "qoder selected executable changed") {
		t.Fatalf("changed Qoder executable admitted: %v", err)
	}
}
