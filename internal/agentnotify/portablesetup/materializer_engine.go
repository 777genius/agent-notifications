package portablesetup

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func (m Materializer) engine(req MaterializeRequest, generation *uint64, res *installruntime.PendingMutation) (*uapinstaller.Engine, error) {
	return m.engineWithIdentities(req, generation, res, nil)
}

// Group callbacks resolve the identity selected for the committed client.
func (m Materializer) engineWithIdentities(req MaterializeRequest, generation *uint64, res *installruntime.PendingMutation, identities map[string]Identity) (*uapinstaller.Engine, error) {
	identityFor := func(client string) (Identity, error) {
		if identities == nil {
			return req.Identity, nil
		}
		id, ok := identities[client]
		if !ok {
			return Identity{}, fmt.Errorf("%w: unexpected group client %s", ErrPreflight, client)
		}
		return id, nil
	}
	helper := m.Roots.HelperExecutable
	if req.HelperExecutable != "" {
		helper = req.HelperExecutable
	}
	runner := m.Roots.ClaudeRunner
	if req.Integration == portable.Codex {
		runner = m.Roots.CodexRunner
	}
	registry := m.Registry
	if registry == nil {
		var err error
		registry, err = NewRegistry()
		if err != nil {
			return nil, err
		}
	}
	registry, err := m.piRetainedRegistry(req, registry)
	if err != nil {
		return nil, err
	}
	return uapinstaller.New(uapinstaller.Config{
		IndependentActivations: true,
		Progress: func(event uapinstaller.ProgressEvent) {
			if m.Progress == nil || event.Phase != uapinstaller.ProgressActivate || event.Client == nil {
				return
			}
			client := event.Client
			if client.ClientID != "claude" && client.ClientID != "codex" {
				return
			}
			switch client.GroupPhase {
			case "external_started":
				m.Progress(client.ClientID, "verification", "running")
			case "external_completed":
				// External verification is finished, not the enclosing materializer
				// transaction. Only the final command result marks setup complete.
				m.Progress(client.ClientID, "verification", "done")
			case "external_failed":
				outcome := "activation_failed"
				if client.Failure != nil {
					switch client.Failure.Stage {
					case "timeout":
						outcome = "activation_timeout"
					case "canceled":
						outcome = "activation_canceled"
					case "deadline":
						outcome = "activation_deadline"
					}
				}
				m.Progress(client.ClientID, "MCP", outcome)
			}
		},
		StateRoot:            filepath.Dir(m.Roots.StateFile),
		StateFile:            m.Roots.StateFile,
		LockFile:             m.Roots.LockFile,
		OperationsDir:        m.Roots.OperationsDir,
		PluginDataBase:       m.Roots.PluginDataBase,
		ManagedRoot:          m.Roots.ManagedRoot,
		TempRoot:             filepath.Join(filepath.Dir(m.Roots.StateFile), "tmp"),
		HelperExecutable:     helper,
		HelperVersion:        m.Roots.HelperVersion,
		Runner:               runner,
		Registry:             registry,
		TrustedLocalPackages: true,
		ServerName:           declaredServerName(req.PackageRoot),
		ProjectArgs: func(facts uapinstaller.BindingFacts) ([]string, error) {
			id, err := identityFor(facts.ClientID)
			if err != nil {
				return nil, err
			}
			b, err := Complete(id, integrationOf(facts), facts.ClientID, facts.Scope, facts.TargetPath, facts.DataRoot)
			if err != nil {
				return nil, err
			}
			name, err := b.Filename()
			if err != nil {
				return nil, err
			}
			if b.Integration == portable.Codex {
				// Codex supplies its own PLUGIN_DATA after reading .mcp.json.
				// Keep the UAP-owned locator root in immutable argv instead.
				return []string{"portable-launch", "--data-root", b.DataRoot, "--locator", name}, nil
			}
			return []string{"portable-launch", "--locator", name}, nil
		},
		OnCommittedBinding: func(ctx context.Context, facts uapinstaller.BindingFacts) error {
			id, err := identityFor(facts.ClientID)
			if err != nil {
				return err
			}
			pb, err := Complete(id, integrationOf(facts), facts.ClientID, facts.Scope, facts.TargetPath, facts.DataRoot)
			if err != nil {
				return err
			}
			if pb.BindingID != facts.BindingID && facts.BindingID != "" {
				return fmt.Errorf("%w: binding identity does not match committed UAP client", ErrPreflight)
			}
			if err := refuseConflictingLocator(pb); err != nil {
				return err
			}
			if pb.Integration == portable.Pi {
				next, err := m.commitPiBinding(ctx, req, pb, res)
				if err != nil {
					return err
				}
				*generation = next
				if m.piHandoffDone != nil {
					*m.piHandoffDone = true
				}
				return nil
			}
			if _, err := m.Kernel.CommitBinding(ctx, Request{Binding: pb, ExpectedGeneration: *generation, Reservation: res}); err != nil {
				return err
			}
			snap, err := installruntime.ReadInstalledSnapshot(pb.ControlRoot)
			if err != nil {
				return err
			}
			*generation = snap.Ledger.Generation
			return nil
		},
	})
}

