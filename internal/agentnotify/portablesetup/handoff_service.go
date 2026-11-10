package portablesetup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/agentnotify/clientsetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// HandoffReverse recreates the exact owned global MCP only after the portable
// locator and consumer are gone. It never restores a whole-config backup.
func (s Service) HandoffReverse(ctx context.Context, req Request) (uint64, error) {
	if ctx == nil {
		return 0, ErrPreflight
	}
	if req.Discovery.ConfigPath == "" {
		return req.ExpectedGeneration, nil
	}
	name, err := req.Binding.Filename()
	if err != nil {
		return 0, err
	}
	if _, err := os.Lstat(filepath.Join(req.Binding.DataRoot, name)); err == nil {
		return 0, fmt.Errorf("%w: portable locator still present", ErrPreflight)
	} else if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	key, _, _, err := req.Binding.Registration()
	if err != nil {
		return 0, err
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.Binding.ControlRoot)
	if err != nil {
		return 0, err
	}
	if _, ok := snap.Ledger.Consumers[key]; ok {
		return 0, fmt.Errorf("%w: portable consumer still registered", ErrPreflight)
	}
	command := req.Discovery.Command
	if command == "" {
		return 0, fmt.Errorf("%w: owned notification command missing", ErrPreflight)
	}
	provider, err := discoveryProvider(req.Binding.Integration)
	if err != nil {
		return 0, err
	}
	res, err := s.matchingReservation(req, "uninstall")
	if err != nil {
		return 0, err
	}
	result, err := clientsetup.Apply(ctx, clientsetup.Request{
		ControlRoot: req.Binding.ControlRoot, RuntimeRoot: req.Binding.RuntimeRoot, Command: command,
		ConfigPath: req.Discovery.ConfigPath, Provider: provider, Mode: clientsetup.Managed,
		ExpectedGeneration: req.ExpectedGeneration, ExpectedPolicy: s.ExpectedPolicy, SkillProjection: req.Discovery.Skill,
		Reservation: res,
	})
	if err != nil {
		return 0, fmt.Errorf("%w: reverse discovery handoff: %w", ErrPreflight, policyConflict(err))
	}
	return result.Ledger.Generation, nil
}

