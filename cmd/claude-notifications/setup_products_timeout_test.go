//go:build linux || darwin

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Existing installs can retain several release binaries. Their verified state
// must reach confirmation rather than fail with unknown MCP under the old 2s
// discovery budget. All files, profiles and inert CLIs belong to a TEST home.
func TestSetupProductsConfirmationExistingRuntime(t *testing.T) {
	f, _, _ := configureFixture(t)
	path := filepath.Join(f.root, "TEST-PATH")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"claude", "codex"} {
		if err := os.WriteFile(filepath.Join(path, id), []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", path)
	t.Setenv("AGENT_NOTIFICATIONS_CONTROL_ROOT", f.control)
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", f.global)
	if err := os.MkdirAll(filepath.Join(f.root, "codex"), 0700); err != nil {
		t.Fatal(err)
	}
	setupCommandWrite(t, filepath.Join(f.root, ".claude.json"), "{}", 0600)
	setupCommandWrite(t, filepath.Join(f.root, "codex", "config.toml"), "model = 'TEST'\n", 0600)
	data := bytes.Repeat([]byte("TEST executable payload\n"), 365000)
	var files []installruntime.File
	for n := 0; n < 17; n++ {
		files = append(files, installruntime.File{Path: filepath.Join(f.runtime, fmt.Sprintf("TEST-retained-%02d", n)), Data: data, Mode: 0600})
	}
	if _, err := installruntime.Commit(setupCommandContext(t), installruntime.Request{ControlRoot: f.control, RuntimeRoot: f.runtime, Owner: "existing-installer", ConsumerID: "TEST-retained-releases", Files: files}); err != nil {
		t.Fatal(err)
	}
	before := setupCommandTree(t, f.root)
	var out, prompt bytes.Buffer
	code := runSetupProducts([]string{"confirm", "--products", "claude,codex", "--control-root", f.control}, strings.NewReader("n\n"), &out, &prompt)
	if code != 0 || out.Len() != 0 || !strings.Contains(prompt.String(), setupProductsConfirmationTitle) || !strings.Contains(prompt.String(), "Installation cancelled.") {
		t.Fatalf("existing runtime did not reach confirmation: code=%d output=%q prompt=%s", code, out.String(), prompt.String())
	}
	if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
		t.Fatal("cancelled confirmation changed TEST home")
	}
}
