package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
)

type bootstrapMigrationError struct{ cause error }

func (e bootstrapMigrationError) Error() string {
	if errors.Is(e.cause, uapinstaller.ErrPlanChanged) {
		return "UAP registry changed after confirmation. Run setup again and confirm the new installation plan."
	}
	if errors.Is(e.cause, context.Canceled) || errors.Is(e.cause, context.DeadlineExceeded) {
		return "UAP registry upgrade was interrupted before client registration. Run setup again to review the registry."
	}
	if errors.Is(e.cause, uapinstaller.ErrRecoveryRequired) {
		return "The older UAP registry has unfinished operations. Complete recovery with the previous installer, then run setup again to confirm the registry upgrade."
	}
	if errors.Is(e.cause, uapinstaller.ErrUnsupportedStateSchema) {
		var schema *uapinstaller.UnsupportedStateSchemaError
		if errors.As(e.cause, &schema) {
			if schema.SourceSchema > schema.TargetSchema {
				return fmt.Sprintf("UAP registry schema %d is newer than supported schema %d. Use a newer installer; this installer cannot downgrade the registry.", schema.SourceSchema, schema.TargetSchema)
			}
			return fmt.Sprintf("UAP registry schema %d has no supported upgrade to schema %d. Use a compatible installer or restore a supported registry backup.", schema.SourceSchema, schema.TargetSchema)
		}
		return "The UAP registry uses an unsupported schema. Use a compatible installer or restore a supported registry backup."
	}
	if errors.Is(e.cause, uapinstaller.ErrStateCorrupt) {
		return "The UAP registry is damaged or invalid. Restore a valid registry backup, then run setup again; client registrations have not been changed."
	}
	return "UAP registry upgrade could not complete. Check access to its state directory and available disk space, then run setup again to review the registry."
}
func (e bootstrapMigrationError) Unwrap() error    { return e.cause }
func bootstrapStateMigrationError(err error) error { return bootstrapMigrationError{cause: err} }

// The bootstrap parent calls this once, after the combined confirmation and
// before primary registrations or workers. This never rewrites the immutable
// confirmation record or lets a worker approve changed registry bytes.
func applyBootstrapStateMigration(ctx context.Context, intent confirmedBootstrapIntent, output, prompts io.Writer) error {
	if intent.StateMigration == nil {
		return writeSelectorResult(ctx, output, "unchanged\n")
	}
	request := intent.Request
	request.IntentFile = ""
	if err := preflightBootstrapIntent(ctx, intent, request); err != nil {
		return err
	}
	plan := *intent.StateMigration
	if plan.Required {
		if err := writeSelectorResult(ctx, prompts, fmt.Sprintf("Upgrading UAP registry: schema %d -> %d…\n", plan.SourceSchema, plan.TargetSchema)); err != nil {
			return err
		}
	}
	applied, err := setupwizard.ApplyStateMigration(ctx, string(intent.Scopes["control-root"]), plan)
	if applied.BackupPath != "" {
		path, displayErr := setupProductsNotice("Backup: " + applied.BackupPath)
		if displayErr != nil {
			return displayErr
		}
		if writeErr := writeSelectorResult(ctx, prompts, path); writeErr != nil {
			return writeErr
		}
	}
	if err != nil {
		return bootstrapStateMigrationError(err)
	}
	if applied.NoChange {
		return writeSelectorResult(ctx, output, "unchanged\n")
	}
	if err := writeSelectorResult(ctx, prompts, fmt.Sprintf("UAP registry upgraded: schema %d -> %d.\n", plan.SourceSchema, plan.TargetSchema)); err != nil {
		return err
	}
	return writeSelectorResult(ctx, output, "migrated\n")
}
