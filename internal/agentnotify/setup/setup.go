// Package setup is the trusted existing-installer opt-in use case. It accepts no
// notification/model arguments and never registers clients or invokes native.
package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	policyruntime "github.com/777genius/agent-notifications/internal/agentnotify/runtime"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Application is an operator-selected local identity, never a payload route.
type Application struct{ Path, TeamID string }
type Route struct {
	LocalRouting        bool   `json:"localRouting"`
	AllowUnknownCaller  bool   `json:"allowUnknownCaller"`
	AllowCallerAsserted bool   `json:"allowCallerAsserted"`
	ApplicationPath     string `json:"applicationPath"`
	TeamID              string `json:"teamID"`
}

// Rates permits partial defaults exactly as the runtime reader does. A supplied
// zero is invalid; nil members preserve existing values or use reader defaults.
type Rates struct {
	SessionPerMinute *int `json:"sessionPerMinute,omitempty"`
	RuntimePerMinute *int `json:"runtimePerMinute,omitempty"`
	Burst            *int `json:"burst,omitempty"`
}
type Request struct {
	ExpectedGeneration uint64
	Enabled            *bool // nil is repair/preserve, never implicit opt-in
	Route              *Route
	Rates              *Rates
}

// Options are trusted installer composition. VerifyApplication must verify the
// selected local app's identity offline without launching it or prompting. There
// is deliberately no permissive default. Platform is a hosted-test seam.
type Options struct {
	ControlRoot, RuntimeRoot, Owner, ConsumerID, GlobalConfig string
	Platform                                                  string
	JournalClock                                              journal.Clock
	VerifyApplication                                         func(context.Context, Application) error
	// Fault is an inert test seam at durable provisioning boundaries.
	Fault func(string) error
}
type Result struct {
	Generation        uint64
	Namespace, Reason string
	Enabled           bool
}
type Error struct {
	Reason string
	Err    error
}

func (e *Error) Error() string            { return e.Reason + ": " + e.Err.Error() }
func (e *Error) Unwrap() error            { return e.Err }
func fail(reason string, err error) error { return &Error{reason, err} }
func (o Options) fault(phase string) error {
	if o.Fault != nil {
		return o.Fault(phase)
	}
	return nil
}

func (o Options) normalized() (Options, error) {
	var err error
	if o.ControlRoot == "" {
		o.ControlRoot, err = installruntime.ControlRoot()
		if err != nil {
			return o, err
		}
	}
	if o.GlobalConfig == "" {
		home, e := os.UserHomeDir()
		if e != nil {
			return o, e
		}
		o.GlobalConfig = filepath.Join(home, ".claude", "claude-notifications-go", "config.json")
	}
	if o.JournalClock == nil {
		o.JournalClock = journal.PlatformClock{}
	}
	if o.Platform == "" {
		o.Platform = runtime.GOOS
	}
	for _, p := range []string{o.ControlRoot, o.RuntimeRoot, o.GlobalConfig} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return o, fmt.Errorf("absolute clean installer paths required")
		}
	}
	if o.Owner == "" || o.ConsumerID == "" {
		return o, fmt.Errorf("existing owner and consumer required")
	}
	return o, nil
}
func (o Options) kernel(g uint64) installruntime.Request {
	return installruntime.Request{PolicyOnly: true, ControlRoot: o.ControlRoot, RuntimeRoot: o.RuntimeRoot, Owner: o.Owner, ConsumerID: o.ConsumerID, RefreshOnly: true, ExpectedGeneration: &g}
}

