// Package portablesetup binds a UAP materialization to the existing-installer
// kernel and private locator. It does not invent client identity from HOME/cwd.
package portablesetup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/777genius/agent-notifications/internal/agentnotify/clientsetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

var (
	ErrPreflight           = errors.New("portable setup refused")
	ErrUpdateRequired      = errors.New("existing clients require explicit update before add")
	ErrIntentConflict      = errors.New("pending setup intent conflict")
	ErrExternalUninstall   = errors.New("external uninstall required")
	ErrAlreadyAbsent       = errors.New("portable binding is already absent")
	ErrSourceIdentityDrift = errors.New("confirmed source identity drifted")
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
	DataReceiptID      string
	IntentTargets      []IntentTarget
	Envelope           Envelope
	Discovery          Discovery
	Reservation        *installruntime.PendingMutation
	SourceRevision     string
	SourceDigest       string
	TreeDigest         string
	HelperDigest       string
	HelperVersion      string
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
	if err := b.CheckPrimaryFile(snapshot); err != nil {
		return fmt.Errorf("%w: selected runtime primary is not an owned executable: %v", ErrPreflight, err)
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
	gen, res, err := s.handoffForward(ctx, req, "install")
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
	if !intentMatches(intent, pending.ID, action, string(req.Binding.Integration), req.SourceDigest, req.TreeDigest, req.HelperDigest, req.HelperVersion) {
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
	key, consumer, _, err := req.Binding.Registration()
	if err != nil {
		return portable.Binding{}, err
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.Binding.ControlRoot)
	if err != nil {
		return portable.Binding{}, err
	}
	if existing, ok := snap.Ledger.Consumers[key]; ok && reflect.DeepEqual(existing, consumer) {
		if _, err = portable.Publish(req.Binding); err != nil {
			return portable.Binding{}, err
		}
		return req.Binding, nil
	}
	res, err := s.matchingReservation(req, "install")
	if err != nil {
		return portable.Binding{}, err
	}
	req.Reservation = res
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
	snap, err := installruntime.ReadInstalledSnapshot(req.Binding.ControlRoot)
	if err != nil || snap.Recovery {
		return fmt.Errorf("%w: managed runtime snapshot unavailable", ErrPreflight)
	}
	variants, err := portable.CommittedBindingVariants(snap.Ledger, req.Binding)
	if err != nil || len(variants) > 1 || (len(variants) == 1 && variants[0] != req.Binding) {
		return fmt.Errorf("%w: historical binding is ambiguous or differs from requested identity", ErrPreflight)
	}
	if _, err := portable.ExactLocator(req.Binding); err != nil {
		return err
	}
	res, err := s.matchingReservation(req, "uninstall")
	if err != nil {
		return err
	}
	req.Reservation = res
	return revokeExactBinding(ctx, req, len(variants) == 1)
}

// ReplaceCommittedBinding retires the old locator only after the replacement
// consumer and locator have both been committed. A retry may find the old
// consumer already gone while its exact locator still awaits deletion.
// req.Binding is the replacement; req.Reservation carries the install intent.
func (s Service) ReplaceCommittedBinding(ctx context.Context, old portable.Binding, req Request) error {
	if ctx == nil || old == req.Binding {
		return ErrPreflight
	}
	sameIdentity := old
	sameIdentity.GlobalConfig, sameIdentity.Primary = req.Binding.GlobalConfig, req.Binding.Primary
	if sameIdentity != req.Binding {
		return fmt.Errorf("%w: replacement changes UAP binding identity", ErrPreflight)
	}
	snap, err := installruntime.ReadInstalledSnapshot(old.ControlRoot)
	if err != nil || snap.Recovery {
		return fmt.Errorf("%w: managed runtime snapshot unavailable", ErrPreflight)
	}
	variants, err := portable.CommittedBindingVariants(snap.Ledger, old)
	if err != nil || len(variants) == 0 || len(variants) > 2 {
		return fmt.Errorf("%w: replacement consumer set is invalid", ErrPreflight)
	}
	oldPresent, newPresent := false, false
	for _, variant := range variants {
		switch variant {
		case old:
			oldPresent = true
		case req.Binding:
			newPresent = true
		default:
			return fmt.Errorf("%w: third consumer for UAP binding", ErrPreflight)
		}
	}
	if !newPresent || !portable.ExactCommittedBinding(snap.Ledger, req.Binding) {
		return fmt.Errorf("%w: replacement consumer is not committed", ErrPreflight)
	}
	action, err := checkReplacementIntent(snap.Ledger, old, req)
	if err != nil {
		return err
	}
	newLocator, err := portable.ExactLocator(req.Binding)
	if err != nil || !newLocator {
		return fmt.Errorf("%w: replacement locator is not published", ErrPreflight)
	}
	if _, err := portable.ExactLocator(old); err != nil {
		return err
	}
	res, err := s.matchingReservation(req, action)
	if err != nil {
		return err
	}
	req.Binding, req.Reservation = old, res
	return revokeExactBinding(ctx, req, oldPresent)
}

func checkReplacementIntent(ledger installruntime.Ledger, old portable.Binding, req Request) (string, error) {
	pending := ledger.PendingMutation
	if pending == nil || req.Reservation == nil || !reflect.DeepEqual(*pending, *req.Reservation) {
		return "", fmt.Errorf("%w: replacement lacks a matching reserved intent", ErrPreflight)
	}
	intent, target, err := readMigrationIntentTarget(ledger, old.ControlRoot, old.Integration)
	if err != nil {
		return "", err
	}
	oldKey, _, _, oldErr := old.Registration()
	newKey, _, _, newErr := req.Binding.Registration()
	if oldErr != nil || newErr != nil || target.InstallationID != old.InstallationID || target.BindingID != old.BindingID || target.OldConsumerKey != oldKey || target.NewConsumerKey != newKey || *target.OldBinding != old || *target.NewBinding != req.Binding {
		return "", fmt.Errorf("%w: replacement intent target differs", ErrPreflight)
	}
	return intent.Action, nil
}

func readMigrationIntentTarget(ledger installruntime.Ledger, controlRoot string, integration portable.Integration) (Intent, IntentTarget, error) {
	pending := ledger.PendingMutation
	if pending == nil || pending.Owner != "existing-installer" || pending.IntentRef != IntentPath(controlRoot) {
		return Intent{}, IntentTarget{}, fmt.Errorf("%w: migration reservation missing", ErrPreflight)
	}
	path := IntentPath(controlRoot)
	wantFile, ok := ledger.Files[path]
	currentFile, err := installruntime.Fingerprint(path)
	if err != nil || !ok || !reflect.DeepEqual(wantFile, currentFile) {
		return Intent{}, IntentTarget{}, fmt.Errorf("%w: migration intent bytes changed", ErrPreflight)
	}
	intent, err := ReadIntent(controlRoot)
	if err != nil || intent.SetupIntentID != pending.ID || (intent.Action != "update" && intent.Action != "repair") {
		return Intent{}, IntentTarget{}, fmt.Errorf("%w: migration intent is missing", ErrPreflight)
	}
	var found IntentTarget
	matches := 0
	for _, target := range intent.Targets {
		if target.Client != string(integration) {
			continue
		}
		if target.DataReceiptID == "" || target.OldBinding == nil || target.NewBinding == nil {
			return Intent{}, IntentTarget{}, fmt.Errorf("%w: migration target incomplete", ErrPreflight)
		}
		found = target
		matches++
	}
	if matches != 1 {
		return Intent{}, IntentTarget{}, fmt.Errorf("%w: migration target is missing or ambiguous", ErrPreflight)
	}
	return intent, found, nil
}

func revokeExactBinding(ctx context.Context, req Request, present bool) error {
	key, _, _, err := req.Binding.Registration()
	if err != nil {
		return err
	}
	if present {
		gen := req.ExpectedGeneration
		if _, err = installruntime.Commit(ctx, installruntime.Request{
			ControlRoot: req.Binding.ControlRoot, Owner: req.Binding.Owner, RuntimeRoot: req.Binding.RuntimeRoot,
			ConsumerID: key, RemoveConsumer: true, ExpectedGeneration: &gen, Reservation: req.Reservation,
		}); err != nil {
			return err
		}
	}
	return portable.RevokeLocator(req.Binding)
}

func (s Service) Remove(ctx context.Context, req Request) error {
	if ctx == nil || s.Remover == nil {
		return ErrPreflight
	}
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
	snap, err = installruntime.ReadInstalledSnapshot(req.Binding.ControlRoot)
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
		ExpectedGeneration: req.ExpectedGeneration, SkillProjection: req.Discovery.Skill,
		Reservation: res,
	})
	if err != nil {
		return 0, fmt.Errorf("%w: reverse discovery handoff: %v", ErrPreflight, err)
	}
	return result.Ledger.Generation, nil
}

