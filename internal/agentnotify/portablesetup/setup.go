// Package portablesetup binds a UAP materialization to the existing-installer
// kernel and private locator. It does not invent client identity from HOME/cwd.
package portablesetup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/777genius/agent-notifications/internal/agentnotify/clientsetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

var (
	ErrPreflight         = errors.New("portable setup refused")
	ErrUpdateRequired    = errors.New("existing clients require explicit update before add")
	ErrIntentConflict    = errors.New("pending setup intent conflict")
	ErrExternalUninstall = errors.New("external uninstall required")
)

// Envelope is the root MCP command projected for one explicit integration.
type Envelope struct {
	Command string
	Args    []string
	Cwd     string
}

// Receipt is the committed UAP binding/data identity. Tests may supply a
// synthetic listing runner; that is not installed-client activation evidence.
type Receipt struct {
	BindingID, DataReceiptID, DataRoot string
	LocatorArg                         string
}

type Stager interface {
	Stage(ctx context.Context, env Envelope) (Receipt, error)
}
type Activator interface {
	Activate(ctx context.Context, receipt Receipt) error
}
type Remover interface {
	Remove(ctx context.Context, bindingID string) error
}

// Discovery names the exact owned global MCP/user-skill surface to retire
// before portable projection. Empty ConfigPath skips handoff. Unowned
// conflicts refuse before any portable mutation.
type Discovery struct {
	ConfigPath string
	Command    string
	Skill      *clientsetup.SkillProjection
}

type Request struct {
	Binding            portable.Binding
	ExpectedGeneration uint64
	Envelope           Envelope
	Discovery          Discovery
	Reservation        *installruntime.PendingMutation
	SourceRevision     string
	SourceDigest       string
	// Profile is the resolved client config root for this target. Empty keeps
	// the published handoff intent without a profile path.
	Profile string
}

type Service struct {
	Stager    Stager
	Activator Activator
	Remover   Remover
}

func decorate(env Envelope, name string) Envelope {
	out := env
	out.Args = []string{"portable-launch", "--locator", name}
	return out
}

func (s Service) preflight(b portable.Binding) error {
	if _, _, _, err := b.Registration(); err != nil {
		return err
	}
	snapshot, err := installruntime.ReadInstalledSnapshot(b.ControlRoot)
	if err != nil {
		return fmt.Errorf("%w: managed runtime prerequisite missing", ErrPreflight)
	}
	if snapshot.Recovery {
		return installruntime.ErrPolicyRecovery
	}
	if snapshot.Ledger.ID != b.ComponentID || snapshot.Ledger.Owner != b.Owner || snapshot.Ledger.RuntimeRoot != b.RuntimeRoot {
		return fmt.Errorf("%w: binding does not match managed runtime", ErrPreflight)
	}
	return nil
}

func (s Service) Install(ctx context.Context, req Request) (portable.Binding, error) {
	if ctx == nil {
		return portable.Binding{}, ErrPreflight
	}
	if err := s.preflight(req.Binding); err != nil {
		return portable.Binding{}, err
	}
	if req.Discovery.ConfigPath != "" {
		release, err := installruntime.AcquireCoordinatorLease(ctx, req.Binding.ControlRoot)
		if err != nil {
			return portable.Binding{}, err
		}
		defer release()
		if _, err = installruntime.Recover(ctx, req.Binding.ControlRoot); err != nil {
			return portable.Binding{}, err
		}
		snap, err := installruntime.ReadInstalledSnapshot(req.Binding.ControlRoot)
		if err != nil {
			return portable.Binding{}, err
		}
		req.ExpectedGeneration = snap.Ledger.Generation
	}
	gen, res, err := s.handoffForward(ctx, req)
	if err != nil {
		return portable.Binding{}, err
	}
	req.ExpectedGeneration = gen
	req.Reservation = res
	name, err := req.Binding.Filename()
	if err != nil {
		return portable.Binding{}, err
	}
	if s.Stager == nil || s.Activator == nil {
		return portable.Binding{}, ErrPreflight
	}
	receipt, err := s.Stager.Stage(ctx, decorate(req.Envelope, name))
	if err != nil {
		return portable.Binding{}, err
	}
	if receipt.DataRoot != req.Binding.DataRoot || receipt.LocatorArg != name || receipt.BindingID != req.Binding.BindingID {
		return portable.Binding{}, fmt.Errorf("%w: materializer receipt does not match binding", ErrPreflight)
	}
	if _, err = s.CommitBinding(ctx, req); err != nil {
		return portable.Binding{}, err
	}
	if err = s.finishHandoff(ctx, req, res); err != nil {
		return portable.Binding{}, err
	}
	if err = s.Activator.Activate(ctx, receipt); err != nil {
		return portable.Binding{}, err
	}
	return req.Binding, nil
}