// Apply stages private state before the final atomic intent/route/rates commit.
// Failed provisioning never commits enable. Result.Generation is the latest
// committed generation, including a durable initialization reservation on error.
func Apply(ctx context.Context, o Options, r Request) (result Result, err error) {
	result.Generation = r.ExpectedGeneration
	if ctx == nil {
		return result, fail("invalid_setup", fmt.Errorf("context required"))
	}
	// Explicit setup verifies the selected application before provisioning and
	// again before activation. Full bundle verification may take tens of seconds;
	// this human setup budget is independent of notify's 15-second deadline.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	o, err = o.normalized()
	if err != nil {
		return result, fail("invalid_setup", err)
	}
	if r.ExpectedGeneration == 0 {
		return result, fail("generation_required", fmt.Errorf("read the managed installation generation first"))
	}
	if err = installruntime.CheckPrivateControlRoot(o.ControlRoot); err != nil {
		return result, fail("managed_runtime_required", err)
	}
	// Revocation bypasses app, global config and journal availability entirely.
	if r.Enabled != nil && !*r.Enabled {
		if r.Route != nil || r.Rates != nil {
			return result, fail("invalid_setup", fmt.Errorf("disable separately from route/rates edits"))
		}
		k := o.kernel(r.ExpectedGeneration)
		k.PolicyEnabled = r.Enabled
		l, e := installruntime.Commit(ctx, k)
		if l.Generation != 0 {
			result.Generation = l.Generation
		}
		result.Enabled = l.Enabled
		result.Reason = "disabled"
		if e != nil {
			return result, commitError(e)
		}
		return result, nil
	}
	if o.Platform != "darwin" {
		return result, fail("unsupported_platform", fmt.Errorf("explicit setup supports macOS"))
	}
	// Read and validate before creating even the setup lock. Missing canonical
	// global configuration must leave all roots and settings byte-for-byte intact.
	s, pre, e := readSnapshot(ctx, o.ControlRoot)
	if e != nil {
		return result, fail("managed_runtime_required", e)
	}
	if e = qualified(s, o, r.ExpectedGeneration); e != nil {
		return result, e
	}
	fields, e := changes(r)
	if e != nil {
		return result, e
	}
	candidate, e := preview(s, fields, r.Enabled)
	if e != nil {
		return result, e
	}
	if e = o.validate(ctx, candidate); e != nil {
		return result, e
	}
	unlock, e := installruntime.Lock(ctx, filepath.Join(o.ControlRoot, ".setup.lock"))
	if e != nil {
		return result, fail("state_unavailable", e)
	}
	defer unlock()
	// This lock serializes setup journal work only. Journal locks are released
	// before any component/config transaction; they are never nested.
	current, now, e := readSnapshot(ctx, o.ControlRoot)
	if e != nil {
		return result, fail("managed_runtime_required", e)
	}
	if now != pre || current.Installation.Ledger.Generation != r.ExpectedGeneration {
		return result, fail("generation_changed", fmt.Errorf("reread setup status before retry"))
	}
	if candidate.Policy.Enabled {
		current, pre, result.Namespace, e = provision(ctx, o, current, pre, &result)
		if e != nil {
			return result, e
		}
	}
	candidate, e = preview(current, fields, r.Enabled)
	if e != nil {
		return result, e
	}
	k := o.kernel(current.Installation.Ledger.Generation)
	k.ExpectedPolicy = &pre
	k.PolicyFields = fields
	k.PolicyEnabled = r.Enabled
	// Global managed writers share this config lock. Revalidate the exact
	// candidate under that lock immediately before the kernel's CAS transaction.
	k.ConfigPaths = []string{o.GlobalConfig}
	k.Prepare = func() ([]installruntime.File, error) {
		if candidate.Policy.Enabled {
			if e := checkProvisioned(o, candidate); e != nil {
				return nil, e
			}
		}
		return nil, o.validate(ctx, candidate)
	}
	l, e := installruntime.Commit(ctx, k)
	if e != nil {
		return result, commitError(e)
	}
	result.Generation = l.Generation
	result.Enabled = l.Enabled
	result.Reason = "configured"
	if !l.Enabled {
		result.Reason = "disabled"
	}
	return result, nil
}
func readSnapshot(ctx context.Context, root string) (installruntime.PolicySnapshot, installruntime.Identity, error) {
	if e := checkPolicy(root); e != nil {
		return installruntime.PolicySnapshot{}, installruntime.Identity{}, e
	}
	s, e := installruntime.ReadPolicySnapshot(ctx, root)
	if e == nil && s.Preimage.Exists && s.Preimage.Mode != 0600 {
		e = fmt.Errorf("explicit policy must remain private")
	}
	return s, s.Preimage, e
}

