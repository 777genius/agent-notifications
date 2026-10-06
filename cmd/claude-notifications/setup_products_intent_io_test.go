package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bootstrapCodecFixture(t *testing.T) (confirmedBootstrapIntent, string) {
	t.Helper()
	stage := t.TempDir()
	if err := os.Chmod(stage, 0700); err != nil {
		t.Fatal(err)
	}
	physical, err := filepath.EvalSymlinks(stage)
	if err != nil {
		t.Fatal(err)
	}
	r, err := parseSetupProducts([]string{"confirm", "--products", "gemini", "--webhook"})
	if err != nil {
		t.Fatal(err)
	}
	r.Mode = ""
	r.Scopes = nil
	r.ConfigureArgs = nil
	i := confirmedBootstrapIntent{Schema: 1, Request: r, Provenance: selectorProvenance{Version: "TEST", SourceCommit: strings.Repeat("1", 40), SHA256: strings.Repeat("2", 64), Stage: []byte(physical)},
		Scopes: map[string][]byte{"home": []byte(physical), "control-root": []byte(filepath.Join(physical, "control")), "global-config": []byte(filepath.Join(physical, "config.json")), "gemini-config-root": []byte(filepath.Join(physical, "gemini")), "gemini-executable": []byte(filepath.Join(physical, "TEST-gemini"))},
		Units:  []bootstrapProductUnits{{Product: "gemini", Native: true, Webhook: true}}}
	return i, filepath.Join(physical, "selector-intent.json")
}

// Regression: confirmed Cursor profile/agent is lost in the real immutable
// codec, or a caller substitutes a different profile at wizard admission.
func TestSetupProductsCursorIntentRoundTrip(t *testing.T) {
	i, path := bootstrapCodecFixture(t)
	r, err := parseSetupProducts([]string{"confirm", "--products", "cursor", "--agent-notify", "--desktop", "--scope-root", string(i.Provenance.Stage), "--client-executable", filepath.Join(string(i.Provenance.Stage), "TEST-agent")})
	if err != nil {
		t.Fatal(err)
	}
	r.Mode, r.Scopes, r.ConfigureArgs = "", nil, nil
	i.Request = r
	delete(i.Scopes, "gemini-config-root")
	delete(i.Scopes, "gemini-executable")
	i.Scopes["scope-root"] = i.Provenance.Stage
	i.Scopes["client-executable"] = []byte(filepath.Join(string(i.Provenance.Stage), "TEST-agent"))
	i.MCP.Selected = []string{"cursor"}
	i.MCP.Projection.Profiles = map[string][]byte{"cursor": i.Provenance.Stage}
	i.Units = []bootstrapProductUnits{{Product: "cursor", MCP: true, Skill: true, Desktop: true}}
	if err := writeBootstrapIntent(path, i); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadBootstrapIntent(path, i.Provenance)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Request.Desktop || loaded.Request.Webhook || !loaded.Units[0].Desktop || loaded.Units[0].Webhook {
		t.Fatal("desktop-only frozen choice broadened or lost")
	}
	for _, mutate := range []func(*confirmedBootstrapIntent){
		func(b *confirmedBootstrapIntent) { b.Request.Webhook = true },
		func(b *confirmedBootstrapIntent) { b.Units[0].Desktop = false },
		func(b *confirmedBootstrapIntent) { b.Units[0].Webhook = true },
	} {
		bad := loaded
		bad.Units = append([]bootstrapProductUnits(nil), loaded.Units...)
		mutate(&bad)
		if validateBootstrapIntent(bad) == nil {
			t.Fatal("channel tamper admitted")
		}
	}
	want := intentWizardRequest(loaded)
	if strings.Join(want.Agents, ",") != "cursor" || want.CursorConfig != string(i.Provenance.Stage) || want.ScopeRoot != want.CursorConfig || want.ClientExecutables["cursor"] != string(i.Scopes["client-executable"]) || want.CursorAuthority != nil {
		t.Fatalf("frozen request: %+v", want)
	}
	actual := want
	actual.CursorConfig = filepath.Join(want.CursorConfig, "replacement")
	if sameWizardBootstrapScope(actual, want) {
		t.Fatal("profile substitution admitted")
	}
}

// Red regression: an accepted record overwrites another journey or loses raw
// authority bytes; the getter observes a different destination on a later run.
func TestSetupProductsImmutableIntentCodec(t *testing.T) {
	i, path := bootstrapCodecFixture(t)
	if os.PathSeparator == '/' {
		i.Scopes["gemini-config-root"] = []byte(filepath.Join(string(i.Provenance.Stage), "raw-\xff-\u202e-end"))
	}
	if err := writeBootstrapIntent(path, i); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loadBootstrapIntent(path, i.Provenance)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded.Scopes["gemini-config-root"], i.Scopes["gemini-config-root"]) {
		t.Fatal("raw path bytes changed")
	}
	i.Scopes["gemini-config-root"] = []byte(filepath.Join(string(i.Provenance.Stage), "other"))
	if err := writeBootstrapIntent(path, i); err == nil {
		t.Fatal("immutable intent overwritten")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("record changed: %v", err)
	}
	var values bytes.Buffer
	if err := writeIntentScalars(&values, loaded); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(values.Bytes(), append(append([]byte("gemini-config-root\x00"), loaded.Scopes["gemini-config-root"]...), 0)) {
		t.Fatalf("getter lost raw scalar: %q", values.Bytes())
	}
}

// Red regression: bounded loading accepts symlink/unknown/duplicate/trailing
// JSON or an oversized file, making a private stage into an executable protocol.
func TestSetupProductsIntentRejectsUntrustedRecord(t *testing.T) {
	for _, name := range []string{"unknown-field", "duplicate-key", "trailing-json", "oversize", "unknown-scalar", "different-helper", "symlink"} {
		t.Run(name, func(t *testing.T) {
			i, path := bootstrapCodecFixture(t)
			if err := writeBootstrapIntent(path, i); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			expected := i.Provenance
			switch name {
			case "unknown-field":
				data = append([]byte(`{"extra":true,`), data[1:]...)
			case "duplicate-key":
				data = append([]byte(`{"schema":1,`), data[1:]...)
			case "trailing-json":
				data = append(data, []byte(`{}`)...)
			case "oversize":
				data = bytes.Repeat([]byte("x"), maxBootstrapIntent+1)
			case "unknown-scalar":
				i.Scopes["execute"] = []byte(filepath.Join(string(i.Provenance.Stage), "payload"))
				data, err = json.Marshal(i)
				if err != nil {
					t.Fatal(err)
				}
			case "different-helper":
				expected.SHA256 = strings.Repeat("3", 64)
			case "symlink":
				target := path + ".target"
				if err := os.Rename(path, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			if name != "symlink" {
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := loadBootstrapIntent(path, expected); err == nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}
}
