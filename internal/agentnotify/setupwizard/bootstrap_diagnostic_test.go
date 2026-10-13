//go:build linux || darwin

package setupwizard

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
)

// A genuine unowned entry must stay refused, identify its client and path, and
// leave the config/ledger unchanged through both bootstrap observation paths.
func TestBootstrapMCPInspectionDiagnostic(t *testing.T) {
	for _, client := range []string{"claude", "codex"} {
		t.Run(client, func(t *testing.T) {
			control, runtimeRoot, _, _, _ := managedRuntime(t)
			root := filepath.Dir(control)
			path := filepath.Join(root, "TEST profile", "config")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			data := []byte(`{"mcpServers":{"agent_notifications":{"command":"TEST-unowned","args":[]}}}`)
			if client == "codex" {
				data = []byte("[mcp_servers.agent_notifications]\ncommand = 'TEST-unowned'\nargs = []\n")
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			ledgerPath := filepath.Join(control, "ownership.json")
			before, err := os.ReadFile(ledgerPath)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			r := Request{Action: ActionInspect, Agents: []string{client}, ControlRoot: control, RuntimeRoot: runtimeRoot, ClaudeConfig: filepath.Dir(path), CodexHome: filepath.Dir(path), MCPConfig: map[string]string{client: path}}
			report, err := Run(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			_, _, selectionErr := BootstrapAutoTargets(ctx, r, report)
			_, _, observationErr := ObserveBootstrapMCP(ctx, r)
			label := "Claude Code"
			if client == "codex" {
				label = "Codex"
			}
			for _, err := range []error{selectionErr, observationErr} {
				if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), label) || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "existing MCP entry conflicts") {
					t.Fatalf("missing attributed conflict: %v", err)
				}
			}
			after, err := os.ReadFile(ledgerPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("inspection changed ledger: %v", err)
			}
			config, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, config) {
				t.Fatalf("inspection changed config: %v", err)
			}
		})
	}
}

// Deadline/cancellation must never be flattened into an ownership conflict or
// fresh installation. Unknown parser content and terminal controls stay hidden.
func TestBootstrapMCPInspectionDeadlineAndPrivacy(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	selected, skipped, err := BootstrapAutoTargets(ctx, Request{}, Result{})
	if !errors.Is(err, context.DeadlineExceeded) || len(selected)+len(skipped) != 0 {
		t.Fatalf("expired inspection: %v %v %v", selected, skipped, err)
	}
	for _, reason := range []string{context.DeadlineExceeded.Error(), registration.ErrInvalid.Error(), "TEST secret value \x1b[31m"} {
		err := bootstrapMCPInspectionError(TargetResult{Client: "codex", ConfigPath: "/TEST profile/\x1b[31m/config", Reason: reason})
		if strings.Contains(err.Error(), "TEST secret") || strings.ContainsRune(err.Error(), '\x1b') || !strings.Contains(err.Error(), `\x1b`) {
			t.Fatalf("unsafe diagnostic: %q", err)
		}
		if reason == context.DeadlineExceeded.Error() && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("deadline lost: %v", err)
		}
	}
}
