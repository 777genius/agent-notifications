package main

import (
	"strings"
	"testing"
)

func TestQoderCLIContainment(t *testing.T) {
	env := append(codexCLIEnv(t), "QODER_PLUGIN_ROOT="+t.TempDir())
	for _, tc := range []struct{ name, event, payload string }{
		{"valid", "Stop", `{"session_id":"qoder","hook_event_name":"Stop","last_assistant_message":"Done"}`},
		{"malformed", "Stop", `{oops`},
		{"oversized", "Stop", strings.Repeat("x", (1<<20)+1)},
		{"unsupported", "PermissionRequest", `{}`},
	} {
		assertContained(t, tc.name, runCLI(t, env, tc.payload, "handle-hook", tc.event, "--product", "qoder"))
	}
}

func TestQoderRootIsExplicit(t *testing.T) {
	t.Setenv("PLUGIN_ROOT", "codex-root")
	t.Setenv("QODER_PLUGIN_ROOT", "qoder-root")
	if got := getPluginRootForProduct("qoder"); got != "qoder-root" {
		t.Fatalf("root %q", got)
	}
	if got := getPluginRootForProduct("codex"); got != "codex-root" {
		t.Fatalf("codex root %q", got)
	}
}