func (s Service) handoffForward(ctx context.Context, req Request, action string) (uint64, *installruntime.PendingMutation, error) {
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
		return 0, nil, fmt.Errorf("%w: owned discovery handoff: %v", ErrPreflight, err)
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

// PublishConfirmedIntent records the confirmed SetupIntent and kernel
// reservation before live hooks/MCP mutation. It takes the coordinator lease.
func (s Service) PublishConfirmedIntent(ctx context.Context, req ConfirmedIntent) (installruntime.Ledger, *installruntime.PendingMutation, error) {
	if ctx == nil {
		return installruntime.Ledger{}, nil, ErrPreflight
	}
	if req.ControlRoot == "" || req.RuntimeRoot == "" || req.Owner == "" || req.Action == "" || len(req.Targets) == 0 {
		return installruntime.Ledger{}, nil, fmt.Errorf("%w: incomplete confirmed intent", ErrPreflight)
	}
	release, err := installruntime.AcquireCoordinatorLease(ctx, req.ControlRoot)
	if err != nil {
		return installruntime.Ledger{}, nil, err
	}
	defer release()
	if _, err = installruntime.Recover(ctx, req.ControlRoot); err != nil {
		return installruntime.Ledger{}, nil, err
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.ControlRoot)
	if err != nil {
		return installruntime.Ledger{}, nil, err
	}
	if pending := snap.Ledger.PendingMutation; pending != nil {
		intent, readErr := ReadIntent(req.ControlRoot)
		if readErr != nil || !intentMatches(intent, pending.ID, req.Action, req.Targets[0].Client, req.SourceDigest, req.TreeDigest, req.HelperDigest, req.HelperVersion) || (intent.Primary != "" && req.Primary != "" && intent.Primary != req.Primary) || (intent.GlobalConfig != "" && req.GlobalConfig != "" && intent.GlobalConfig != req.GlobalConfig) {
			return installruntime.Ledger{}, nil, fmt.Errorf("%w: pending %s", ErrIntentConflict, intent.Action)
		}
		cp := *pending
		return snap.Ledger, &cp, nil
	}
	consumerID, commitRoot, err := intentConsumer(snap.Ledger, req.RuntimeRoot)
	if err != nil {
		return installruntime.Ledger{}, nil, err
	}
	intentID, err := newIntentID()
	if err != nil {
		return installruntime.Ledger{}, nil, err
	}
	stage := req.Stage
	if stage == "" {
		stage = "confirmed"
	}
	intent := Intent{
		Version:             intentVersion,
		SetupIntentID:       intentID,
		Action:              req.Action,
		Stage:               stage,
		ExpectedGeneration:  snap.Ledger.Generation,
		SourceRevision:      req.SourceRevision,
		SourceDigest:        req.SourceDigest,
		TreeDigest:          req.TreeDigest,
		HelperDigest:        req.HelperDigest,
		HelperVersion:       req.HelperVersion,
		Primary:             req.Primary,
		GlobalConfig:        req.GlobalConfig,
		ExternalUninstalled: req.ExternalUninstalled,
		Targets:             req.Targets,
	}
	payload, err := marshalIntent(intent)
	if err != nil {
		return installruntime.Ledger{}, nil, err
	}
	res := reservationFrom(intent, req.ControlRoot)
	path := IntentPath(req.ControlRoot)
	before, err := installruntime.Fingerprint(path)
	if err != nil {
		return installruntime.Ledger{}, nil, err
	}
	gen := snap.Ledger.Generation
	ledger, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: req.ControlRoot, Owner: req.Owner, RuntimeRoot: commitRoot,
		ConsumerID: consumerID, RefreshOnly: true, ExpectedGeneration: &gen, Reservation: &res,
		Files: []installruntime.File{{Path: path, Before: before, Data: payload, Mode: 0600}},
	})
	if err != nil {
		return installruntime.Ledger{}, nil, fmt.Errorf("%w: publish confirmed intent: %v", ErrPreflight, err)
	}
	return ledger, &res, nil
}

