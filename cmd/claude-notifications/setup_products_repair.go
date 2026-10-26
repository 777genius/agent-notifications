package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/cli/installerui"
)

// Repair precedes the ordinary installation plan: its generation is observed
// afresh by confirmation, and declining repair never publishes an install intent.
func repairBootstrapCache(ctx context.Context, a setupProductsArgs, input io.Reader, output, prompts io.Writer) error {
	root := a.Scopes["control-root"]
	if root == "" {
		root = os.Getenv("AGENT_NOTIFICATIONS_CONTROL_ROOT")
	}
	if root == "" {
		var err error
		root, err = installruntime.ControlRoot()
		if err != nil {
			return err
		}
	}
	if !validProductPath(root) {
		return fmt.Errorf("invalid control-root authority")
	}
	observe, cancel := context.WithTimeout(ctx, bootstrapInspectionTimeout)
	defer cancel()
	if a.BootstrapCacheRepair {
		_, pending, err := installruntime.ReadOwnership(root)
		if err == nil && pending {
			// The existing installer owns redo/rollback. Repair neither consumes
			// its journal nor prevents an explicit install from recovering it.
			return writeSelectorResult(ctx, output, "ready\n")
		}
	}
	// Explicit Claude installs already own a guarded relocation path that can
	// retire their sole old cache without inspecting its drift. Do not make
	// optional repair a new prerequisite for that existing operation.
	if a.DeferClaudeRelocation && soleClaudeCacheDrift(observe, root) {
		return writeSelectorResult(ctx, output, "ready\n")
	}
	plan, err := installruntime.PreviewCacheRepair(observe, root)
	cancel()
	if err != nil {
		return err
	}
	if len(plan.Paths) == 0 {
		return writeSelectorResult(ctx, output, "ready\n")
	}
	if !a.RepairApproved {
		terminal, line, err := newSetupProductsPrompt(input, prompts, a.Mode)
		if err != nil {
			return err
		}
		req := installerui.ConfirmRequest{Title: "Restore missing notification files?", Default: true, Summary: []string{
			fmt.Sprintf("The previous Claude cache lost %d managed files or links.", len(plan.Paths)),
			"Restore exact verified copies before preparing the installation plan.",
			"Existing settings and integrations are preserved.",
		}}
		var answer installerui.Confirmation
		if terminal != nil {
			answer, err = terminal.Confirm(ctx, req)
		} else {
			answer, err = line.Confirm(ctx, req)
		}
		if err != nil {
			return err
		}
		if answer.Cancelled || !answer.Accepted {
			return writeSelectorResult(ctx, prompts, "Installation cancelled. No changes were applied.\n")
		}
	}
	repair, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if _, err := installruntime.RepairCache(repair, root, plan); err != nil {
		return err
	}
	if err := writeSelectorResult(ctx, prompts, "Missing notification files restored.\n"); err != nil {
		return err
	}
	return writeSelectorResult(ctx, output, "ready\n")
}

func soleClaudeCacheDrift(ctx context.Context, root string) bool {
	l, _, err := installruntime.ReadOwnership(root)
	if err != nil {
		return false
	}
	hooks, exists := l.Consumers["claude-hooks"]
	if !exists || !filepath.IsAbs(hooks.RuntimeRoot) {
		return false
	}
	for id, consumer := range l.Consumers {
		if id != "claude-hooks" && consumer.RuntimeRoot == hooks.RuntimeRoot {
			return false
		}
	}
	drift := false
	for path, want := range l.Files {
		if ctx.Err() != nil {
			return false
		}
		got, err := installruntime.Fingerprint(path)
		if err == nil && got == want {
			continue
		}
		relative, relErr := filepath.Rel(hooks.RuntimeRoot, path)
		if relErr != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return false
		}
		drift = true
	}
	return drift
}
