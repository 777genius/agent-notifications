package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeMCPHealth(t *testing.T) {
	for _, tc := range []struct {
		name, selector, projection string
		installed, duplicate, ok   bool
	}{
		{"local", "agent-notifications", `{"agent-notifications":{"type":"stdio","command":"./bin/probe"}}`, true, false, true},
		{"alternate", "agent-notify", `{"agent-notify":{"command":"./bin/probe"}}`, true, false, true},
		{"missing", "agent-notifications", `{}`, false, false, false},
		{"duplicate", "agent-notifications", `{"agent-notifications":{"command":"./bin/probe"}}`, true, true, false},
		{"foreign", "other", `{"other":{"command":"./bin/probe"}}`, true, false, false},
		{"wrong-server", "agent-notifications", `{"agent-notify":{"command":"./bin/probe"}}`, true, false, false},
		{"remote", "agent-notifications", `{"agent-notifications":{"type":"http","url":"https://example.invalid","command":"./bin/probe"}}`, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := t.TempDir()
			write := func(name string) {
				root := filepath.Join(profile, "skills", name)
				if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, ".claude-plugin", "plugin.json"), []byte(`{"name":"agent-notify"}`), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(tc.projection), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.installed {
				write("one")
			}
			if tc.duplicate {
				write("two")
			}
			selector := "plugin:agent-notify:" + tc.selector
			body, handled, err := ClaudeMCPHealth(profile, []string{"claude", "mcp", "get", selector})
			if !handled || (err == nil) != tc.ok {
				t.Fatalf("handled=%v err=%v", handled, err)
			}
			if tc.ok && string(body) != selector+":\n  Scope: Dynamic config (from command line)\n  Status: ✔ Connected\n  Type: stdio\n" {
				t.Fatalf("wrong health response %q", body)
			}
			if !tc.ok && len(body) != 0 {
				t.Fatal("refused fixture emitted health")
			}
		})
	}
	if body, handled, err := ClaudeMCPHealth(t.TempDir(), []string{"claude", "plugin", "list", "--json"}); len(body) != 0 || handled || err != nil {
		t.Fatal("listing intercepted")
	}
}