func qualified(s installruntime.PolicySnapshot, o Options, g uint64) error {
	l := s.Installation.Ledger
	if s.Installation.Recovery {
		return fail("recovery_required", fmt.Errorf("finish the managed installation transaction first"))
	}
	if l.ID == "" || l.Owner != o.Owner || l.Generation != g {
		return fail("generation_changed", fmt.Errorf("expected existing owner and generation"))
	}
	if l.WriterFloor > installruntime.WriterFloor {
		return fail("newer_installer_required", fmt.Errorf("installed writer floor is newer"))
	}
	registered := false
	for _, c := range l.Consumers {
		if c.RuntimeRoot == o.RuntimeRoot {
			registered = true
		}
	}
	if !registered || l.Native == nil || l.Native.DecoderFloor < 1 {
		return fail("qualified_runtime_required", fmt.Errorf("install a qualified managed native runtime first"))
	}
	return nil
}
func changes(r Request) (map[string]json.RawMessage, error) {
	m := map[string]json.RawMessage{}
	if r.Route != nil {
		b, e := json.Marshal(r.Route)
		if e != nil {
			return nil, e
		}
		m["route"] = b
	}
	if r.Rates != nil {
		b, e := json.Marshal(r.Rates)
		if e != nil {
			return nil, e
		}
		m["rates"] = b
	}
	return m, nil
}
func preview(s installruntime.PolicySnapshot, patch map[string]json.RawMessage, enabled *bool) (installruntime.PolicySnapshot, error) {
	out := s
	out.Fields = map[string]json.RawMessage{}
	for k, v := range s.Fields {
		out.Fields[k] = append(json.RawMessage(nil), v...)
	}
	for k, v := range patch {
		base := map[string]json.RawMessage{}
		if old, ok := out.Fields[k]; ok {
			if json.Unmarshal(old, &base) != nil || base == nil {
				return out, fail("configuration_invalid", fmt.Errorf("invalid %s", k))
			}
		}
		var change map[string]json.RawMessage
		if json.Unmarshal(v, &change) != nil || change == nil {
			return out, fail("configuration_invalid", fmt.Errorf("invalid %s", k))
		}
		for member, value := range change {
			base[member] = value
		}
		out.Fields[k], _ = json.Marshal(base)
	}
	out.Fields["schemaVersion"] = json.RawMessage("1")
	if enabled != nil {
		out.Policy.Enabled = *enabled
	}
	out.Fields["enabled"], _ = json.Marshal(out.Policy.Enabled)
	return out, nil
}
func (o Options) validate(ctx context.Context, s installruntime.PolicySnapshot) error {
	return o.validatePrepared(ctx, s, nil)
}
func (o Options) validatePrepared(ctx context.Context, s installruntime.PolicySnapshot, prepared []byte) error {
	// Setup always requires the canonical global file; pure disable returned
	// earlier. Runtime disabled reads may skip it, but setup must not create
	// a missing parent/lock through a later config transaction.
	enabled := true
	validation, e := preview(s, nil, &enabled)
	if e != nil {
		return e
	}
	var p agentnotify.Policy
	if prepared == nil {
		p, e = policyruntime.ValidateSetupPolicy(validation, o.GlobalConfig)
	} else {
		p, e = policyruntime.ValidatePreparedSetupPolicy(validation, prepared)
	}
	if errors.Is(e, agentnotify.ErrConfigurationRequired) {
		return fail("configuration_required", fmt.Errorf("prepare the canonical global config with the existing installer: %w", e))
	}
	if e != nil {
		return fail("configuration_invalid", e)
	}
	if p.Route.LocalRouting {
		a := Application{p.Route.ApplicationPath, p.Route.TeamID}
		if !origin.Text(a.Path, 1024, true) || !filepath.IsAbs(a.Path) || filepath.Clean(a.Path) != a.Path || filepath.Ext(a.Path) != ".app" || len(a.Path) > 1024 || len(a.TeamID) != 10 {
			return fail("invalid_route", fmt.Errorf("select an absolute local .app and its verified ten-character team ID"))
		}
		for _, c := range a.TeamID {
			if !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') {
				return fail("invalid_route", fmt.Errorf("invalid team ID"))
			}
		}
		if o.VerifyApplication == nil {
			return fail("application_verification_required", fmt.Errorf("installer must verify the selected local application offline"))
		}
		if e = o.VerifyApplication(ctx, a); e != nil {
			return fail("application_identity_invalid", e)
		}
	} else if p.Route.ApplicationPath != "" || p.Route.TeamID != "" {
		return fail("invalid_route", fmt.Errorf("application identity requires explicit local routing"))
	}
	if _, present := s.Fields["route"]; s.Policy.Enabled && !present {
		return fail("route_required", fmt.Errorf("choose a verified local application or explicit navigation none before enabling"))
	}
	return nil
}

func commitError(err error) error {
	if errors.Is(err, installruntime.ErrPolicyRecovery) {
		return fail("recovery_required", err)
	}
	var setupErr *Error
	if errors.As(err, &setupErr) {
		return err
	}
	return fail("policy_commit_failed", err)
}

// Inspect validates the same candidate as Apply before configuration preparation.
// It performs no provisioning and preserves the caller's generation fence.
func Inspect(ctx context.Context, o Options, r Request, preparedGlobal []byte) error {
	if ctx == nil {
		return fail("invalid_setup", fmt.Errorf("context required"))
	}
	var err error
	o, err = o.normalized()
	if err != nil {
		return err
	}
	if o.Platform != "darwin" {
		return fail("unsupported_platform", fmt.Errorf("explicit setup supports macOS"))
	}
	s, _, err := readSnapshot(ctx, o.ControlRoot)
	if err != nil {
		return err
	}
	if err = qualified(s, o, r.ExpectedGeneration); err != nil {
		return err
	}
	fields, err := changes(r)
	if err != nil {
		return err
	}
	candidate, err := preview(s, fields, r.Enabled)
	if err != nil {
		return err
	}
	return o.validatePrepared(ctx, candidate, preparedGlobal)
}
