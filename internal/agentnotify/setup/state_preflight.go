//go:build linux || darwin || windows

package setup

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// inspectInitializedForEnable mirrors provision's non-mutating admission rules.
// The ordinary setup preflight used to validate only route/config/runtime and
// announce "ready" for a missing journal or a policy enabled without setupState.
// Neither an inspection nor a failed setup may adopt/reset private state (#585).
func inspectInitializedForEnable(ctx context.Context, o Options, s installruntime.PolicySnapshot) error {
	if err := installruntime.CheckPrivateControlRoot(o.ControlRoot); err != nil {
		return fail("managed_runtime_required", err)
	}
	raw, present := s.Fields["setupState"]
	if !present {
		if s.Policy.Enabled || s.Installation.Ledger.Enabled {
			return fail("initialization_recovery_required",
				fmt.Errorf("enabled installation has no initialization ownership evidence; preserve policy and state"))
		}
		// A pristine disabled install has no journal. A foreign or interrupted
		// staging directory must be inspected rather than adopted as fresh.
		for _, name := range []string{"state", stageName} {
			_, err := os.Lstat(filepath.Join(o.ControlRoot, name))
			if err == nil {
				return fail("initialization_recovery_required",
					fmt.Errorf("unowned %s exists; preserve it for recovery", name))
			}
			if !os.IsNotExist(err) {
				return fail("initialization_recovery_required",
					fmt.Errorf("cannot verify absence of private setup state: %w", err))
			}
		}
		return nil
	}

	var owner ownership
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	parseErr := decoder.Decode(&owner)
	if parseErr == nil {
		parseErr = decoder.Decode(&struct{}{})
		switch parseErr {
		case io.EOF:
			parseErr = nil
		case nil:
			parseErr = fmt.Errorf("unexpected extra ownership data")
		}
	}
	if parseErr != nil || owner.Schema != 1 ||
		owner.InstallationID != s.Installation.Ledger.ID ||
		len(owner.Token) != 64 || owner.DirectoryID == "" ||
		(owner.Phase != "started" && owner.Phase != "ready") ||
		(owner.Phase == "ready" && len(owner.Namespace) != 64) {
		return fail("initialization_recovery_required",
			fmt.Errorf("invalid durable setupState ownership marker; preserve policy and state"))
	}
	if _, err := hex.DecodeString(owner.Token); err != nil {
		return fail("initialization_recovery_required", fmt.Errorf("invalid ownership token: %w", err))
	}
	// An enabled policy cannot borrow an unfinished journal owner. Apply's
	// recordOwner also rejects this combination, so preflight must not mark
	// such an installation ready (even if staged files look complete).
	if owner.Phase == "started" && (s.Policy.Enabled || s.Installation.Ledger.Enabled) {
		return fail("initialization_recovery_required",
			fmt.Errorf("enabled installation has an unfinished setup ownership marker"))
	}
	return inspectProvisionContinuation(ctx, o, owner)
}

// A complete interrupted journal is resumable by Apply. Preflight must not
// block a legitimate retry after journal_initialized or ownership_ready, nor
// claim readiness for an abandoned pre-initialization stage (#585).
func inspectProvisionContinuation(ctx context.Context, o Options, owner ownership) error {
	root := o.ControlRoot
	state := filepath.Join(root, "state")
	stage := filepath.Join(root, stageName)
	stateInfo, stateErr := os.Lstat(state)
	stageInfo, stageErr := os.Lstat(stage)
	if stateErr != nil && !os.IsNotExist(stateErr) {
		return fail("initialization_recovery_required", fmt.Errorf("cannot inspect owned state: %w", stateErr))
	}
	if stageErr != nil && !os.IsNotExist(stageErr) {
		return fail("initialization_recovery_required", fmt.Errorf("cannot inspect provisioning stage: %w", stageErr))
	}
	if stateErr == nil {
		if owner.Phase != "ready" || stageErr == nil || !stateInfo.IsDir() {
			return fail("initialization_recovery_required",
				fmt.Errorf("unexpected retained or overlapping setup stage; preserve state"))
		}
		// Do not open the snapshot through the commit-time structural check:
		// the canonical read-only journal reader handles concurrent replacement.
		if err := inspectPersistedStateIdentity(root, "state", owner.DirectoryID, owner.Volume); err != nil {
			return fail("initialization_recovery_required",
				fmt.Errorf("state is not the durably owned directory: %w", err))
		}
		return inspectInitializedJournal(ctx, o, state, owner.Namespace)
	}
	if stageErr != nil || !stageInfo.IsDir() {
		return fail("initialization_recovery_required",
			fmt.Errorf("expected owned setup state is absent; preserve ownership evidence"))
	}
	// Use the same persisted-directory identity authority as provision.
	// On Darwin (or btrfs, see renumberedOnVolume) the device may renumber; comparing serialized
	// strings directly would refuse a legitimate interrupted setup.
	if err := inspectPersistedStateIdentity(root, stageName, owner.DirectoryID, owner.Volume); err != nil {
		return fail("initialization_recovery_required",
			fmt.Errorf("provisioning stage is not the durably owned directory: %w", err))
	}
	// Only a fully staged journal may progress under Apply's exclusive locks,
	// identity checks and namespace comparison. No journal.Open/Initialize in
	// the read-only path (they might create files).
	for _, name := range []string{"journal", "native-spool"} {
		info, err := os.Lstat(filepath.Join(stage, name))
		if err != nil || !info.IsDir() {
			return fail("initialization_recovery_required",
				fmt.Errorf("interrupted setup directories are missing or unsafe; preserve private state"))
		}
	}
	for _, name := range []string{
		"journal/namespace", "journal/journal.json", "journal/lock",
		"native-spool/.spool.lock",
	} {
		info, err := os.Lstat(filepath.Join(stage, filepath.FromSlash(name)))
		if err != nil || !info.Mode().IsRegular() {
			return fail("initialization_recovery_required",
				fmt.Errorf("interrupted setup journal is incomplete or unsafe; preserve private state"))
		}
	}
	return inspectInitializedJournal(ctx, o, stage, owner.Namespace)
}

// InspectReadOnly uses the existing canonical journal decoder but takes no
// journal lock, writes no files, and never resets history. A structural check
// alone could call a corrupt journal "ready" before enable (#585).
func inspectInitializedJournal(ctx context.Context, o Options, state, expectedNamespace string) error {
	namespace, err := journal.InspectReadOnly(ctx, journal.Options{
		Root: filepath.Join(state, "journal"), Clock: o.JournalClock,
	})
	if err != nil {
		return fail("initialization_recovery_required",
			fmt.Errorf("persisted setup journal is unreadable or inconsistent; preserve it for recovery: %w", err))
	}
	if expectedNamespace != "" && namespace != expectedNamespace {
		return fail("initialization_recovery_required",
			fmt.Errorf("persisted setup journal namespace does not match ownership"))
	}
	return nil
}
