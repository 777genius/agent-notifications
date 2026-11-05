package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
)

func TestWizardEarlyJSONRejectionThroughPublicCLI(t *testing.T) {
	private := "PRIVATE_SETUP_ARGUMENT_CANARY"
	many := make([]string, 65)
	for i := range many {
		many[i] = "--yes"
	}
	many[len(many)-1] = "--json"
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"json-before-oversized", []string{"--json", "--agents", strings.Repeat(private, 200)}},
		{"json-after-oversized", []string{"--agents", strings.Repeat(private, 200), "--json"}},
		{"json-after-null", []string{"--agents", private + "\x00", "--json"}},
		{"json-at-arg-count-boundary", many},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			root := filepath.Join(t.TempDir(), "TEST-no-owned-state")
			args := append(append([]string(nil), tc.args...), "--control-root", root)
			exit := executeSetupWizardWith(context.Background(), args, &out, io.Discard, strings.NewReader(""), false)
			if exit != 2 {
				t.Fatalf("invalid args exit=%d response=%q", exit, out.String())
			}
			var response setupwizard.Result
			if err := json.Unmarshal(out.Bytes(), &response); err != nil {
				t.Fatalf("invalid JSON wire response: %v, %q", err, out.String())
			}
			if response.Outcome != "invalid" || response.Reason != "invalid_arguments" ||
				strings.Contains(out.String(), private) {
				t.Fatalf("private input leaked or wrong wire result: %+v", response)
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatalf("invalid request performed setup I/O: %v", err)
			}
		})
	}
}
