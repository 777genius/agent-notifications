package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/testenv"
)

func TestConfigRejectsUnsafeInputWithoutDisclosure(t *testing.T) {
	const secret = "CANARY_nested_URL_header_payload"
	cases := []struct {
		args  []string
		input string
	}{
		{[]string{"path", "--" + secret}, ""},
		{[]string{"inspect", "--json", "--json"}, ""},
		{[]string{"edit", "--stdin"}, `{}`},
		{[]string{"edit", "--stdin", "--expect-revision", "r"}, `{"set":{},"set":{"/` + secret + `":true}}`},
		{[]string{"edit", "--stdin", "--expect-revision", "r"}, `{"` + secret + `":true}`},
		{[]string{"edit", "--stdin", "--expect-revision", "r"}, `{"set":{"":{}}}`},
		{[]string{"edit", "--stdin", "--expect-revision", "r"}, `{"set":{"/statuses/idle/sound":"` + secret},
		{[]string{"preflight-update", "--stdin", "--json"}, `{"historicalCandidates":[{"path":"/tmp/example","` + secret + `":true}]}`},
		{[]string{"init", "--from", secret}, ""},
		{[]string{"edit", "--stdin", "--expect-revision", "r"}, strings.Repeat(" ", 4<<20) + secret},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			testenv.Set(t, t.TempDir())
			var out, stderr bytes.Buffer
			if configCommand(tc.args, strings.NewReader(tc.input), &out, &stderr) == 0 {
				t.Fatal("accepted invalid request")
			}
			if strings.Contains(out.String()+stderr.String(), secret) {
				t.Fatal("disclosed input")
			}
			if !strings.HasPrefix(stderr.String(), "Config") {
				t.Fatalf("unsafe error: %q", stderr.String())
			}
		})
	}
}

func TestConfigPreflightAcceptsProtectedPaths(t *testing.T) {
	testenv.Set(t, t.TempDir())
	protected := filepath.Join(t.TempDir(), "installed_plugins.json")
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", protected)
	input, err := json.Marshal(map[string]any{"protectedPaths": []string{protected}})
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := configCommand([]string{"preflight-update", "--stdin", "--json"}, bytes.NewReader(input), &out, &stderr); code == 0 {
		t.Fatal("protected registration path was accepted as configuration")
	}
	if !strings.Contains(out.String()+stderr.String(), "ConfigUnsafeTarget") {
		t.Fatalf("field was not passed to preflight: stdout=%q stderr=%q", out.String(), stderr.String())
	}
}

func TestConfigInspectAndEditPreserveUnexpandedSecrets(t *testing.T) {
	testenv.Set(t, t.TempDir())
	canonical := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", canonical)
	const canary = "CANARY_unknown_header_payload_URL"
	raw := `{"notifications":{"webhook":{"enabled":true,"url":"${UNSET_SECRET_URL}","headers":{"CANARY_unknown_header_payload_URL":"secret"},"payloadFields":{"unknown":"CANARY_unknown_header_payload_URL"}}},"future":{"CANARY_unknown_header_payload_URL":9007199254740993}}`
	if err := os.WriteFile(canonical, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := configCommand([]string{"inspect", "--json"}, strings.NewReader(""), &out, &stderr); code != 0 {
		t.Fatalf("inspect: %s", stderr.String())
	}
	if strings.Contains(out.String()+stderr.String(), canary) || strings.Contains(out.String(), "UNSET_SECRET_URL") {
		t.Fatal("inspect disclosed data")
	}
	var inspection struct {
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal(out.Bytes(), &inspection); err != nil || inspection.Revision == "" {
		t.Fatal("missing revision")
	}
	out.Reset()
	stderr.Reset()
	if code := configCommand([]string{"edit", "--stdin", "--expect-revision", inspection.Revision}, strings.NewReader(`{"set":{"/notifications/desktop/volume":0.25}}`), &out, &stderr); code != 0 {
		t.Fatalf("edit: %s", stderr.String())
	}
	after, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatal(err)
	}
	for _, literal := range []string{"${UNSET_SECRET_URL}", canary, "9007199254740993"} {
		if !bytes.Contains(after, []byte(literal)) {
			t.Fatalf("raw value lost: %s", literal)
		}
	}
	if bytes.Contains(after, []byte("CONFIG_ENV_PLACEHOLDER")) {
		t.Fatal("persisted validation placeholder")
	}
	out.Reset()
	stderr.Reset()
	if code := configCommand([]string{"edit", "--stdin", "--expect-revision", inspection.Revision}, strings.NewReader(`{"set":{"/notifications/desktop/volume":0.75}}`), &out, &stderr); code == 0 || !strings.Contains(stderr.String(), "ConfigConflict") {
		t.Fatal("stale wizard edit accepted")
	}
	unchanged, err := os.ReadFile(canonical)
	if err != nil || !bytes.Equal(after, unchanged) {
		t.Fatal("conflict changed config")
	}
}
