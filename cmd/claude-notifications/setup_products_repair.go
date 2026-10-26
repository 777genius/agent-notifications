package main

import (
	"context"
	"fmt"
	"io"
	"os"
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
	canonical, err := installruntime.CanonicalPath(root)
	if err != nil {
		return err
	}
	root = canonical
	observe, cancel := context.WithTimeout(ctx, bootstrapInspectionTimeout)
	defer cancel()
	plan, err := installruntime.PreviewCacheRepair(observe, root)
	cancel()
	if err != nil {
		// This optional read-only step must not replace the existing installer
		// decision: it may repair a non-cache file, retire an old Claude cache,
		// or replay a journal. Those writers retain their normal locked checks.
		if a.BootstrapCacheRepair && ctx.Err() == nil {
			return writeSelectorResult(ctx, output, "ready\n")
		}
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
