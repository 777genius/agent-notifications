package setupwizard

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// StateMigrationOutcome keeps registry work separate from client activation.
// A backup may exist even if saving the upgraded registry failed.
type StateMigrationOutcome struct {
	Outcome      string `json:"outcome"`
	SourceSchema int    `json:"sourceSchema"`
	TargetSchema int    `json:"targetSchema"`
	BackupPath   string `json:"backupPath,omitempty"`
	Migrated     int    `json:"migrated"`
	NeedsRebind  int    `json:"needsRebind"`
}

func stateMigrationEngine(controlRoot string) (*uapinstaller.Engine, error) {
	if !explicitAbs(controlRoot) {
		return nil, ErrRefused
	}
	uapRoot := filepath.Join(filepath.Dir(controlRoot), "uap")
	return uapinstaller.New(uapinstaller.Config{
		StateRoot:      filepath.Join(uapRoot, "state"),
		ManagedRoot:    filepath.Join(uapRoot, "managed"),
		PluginDataBase: filepath.Join(uapRoot, "plugin-data"),
	})
}

// PrepareStateMigration reads the selected registry without creating directories,
// locking it, publishing an intent, or changing client configuration.
func PrepareStateMigration(ctx context.Context, controlRoot string) (uapinstaller.StateMigrationPlan, error) {
	engine, err := stateMigrationEngine(controlRoot)
	if err != nil {
		return uapinstaller.StateMigrationPlan{}, err
	}
	return engine.PrepareStateMigration(ctx)
}

// CheckStateMigration revalidates a reviewed snapshot without applying it. The
// byte check precedes decoding so an invalid replacement still needs new consent.
func CheckStateMigration(ctx context.Context, controlRoot string, plan uapinstaller.StateMigrationPlan) error {
	if ctx == nil {
		return ErrRefused
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !explicitAbs(controlRoot) {
		return ErrRefused
	}
	identity, err := installruntime.Fingerprint(uapStateFile(controlRoot))
	if err != nil {
		return err
	}
	digest := "missing"
	if identity.Exists {
		digest = "sha256:" + identity.SHA256
	}
	if digest != plan.StateDigest {
		return uapinstaller.ErrPlanChanged
	}
	current, err := PrepareStateMigration(ctx, controlRoot)
	if err != nil {
		return err
	}
	if current != plan {
		return uapinstaller.ErrPlanChanged
	}
	return nil
}

// ApplyStateMigration uses UAP's shared mutation lock, exact byte backup and
// atomic writer. The supplied plan is bound to the selected control root.
func ApplyStateMigration(ctx context.Context, controlRoot string, plan uapinstaller.StateMigrationPlan) (uapinstaller.StateMigrationResult, error) {
	engine, err := stateMigrationEngine(controlRoot)
	if err != nil {
		return uapinstaller.StateMigrationResult{}, err
	}
	return engine.ApplyStateMigration(ctx, plan)
}

func stateMigrationSummary(plan uapinstaller.StateMigrationPlan) string {
	return fmt.Sprintf("UAP registry upgrade: schema %d -> %d; %d installation(s); exact backup before changes", plan.SourceSchema, plan.TargetSchema, plan.Installations)
}

func applyRequestedStateMigration(ctx context.Context, req *Request) (*StateMigrationOutcome, error) {
	plan := req.StateMigration
	if plan == nil {
		prepared, err := PrepareStateMigration(ctx, req.ControlRoot)
		if err != nil {
			return nil, err
		}
		plan = &prepared
	}
	req.StateMigration = nil
	if !plan.Required {
		_, err := ApplyStateMigration(ctx, req.ControlRoot, *plan)
		return nil, err
	}
	// Bootstrap owns migration once in its parent process, before independent
	// client workers. A late old registry is a refusal, never worker consent.
	if req.BootstrapMCP != nil || req.BootstrapStateMigrationOwned {
		return nil, uapinstaller.ErrMigrationRequired
	}
	if req.Progress != nil {
		req.Progress("registry-migration")
	}
	applied, err := ApplyStateMigration(ctx, req.ControlRoot, *plan)
	outcome := &StateMigrationOutcome{Outcome: "completed", SourceSchema: plan.SourceSchema, TargetSchema: plan.TargetSchema,
		BackupPath: applied.BackupPath, Migrated: applied.Migrated, NeedsRebind: applied.NeedsRebind}
	if err != nil {
		outcome.Outcome = "incomplete"
	}
	if req.StateMigrationProgress != nil {
		req.StateMigrationProgress(*outcome)
	}
	return outcome, err
}

// StateMigrationFailure gives orchestration callers the same finite diagnostic
// as Plan and Run without exposing registry contents or activation-only retries.
func StateMigrationFailure(req Request, err error) Result {
	out := Result{Action: string(req.Action), Outcome: "incomplete", Reason: "state_migration_unavailable"}
	if errors.Is(err, uapinstaller.ErrPlanChanged) {
		out.Outcome, out.Reason = "conflict", "state_migration_changed"
	} else if errors.Is(err, uapinstaller.ErrMigrationRequired) {
		out.Reason = "state_migration_required"
	} else if errors.Is(err, uapinstaller.ErrRecoveryRequired) {
		out.Reason = "state_migration_pending"
	} else if errors.Is(err, uapinstaller.ErrUnsupportedStateSchema) {
		out.Reason = "state_migration_unsupported"
		var schema *uapinstaller.UnsupportedStateSchemaError
		if errors.As(err, &schema) && schema.SourceSchema > schema.TargetSchema {
			out.Reason = "state_migration_future"
		}
	} else if errors.Is(err, uapinstaller.ErrStateCorrupt) {
		out.Reason = "state_migration_corrupt"
	}
	return out
}