func (m Materializer) apply(ctx context.Context, eng *uapinstaller.Engine, req uapinstaller.Request, expected ...MaterializeRequest) (uapinstaller.Result, error) {
	prepared, err := eng.Prepare(ctx, req)
	if err != nil {
		return uapinstaller.Result{}, err
	}
	defer func() { _ = prepared.Close() }()
	for index, selected := range expected {
		if index == 0 {
			if err := confirmPreparedIdentity(selected, prepared.Plan()); err != nil {
				return uapinstaller.Result{}, err
			}
		} else if selected.Integration == portable.CopilotVSCode {
			if err := confirmLocalSelection(selected, prepared.Plan()); err != nil {
				return uapinstaller.Result{}, err
			}
		}
	}
	return eng.Apply(ctx, prepared, uapinstaller.Decision{Confirmed: true})
}

// prepareRemove is the §5.5.2 read-only removal preflight. It refuses a pending
// journal and verifies the managed artifact before locator revoke or Apply.
func (m Materializer) prepareRemove(ctx context.Context, eng *uapinstaller.Engine, req uapinstaller.Request, expected ...MaterializeRequest) (*uapinstaller.PreparedOperation, error) {
	view, err := eng.Inspect(ctx)
	if err != nil {
		return nil, err
	}
	if view.Recovery.Required {
		reason := view.Recovery.Reason
		if reason == "" {
			reason = "pending transactions remain"
		}
		return nil, fmt.Errorf("%w: %s", uapinstaller.ErrRecoveryRequired, reason)
	}
	prepared, err := eng.Prepare(ctx, req)
	if err != nil {
		return nil, err
	}

	plan := prepared.Plan()
	for index, selected := range expected {
		// Removal freezes every selected Local client's recorded delivery,
		// including its original Stop/components. Requested deselection is
		// not replacement removal authority, regardless of group order.
		if selected.Integration == portable.CopilotVSCode {
			facts, ok := plan.SelectedDelivery.LocalFacts()
			alreadyAbsent := false
			if len(plan.Targets) > 0 {
				ok = false
				for _, target := range plan.Targets {
					if target.ClientID == ClientID(selected.Integration) {
						facts, ok = target.SelectedDelivery.LocalFacts()
						// UAP gives an absent target no delivery authority.
						// Only this explicit no-op may skip its comparison.
						alreadyAbsent = target.NoChange
						break
					}
				}
			}
			if !alreadyAbsent && (!ok || selected.LocalConfig == nil || facts.SettingsPath != selected.LocalConfig.ProfileSettingsPath || facts.Tuple != selected.LocalConfig.QualifiedTuple) {
				_ = prepared.Close()
				return nil, ErrPreflight
			}
			selected.LocalConfig = nil
		}
		// The enclosing plan retains the existing primary source/helper
		// identity check; sibling delivery authority is checked above.
		if index == 0 {
			if err := confirmPreparedIdentity(selected, plan); err != nil {
				_ = prepared.Close()
				return nil, err
			}
		}
	}
	return prepared, nil
}

func confirmPreparedIdentity(req MaterializeRequest, plan uapinstaller.Plan) error {
	if req.LocalConfig != nil {
		if err := confirmLocalSelection(req, plan); err != nil {
			return err
		}
	}
	if err := matchOptionalIdentity("tree digest", req.TreeDigest, plan.TreeDigest); err != nil {
		return err
	}
	if err := matchOptionalIdentity("helper digest", req.HelperDigest, plan.HelperDigest); err != nil {
		return err
	}
	return matchOptionalIdentity("helper version", req.HelperVersion, plan.HelperVersion)
}

func matchOptionalIdentity(name, expected, got string) error {
	if expected == "" || got == "" || expected == got {
		return nil
	}
	return fmt.Errorf("%w: %s %s desired %s", ErrSourceIdentityDrift, name, expected, got)
}

func integrationOf(facts uapinstaller.BindingFacts) portable.Integration {
	return Integration(facts.ClientID)
}
