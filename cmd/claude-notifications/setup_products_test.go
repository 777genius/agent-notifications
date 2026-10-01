package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The old top-level single-choice menu could not express a mixed observer and
// portable selection. Verify the real UAP SelectMany and machine-output seam,
// including cancellation without installation/config writes.
func TestSetupProductsSelect(t *testing.T) {
	root := t.TempDir()
	for _, key := range []string{"AGENT_NOTIFICATIONS_CONTROL_ROOT", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "GEMINI_CLI_HOME", "OPENCODE_CONFIG_DIR", "XDG_CONFIG_HOME"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	for _, tc := range []struct {
		name, answer, want string
		code               int
	}{
		{"mixed-numbers", "1,3,4\n", "claude,opencode,gemini\n", 0},
		{"mixed-ids", "codex,gemini\n", "codex,gemini\n", 0},
		{"all", "1,2,3,4\n", "claude,codex,opencode,gemini\n", 0},
		{"cancel", "cancel\n", "", 0},
		{"empty", "\n", "", 0},
		{"closed", "", "", 1},
		{"duplicate", "1,claude\n", "", 1},
		{"unknown", "5\n", "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var selected, prompts bytes.Buffer
			code := runSetupProducts([]string{"select"}, strings.NewReader(tc.answer), &selected, &prompts)
			if code != tc.code || selected.String() != tc.want {
				t.Fatalf("selection: code=%d output=%q prompts=%q", code, selected.String(), prompts.String())
			}
			for _, label := range []string{"Claude Code", "Codex", "OpenCode", "Gemini CLI", "comma-separated"} {
				if !strings.Contains(prompts.String(), label) {
					t.Fatalf("product multiselect omitted %q: %s", label, prompts.String())
				}
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("selection mutated product roots: %v %v", entries, err)
			}
		})
	}
}

func TestSetupProductsRejectsUnexpectedArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"install"}, {"select", "--product", "gemini"}} {
		var selected, prompts bytes.Buffer
		if code := runSetupProducts(args, strings.NewReader("1\n"), &selected, &prompts); code != 2 || selected.Len() != 0 {
			t.Fatalf("unexpected arguments selected products: %v code=%d output=%q", args, code, selected.String())
		}
	}
}