// PatchIntentReceipt records a known UAP data receipt on the pending host
// intent without allocating a new SetupIntentID.
func (s Service) PatchIntentReceipt(ctx context.Context, controlRoot, runtimeRoot, owner, client, receiptID string) error {
	if receiptID == "" || client == "" {
		return nil
	}
	return s.patchIntent(ctx, controlRoot, runtimeRoot, owner, func(intent *Intent, _ installruntime.Ledger) (bool, error) {
		changed := false
		for i, target := range intent.Targets {
			if target.Client != client {
				continue
			}
			if target.DataReceiptID == receiptID {
				return false, nil
			}
			intent.Targets[i].DataReceiptID = receiptID
			changed = true
		}
		return changed, nil
	})
}

// PatchIntentExternalUninstalled records a confirmed Codex native-plugin
// attestation on the pending intent so resume does not require the flag again.
func (s Service) PatchIntentExternalUninstalled(ctx context.Context, controlRoot, runtimeRoot, owner string) error {
	return s.patchIntent(ctx, controlRoot, runtimeRoot, owner, func(intent *Intent, _ installruntime.Ledger) (bool, error) {
		if intent.ExternalUninstalled {
			return false, nil
		}
		intent.ExternalUninstalled = true
		return true, nil
	})
}

// PatchIntentGlobalConfig freezes the path before the first portable consumer
// is committed. Legacy or hooks-only intents may omit it; a later retry must
// match the frozen path exactly.
func (s Service) PatchIntentGlobalConfig(ctx context.Context, controlRoot, runtimeRoot, owner, installationID, path string) error {
	if installationID == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrPreflight
	}
	return s.patchIntent(ctx, controlRoot, runtimeRoot, owner, func(intent *Intent, ledger installruntime.Ledger) (bool, error) {
		installed, found, err := portable.InstalledGlobalConfig(ledger, installationID, controlRoot)
		if err != nil || (found && installed != path) {
			return false, ErrIntentConflict
		}
		if intent.GlobalConfig != "" {
			if intent.GlobalConfig != path {
				return false, ErrIntentConflict
			}
			return false, nil
		}
		intent.GlobalConfig = path
		return true, nil
	})
}