func (s Service) handoffForward(ctx context.Context, req Request, action string) (uint64, *installruntime.PendingMutation, error) {
	if err := s.checkPolicy(ctx, req.Binding.ControlRoot); err != nil {
		return 0, nil, err
	}
	if req.Discovery.ConfigPath == "" {
		return req.ExpectedGeneration, nil, nil
	}
	command := req.Discovery.Command
	if command == "" {
		return 0, nil, fmt.Errorf("%w: owned notification command missing", ErrPreflight)
	}
	provider, err := discoveryProvider(req.Binding.Integration)
	if err != nil {
		return 0, nil, err
	}
	r := clientsetup.Request{
		ControlRoot: req.Binding.ControlRoot, RuntimeRoot: req.Binding.RuntimeRoot, Command: command,
		ConfigPath: req.Discovery.ConfigPath, Provider: provider, Mode: clientsetup.Managed,
		ExpectedGeneration: req.ExpectedGeneration, ExpectedPolicy: s.ExpectedPolicy, SkillProjection: req.Discovery.Skill,
	}
	facts, err := clientsetup.Inspect(ctx, r)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", ErrPreflight, err)
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.Binding.ControlRoot)
	if err != nil {
		return 0, nil, err
	}
	pending := snap.Ledger.PendingMutation
	if pending != nil && pending.Owner != req.Binding.Owner {
		return 0, nil, fmt.Errorf("%w: pending reservation owned by %s", ErrPreflight, pending.Owner)
	}
	if !facts.Registered && !facts.SkillProjected {
		if pending == nil {
			return req.ExpectedGeneration, nil, nil
		}
		intent, err := ReadIntent(req.Binding.ControlRoot)
		if err != nil {
			return 0, nil, fmt.Errorf("%w: pending handoff intent missing: %v", ErrPreflight, err)
		}
		if !intentMatches(intent, pending.ID, action, string(req.Binding.Integration), req.SourceDigest, req.TreeDigest, req.HelperDigest, req.HelperVersion) {
			return 0, nil, fmt.Errorf("%w: pending %s", ErrIntentConflict, intent.Action)
		}
		return snap.Ledger.Generation, pending, nil
	}
	var res *installruntime.PendingMutation
	gen := req.ExpectedGeneration
	if pending != nil {
		if _, err = os.Lstat(pending.IntentRef); err != nil {
			return 0, nil, fmt.Errorf("%w: pending handoff intent missing: %v", ErrPreflight, err)
		}
		intent, err := ReadIntent(req.Binding.ControlRoot)
		if err != nil {
			return 0, nil, fmt.Errorf("%w: pending handoff intent missing: %v", ErrPreflight, err)
		}
		if !intentMatches(intent, pending.ID, action, string(req.Binding.Integration), req.SourceDigest, req.TreeDigest, req.HelperDigest, req.HelperVersion) {
			return 0, nil, fmt.Errorf("%w: pending %s", ErrIntentConflict, intent.Action)
		}
		res = pending
		gen = snap.Ledger.Generation
	} else {
		var published installruntime.Ledger
		var created *installruntime.PendingMutation
		if action == "install" {
			published, created, err = s.publishHandoffReservation(ctx, req, gen)
		} else {
			published, created, err = s.publishIntent(ctx, req, gen, action, "retire-direct", []string{"direct-mcp"})
		}
		if err != nil {
			return 0, nil, err
		}
		res = created
		gen = published.Generation
	}
	r.ExpectedGeneration = gen
	r.Remove = true
	r.Reservation = res
	result, err := clientsetup.Apply(ctx, r)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: owned discovery handoff: %w", ErrPreflight, policyConflict(err))
	}
	return result.Ledger.Generation, res, nil
}

func (s Service) publishHandoffReservation(ctx context.Context, req Request, gen uint64) (installruntime.Ledger, *installruntime.PendingMutation, error) {
	return s.publishIntent(ctx, req, gen, "install", "retire-direct", []string{"direct-mcp"})
}

func intentConsumer(ledger installruntime.Ledger, runtimeRoot string) (string, string, error) {
	if runtimeRoot == "" {
		return "", "", fmt.Errorf("%w: runtime root required", ErrPreflight)
	}
	if consumer, ok := ledger.Consumers["existing"]; ok && (consumer.RuntimeRoot == "" || consumer.RuntimeRoot == runtimeRoot) {
		return "existing", runtimeRoot, nil
	}
	for id, consumer := range ledger.Consumers {
		if consumer.RuntimeRoot == runtimeRoot {
			return id, runtimeRoot, nil
		}
	}
	// Claude's hooks may move to a new versioned cache while portable bindings
	// retain the old ledger root. After the last portable binding is removed,
	// confirmed-intent publication, patching and finalization may use the sole
	// remaining hook consumer without changing the ledger's retained root.
	if runtimeRoot == ledger.RuntimeRoot && len(ledger.Consumers) == 1 {
		if hooks, ok := ledger.Consumers["claude-hooks"]; ok && filepath.IsAbs(hooks.RuntimeRoot) {
			return "claude-hooks", hooks.RuntimeRoot, nil
		}
	}
	// A completed last-client uninstall keeps the installation and plugin data,
	// but leaves no runtime consumer. Its next confirmed install still needs a
	// durable intent before adding a new consumer.
	if len(ledger.Consumers) == 0 && ledger.ID != "" && ledger.RuntimeRoot == runtimeRoot {
		return "reservation-publisher", runtimeRoot, nil
	}
	return "", "", fmt.Errorf("%w: no consumer at runtime root", ErrPreflight)
}
