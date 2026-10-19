package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// A silent exit alone is insufficient: a routing rejection also fails open.
// Prove that the exact public command reaches the Qoder profile and delivery.
func TestQoderCLIActuallyDelivers(t *testing.T) {
	received := make(chan []byte, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- body
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	root := t.TempDir()
	cfg := filepath.Join(root, "qoder-config.json")
	body := fmt.Sprintf(`{"schemaVersion":2,"notifications":{"desktop":{"enabled":false},"webhook":{"enabled":false}},"agents":{"qoder":{"notifications":{"webhook":{"enabled":true,"url":%q,"preset":"slack"}}}}}`, srv.URL)
	if err := os.WriteFile(cfg, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	env := append(codexCLIEnv(t), "AGENT_NOTIFICATIONS_CONFIG="+cfg, "QODER_PLUGIN_ROOT="+root)
	marker := fmt.Sprintf("qoder-delivery-%d", time.Now().UnixNano())
	payload := fmt.Sprintf(`{"session_id":%q,"hook_event_name":"Stop","last_assistant_message":%q}`, marker, marker)
	assertContained(t, "Qoder delivery", runCLI(t, env, payload, "handle-hook", "Stop", "--product", "qoder"))
	select {
	case got := <-received:
		if !strings.Contains(string(got), marker) {
			t.Fatalf("missing completion marker: %q", got)
		}
	case <-time.After(3 * time.Second):
		log, _ := os.ReadFile(filepath.Join(root, "notification-debug.log"))
		t.Fatalf("Qoder route failed to deliver: %s", log)
	}
}