func (s Service) patchIntent(ctx context.Context, controlRoot, runtimeRoot, owner string, mutate func(*Intent, installruntime.Ledger) (bool, error)) error {
	if ctx == nil || controlRoot == "" || mutate == nil {
		return nil
	}
	release, err := installruntime.AcquireCoordinatorLease(ctx, controlRoot)
	if err != nil {
		return err
	}
	defer release()
	return s.patchIntentLocked(ctx, controlRoot, runtimeRoot, owner, mutate)
}

// patchIntentLocked is called only while the coordinator lease is held by the
// enclosing materializer. It avoids reacquiring that lease during recovery.
func (s Service) patchIntentLocked(ctx context.Context, controlRoot, runtimeRoot, owner string, mutate func(*Intent, installruntime.Ledger) (bool, error)) error {
	if ctx == nil || controlRoot == "" || mutate == nil {
		return ErrPreflight
	}
	if _, err := installruntime.Recover(ctx, controlRoot); err != nil {
		return err
	}
	snap, err := installruntime.ReadInstalledSnapshot(controlRoot)
	if err != nil {
		return err
	}
	pending := snap.Ledger.PendingMutation
	if pending == nil {
		return nil
	}
	intent, err := ReadIntent(controlRoot)
	if err != nil {
		return err
	}
	if intent.SetupIntentID != pending.ID {
		return fmt.Errorf("%w: pending %s", ErrIntentConflict, intent.Action)
	}
	changed, err := mutate(&intent, snap.Ledger)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	payload, err := marshalIntent(intent)
	if err != nil {
		return err
	}
	consumerID, commitRoot, err := intentConsumer(snap.Ledger, runtimeRoot)
	if err != nil {
		return err
	}
	path := IntentPath(controlRoot)
	before, err := installruntime.Fingerprint(path)
	if err != nil {
		return err
	}
	if owner == "" {
		owner = snap.Ledger.Owner
	}
	gen := snap.Ledger.Generation
	res := *pending
	_, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: controlRoot, Owner: owner, RuntimeRoot: commitRoot,
		ConsumerID: consumerID, RefreshOnly: true, ExpectedGeneration: &gen, Reservation: &res,
		Files: []installruntime.File{{Path: path, Before: before, Data: payload, Mode: 0600}},
	})
	if err != nil {
		return fmt.Errorf("%w: patch confirmed intent: %v", ErrPreflight, err)
	}
	return nil
}

