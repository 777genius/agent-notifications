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

var ErrPreflight = errors.New("portable setup refused")

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
	gen, err := s.handoffForward(ctx, req)
	if err != nil {
		return portable.Binding{}, err
	}
	req.ExpectedGeneration = gen
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
	if err = s.Activator.Activate(ctx, receipt); err != nil {
		return portable.Binding{}, err
	}
	return req.Binding, nil
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
	_, existed := snap.Ledger.Consumers[key]
	gen := req.ExpectedGeneration
	ledger, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: req.Binding.ControlRoot, Owner: req.Binding.Owner, RuntimeRoot: req.Binding.RuntimeRoot,
		ConsumerID: key, Consumer: consumer, ExpectedGeneration: &gen, RefreshOnly: false,
	})
	if err != nil {
		return portable.Binding{}, err
	}
	if _, err = portable.Publish(req.Binding); err != nil {
		if !existed {
			next := ledger.Generation
			_, _ = installruntime.Commit(ctx, installruntime.Request{
				ControlRoot: req.Binding.ControlRoot, Owner: req.Binding.Owner, RuntimeRoot: req.Binding.RuntimeRoot,
				ConsumerID: key, RemoveConsumer: true, ExpectedGeneration: &next,
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
	key, _, _, err := req.Binding.Registration()
	if err != nil {
		return err
	}
	gen := req.ExpectedGeneration
	if _, err = installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: req.Binding.ControlRoot, Owner: req.Binding.Owner, RuntimeRoot: req.Binding.RuntimeRoot,
		ConsumerID: key, RemoveConsumer: true, ExpectedGeneration: &gen,
	}); err != nil {
		return err
	}
	return portable.RevokeLocator(req.Binding)
}

func (s Service) Remove(ctx context.Context, req Request) error {
	if ctx == nil || s.Remover == nil {
		return ErrPreflight
	}
	if err := s.RevokeBinding(ctx, req); err != nil {
		return err
	}
	if err := s.Remover.Remove(ctx, req.Binding.BindingID); err != nil {
		return err
	}
	if req.Discovery.ConfigPath == "" {
		return nil
	}
	snap, err := installruntime.ReadInstalledSnapshot(req.Binding.ControlRoot)
	if err != nil {
		return err
	}
	req.ExpectedGeneration = snap.Ledger.Generation
	_, err = s.HandoffReverse(ctx, req)
	return err
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
	result, err := clientsetup.Apply(ctx, clientsetup.Request{
		ControlRoot: req.Binding.ControlRoot, RuntimeRoot: req.Binding.RuntimeRoot, Command: command,
		ConfigPath: req.Discovery.ConfigPath, Provider: provider, Mode: clientsetup.Managed,
		ExpectedGeneration: req.ExpectedGeneration, SkillProjection: req.Discovery.Skill,
	})
	if err != nil {
		return 0, fmt.Errorf("%w: reverse discovery handoff: %v", ErrPreflight, err)
	}
	return result.Ledger.Generation, nil
}

func (s Service) handoffForward(ctx context.Context, req Request) (uint64, error) {
	if req.Discovery.ConfigPath == "" {
		return req.ExpectedGeneration, nil
	}
	command := req.Discovery.Command
	if command == "" {
		command = filepath.Join(req.Binding.RuntimeRoot, req.Binding.Primary)
	}
	provider, err := discoveryProvider(req.Binding.Integration)
	if err != nil {
		return 0, err
	}
	r := clientsetup.Request{
		ControlRoot: req.Binding.ControlRoot, RuntimeRoot: req.Binding.RuntimeRoot, Command: command,
		ConfigPath: req.Discovery.ConfigPath, Provider: provider, Mode: clientsetup.Managed,
		ExpectedGeneration: req.ExpectedGeneration, SkillProjection: req.Discovery.Skill,
	}
	facts, err := clientsetup.Inspect(ctx, r)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrPreflight, err)
	}
	if !facts.Registered && !facts.SkillProjected {
		return req.ExpectedGeneration, nil
	}
	r.Remove = true
	result, err := clientsetup.Apply(ctx, r)
	if err != nil {
		return 0, fmt.Errorf("%w: owned discovery handoff: %v", ErrPreflight, err)
	}
	return result.Ledger.Generation, nil
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