func (s Service) matchingReservation(req Request, action string) (*installruntime.PendingMutation, error) {
	if req.Reservation != nil {
		return req.Reservation, nil
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.Binding.ControlRoot)
	if err != nil {
		return nil, err
	}
	pending := snap.Ledger.PendingMutation
	if pending == nil {
		return nil, nil
	}
	if pending.Owner != req.Binding.Owner {
		return nil, fmt.Errorf("%w: pending reservation owned by %s", ErrPreflight, pending.Owner)
	}
	intent, err := ReadIntent(req.Binding.ControlRoot)
	if err != nil {
		return nil, fmt.Errorf("%w: pending handoff intent missing: %v", ErrPreflight, err)
	}
	if !intentMatches(intent, pending.ID, action, string(req.Binding.Integration), req.SourceDigest) {
		return nil, fmt.Errorf("%w: pending %s", ErrIntentConflict, intent.Action)
	}
	cp := *pending
	return &cp, nil
}

func (s Service) CommitBinding(ctx context.Context, req Request) (portable.Binding, error) {
	if ctx == nil {
		return portable.Binding{}, ErrPreflight
	}
	if err := s.preflight(req.Binding); err != nil {
		return portable.Binding{}, err
	}
	res, err := s.matchingReservation(req, "install")
	if err != nil {
		return portable.Binding{}, err
	}
	req.Reservation = res
	key, consumer, _, err := req.Binding.Registration()
	if err != nil {
		return portable.Binding{}, err
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.Binding.ControlRoot)
	if err != nil {
		return portable.Binding{}, err
	}
	_, existed := snap.Ledger.Consumers[key]
	gen := req.ExpectedGeneration
	ledger, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: req.Binding.ControlRoot, Owner: req.Binding.Owner, RuntimeRoot: req.Binding.RuntimeRoot,
		ConsumerID: key, Consumer: consumer, ExpectedGeneration: &gen, RefreshOnly: false, Reservation: res,
	})
	if err != nil {
		return portable.Binding{}, err
	}
	if _, err = portable.Publish(req.Binding); err != nil {
		if !existed {
			next := ledger.Generation
			_, _ = installruntime.Commit(ctx, installruntime.Request{
				ControlRoot: req.Binding.ControlRoot, Owner: req.Binding.Owner, RuntimeRoot: req.Binding.RuntimeRoot,
				ConsumerID: key, RemoveConsumer: true, ExpectedGeneration: &next, Reservation: res,
			})
		}
		return portable.Binding{}, err
	}
	return req.Binding, nil
}

func (s Service) RevokeBinding(ctx context.Context, req Request) error {
	if ctx == nil {
		return ErrPreflight
	}
	if err := s.preflight(req.Binding); err != nil {
		return err
	}
	res, err := s.matchingReservation(req, "uninstall")
	if err != nil {
		return err
	}
	req.Reservation = res
	key, _, _, err := req.Binding.Registration()
	if err != nil {
		return err
	}
	gen := req.ExpectedGeneration
	if _, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: req.Binding.ControlRoot, Owner: req.Binding.Owner, RuntimeRoot: req.Binding.RuntimeRoot,
		ConsumerID: key, RemoveConsumer: true, ExpectedGeneration: &gen, Reservation: res,
	}); err != nil {
		return err
	}
	return portable.RevokeLocator(req.Binding)
}

func (s Service) Remove(ctx context.Context, req Request) error {
	if ctx == nil || s.Remover == nil {
		return ErrPreflight
	}
	if req.Discovery.ConfigPath != "" {
		release, err := installruntime.AcquireCoordinatorLease(ctx, req.Binding.ControlRoot)
		if err != nil {
			return err
		}
		defer release()
		if _, err = installruntime.Recover(ctx, req.Binding.ControlRoot); err != nil {
			return err
		}
		snap, err := installruntime.ReadInstalledSnapshot(req.Binding.ControlRoot)
		if err != nil {
			return err
		}
		req.ExpectedGeneration = snap.Ledger.Generation
	}
	res, err := s.matchingReservation(req, "uninstall")
	if err != nil {
		return err
	}
	if res == nil {
		published, created, err := s.publishIntent(ctx, req, req.ExpectedGeneration, "uninstall", "revoke-locator", []string{"direct-mcp"})
		if err != nil {
			return err
		}
		req.ExpectedGeneration = published.Generation
		res = created
	}
	req.Reservation = res
	if err := s.RevokeBinding(ctx, req); err != nil {
		return err
	}
	if err := s.Remover.Remove(ctx, req.Binding.BindingID); err != nil {
		return err
	}
	if req.Discovery.ConfigPath == "" {
		return s.finishHandoff(ctx, req, res)
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.Binding.ControlRoot)
	if err != nil {
		return err
	}
	req.ExpectedGeneration = snap.Ledger.Generation
	if _, err = s.HandoffReverse(ctx, req); err != nil {
		return err
	}
	return s.finishHandoff(ctx, req, res)
}

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
		command = filepath.Join(req.Binding.RuntimeRoot, req.Binding.Primary)
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
		ExpectedGeneration: req.ExpectedGeneration, SkillProjection: req.Discovery.Skill,
		Reservation: res,
	})
	if err != nil {
		return 0, fmt.Errorf("%w: reverse discovery handoff: %v", ErrPreflight, err)
	}
	return result.Ledger.Generation, nil
}