// FinishConfirmedIntent removes a matching reservation after the whole wizard
// mutation finished. Incomplete HoldOnly/external uninstall leaves it in place.
func (s Service) FinishConfirmedIntent(ctx context.Context, req ConfirmedIntent, res *installruntime.PendingMutation) error {
	if ctx == nil || res == nil {
		return nil
	}
	release, err := installruntime.AcquireCoordinatorLease(ctx, req.ControlRoot)
	if err != nil {
		return err
	}
	defer release()
	if _, err = installruntime.Recover(ctx, req.ControlRoot); err != nil {
		return err
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.ControlRoot)
	if err != nil {
		return err
	}
	if snap.Ledger.PendingMutation == nil {
		return nil
	}
	consumerID := "reservation-finalizer"
	commitRoot := req.RuntimeRoot
	if len(snap.Ledger.Consumers) != 0 {
		consumerID, commitRoot, err = intentConsumer(snap.Ledger, req.RuntimeRoot)
		if err != nil {
			return err
		}
	}
	path := IntentPath(req.ControlRoot)
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
		ControlRoot: req.ControlRoot, Owner: req.Owner, RuntimeRoot: commitRoot,
		ConsumerID: consumerID, RefreshOnly: true, ExpectedGeneration: &gen, Reservation: res, ClearReservation: true,
		Files: files,
	})
	if err != nil {
		return fmt.Errorf("%w: clear confirmed intent: %v", ErrPreflight, err)
	}
	return nil
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
	targets := req.IntentTargets
	if len(targets) == 0 {
		target := IntentTarget{
			Client: string(req.Binding.Integration), BindingID: req.Binding.BindingID,
			InstallationID: req.Binding.InstallationID, DataReceiptID: req.DataReceiptID,
			Profile: req.Profile, Units: units,
		}
		if action == "uninstall" {
			binding := req.Binding
			target.OldBinding, target.OldConsumerKey = &binding, key
		}
		targets = []IntentTarget{target}
	}
	intent := Intent{
		Version:            intentVersion,
		SetupIntentID:      intentID,
		Action:             action,
		Stage:              stage,
		ExpectedGeneration: gen,
		SourceRevision:     req.SourceRevision,
		SourceDigest:       req.SourceDigest,
		TreeDigest:         req.TreeDigest,
		HelperDigest:       req.HelperDigest,
		HelperVersion:      req.HelperVersion,
		Targets:            targets,
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