func (s Service) handoffForward(ctx context.Context, req Request) (uint64, *installruntime.PendingMutation, error) {
	if req.Discovery.ConfigPath == "" {
		return req.ExpectedGeneration, nil, nil
	}
	command := req.Discovery.Command
	if command == "" {
		command = filepath.Join(req.Binding.RuntimeRoot, req.Binding.Primary)
	}
	provider, err := discoveryProvider(req.Binding.Integration)
	if err != nil {
		return 0, nil, err
	}
	r := clientsetup.Request{
		ControlRoot: req.Binding.ControlRoot, RuntimeRoot: req.Binding.RuntimeRoot, Command: command,
		ConfigPath: req.Discovery.ConfigPath, Provider: provider, Mode: clientsetup.Managed,
		ExpectedGeneration: req.ExpectedGeneration, SkillProjection: req.Discovery.Skill,
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
		if !intentMatches(intent, pending.ID, "install", string(req.Binding.Integration), req.SourceDigest) {
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
		if !intentMatches(intent, pending.ID, "install", string(req.Binding.Integration), req.SourceDigest) {
			return 0, nil, fmt.Errorf("%w: pending %s", ErrIntentConflict, intent.Action)
		}
		res = pending
		gen = snap.Ledger.Generation
	} else {
		published, created, err := s.publishHandoffReservation(ctx, req, gen)
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
		return 0, nil, fmt.Errorf("%w: owned discovery handoff: %v", ErrPreflight, err)
	}
	return result.Ledger.Generation, res, nil
}

func (s Service) publishHandoffReservation(ctx context.Context, req Request, gen uint64) (installruntime.Ledger, *installruntime.PendingMutation, error) {
	return s.publishIntent(ctx, req, gen, "install", "retire-direct", []string{"direct-mcp"})
}

func (s Service) publishIntent(ctx context.Context, req Request, gen uint64, action, stage string, units []string) (installruntime.Ledger, *installruntime.PendingMutation, error) {
	key, _, _, err := req.Binding.Registration()
	if err != nil {
		return installruntime.Ledger{}, nil, err
	}
	intentID, err := newIntentID()
	if err != nil {
		return installruntime.Ledger{}, nil, err
	}
	intent := Intent{
		Version:            intentVersion,
		SetupIntentID:      intentID,
		Action:             action,
		Stage:              stage,
		ExpectedGeneration: gen,
		SourceRevision:     req.SourceRevision,
		SourceDigest:       req.SourceDigest,
		Targets: []IntentTarget{{
			Client:         string(req.Binding.Integration),
			BindingID:      req.Binding.BindingID,
			InstallationID: req.Binding.InstallationID,
			Profile:        req.Profile,
			Units:          units,
		}},
	}
	payload, err := marshalIntent(intent)
	if err != nil {
		return installruntime.Ledger{}, nil, err
	}
	res := reservationFrom(intent, req.Binding.ControlRoot)
	path := IntentPath(req.Binding.ControlRoot)
	before, err := installruntime.Fingerprint(path)
	if err != nil {
		return installruntime.Ledger{}, nil, err
	}
	ledger, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: req.Binding.ControlRoot, Owner: req.Binding.Owner, RuntimeRoot: req.Binding.RuntimeRoot,
		ConsumerID: key, RefreshOnly: true, ExpectedGeneration: &gen, Reservation: &res,
		Files: []installruntime.File{{Path: path, Before: before, Data: payload, Mode: 0600}},
	})
	if err != nil {
		return installruntime.Ledger{}, nil, fmt.Errorf("%w: publish %s reservation: %v", ErrPreflight, action, err)
	}
	return ledger, &res, nil
}

func (s Service) finishHandoff(ctx context.Context, req Request, res *installruntime.PendingMutation) error {
	if res == nil {
		return nil
	}
	key, _, _, err := req.Binding.Registration()
	if err != nil {
		return err
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.Binding.ControlRoot)
	if err != nil {
		return err
	}
	if snap.Ledger.PendingMutation == nil {
		return nil
	}
	path := IntentPath(req.Binding.ControlRoot)
	before, err := installruntime.Fingerprint(path)
	if err != nil {
		return err
	}
	var files []installruntime.File
	if before.Exists {
		files = []installruntime.File{{Path: path, Before: before, Remove: true}}
	}
	gen := snap.Ledger.Generation
	_, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: req.Binding.ControlRoot, Owner: req.Binding.Owner, RuntimeRoot: req.Binding.RuntimeRoot,
		ConsumerID: key, RefreshOnly: true, ExpectedGeneration: &gen, Reservation: res, ClearReservation: true,
		Files: files,
	})
	if err != nil {
		return fmt.Errorf("%w: clear handoff reservation: %v", ErrPreflight, err)
	}
	return nil
}

func discoveryProvider(i portable.Integration) (registration.Provider, error) {
	switch i {
	case portable.Codex:
		return registration.Codex, nil
	case portable.Claude:
		return registration.Claude, nil
	default:
		return "", ErrPreflight
	}
}

func SharedDataSibling(dataRoot, otherBindingID string) string {
	return filepath.Join(dataRoot, otherBindingID)
}
