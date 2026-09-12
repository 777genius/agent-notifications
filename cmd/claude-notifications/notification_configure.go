package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/777genius/agent-notifications/internal/agentnotify/clientsetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	notifyruntime "github.com/777genius/agent-notifications/internal/agentnotify/runtime"
	notifysetup "github.com/777genius/agent-notifications/internal/agentnotify/setup"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

type notificationConfigureRequest struct {
	Provider          string
	CodexHome         string
	Route             *notifysetup.Route
	RequestPermission bool
}
type notificationConfigureDependencies struct {
	Home, ClaudeHome, BundleRoot, ControlRoot string
	Composition                               agentNotifySetupComposition
	Inventory                                 func(context.Context, registration.Provider, string) (notificationInventory, error)
}
type notificationConfigureStage struct {
	Name    string `json:"name"`
	Outcome string `json:"outcome"`
}
type notificationConfigureResult struct {
	IntentObserved  bool     `json:"intentObserved"`
	RuntimeObserved bool     `json:"runtimeObserved"`
	Warnings        []string `json:"warnings,omitempty"`
	agentNotifySetupResult
	Stages []notificationConfigureStage `json:"stages"`
}

// Unknown status is encoded as null, never as an assertion of disabled intent.
// Typed callers use the observation bits with the retained last-known values.
func (r notificationConfigureResult) MarshalJSON() ([]byte, error) {
	type plain notificationConfigureResult
	var intent, eligible *bool
	if r.IntentObserved {
		intent = &r.ExplicitIntent
	}
	if r.RuntimeObserved {
		eligible = &r.RuntimeEligible
	}
	return json.Marshal(struct {
		plain
		ExplicitIntent  *bool `json:"explicitIntent"`
		RuntimeEligible *bool `json:"runtimeEligible"`
	}{plain(r), intent, eligible})
}

// configureNotifications composes cores in-process. Each core retains its own
// transaction; completed stages are never blanket-rolled back after a failure.
func configureNotifications(ctx context.Context, request notificationConfigureRequest, deps notificationConfigureDependencies) (result notificationConfigureResult, err error) {
	result.Permission = "not_checked"
	result.Activation = "not_verified"
	stage := "preflight"
	var global string
	fail := func(reason string) error { result.Reason = reason; return errors.New(reason) }
	if ctx == nil {
		return result, fail("context_required")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	defer func() {
		if err != nil {
			if result.Reason == "" {
				result.Reason = agentNotifySetupReason(ctx, err)
			}
			result.Stages = append(result.Stages, notificationConfigureStage{stage, "failed"})
		}
		// Report the persisted canonical file, not the uncommitted preparation
		// candidate. A later user edit may also change desktop eligibility.
		if global != "" {
			if backend, e := notifyruntime.New(notifyruntime.Options{ControlRoot: deps.ControlRoot, GlobalConfig: global}); e == nil {
				observed := backend.InspectGlobalConfiguration()
				result.GlobalConfiguration = observed.Configuration
				result.DesktopEnabled = observed.DesktopEnabled
				_ = backend.Close(ctx)
			}
		}
		// A refresh reports saved progress, never authorizes the next mutation.
		if deps.ControlRoot != "" {
			if s, e := installruntime.ReadInstalledSnapshot(deps.ControlRoot); e == nil {
				result.Generation = s.Ledger.Generation
				result.RuntimeEligible = s.Enabled
				result.RuntimeObserved = true
			}
			if p, e := installruntime.ReadUserPolicy(deps.ControlRoot); e == nil {
				result.ExplicitIntent = p.Enabled
				result.IntentObserved = true
			}
		}
	}()
	if request.Provider != "codex" && request.Provider != "claude" && request.Provider != "both" {
		return result, fail("invalid_arguments")
	}
	for _, p := range []string{deps.Home, deps.BundleRoot, deps.ControlRoot} {
		if !configurePhysical(p) {
			return result, fail("physical_path_required")
		}
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	snapshot, e := installruntime.ReadInstalledSnapshot(deps.ControlRoot)
	if e != nil {
		return result, e
	}
	result.Generation = snapshot.Ledger.Generation
	result.RuntimeEligible = snapshot.Enabled
	result.RuntimeObserved = true
	primary := snapshot.Ledger.RuntimeRoot
	// Bundle defaults must come from an installed consumer, never the process cwd.
	bundleOwned := false
	for _, c := range snapshot.Ledger.Consumers {
		if c.RuntimeRoot == deps.BundleRoot {
			bundleOwned = true
		}
	}
	if !bundleOwned {
		return result, fail("installed_bundle_required")
	}
	global = filepath.Join(deps.Home, ".claude", "claude-notifications-go", "config.json")
	legacy := filepath.Join(primary, "config", "config.json")
	defaults := filepath.Join(deps.BundleRoot, "config", "config.json")
	for _, p := range []string{global, legacy, defaults} {
		if !configurePhysical(p) {
			return result, fail("physical_path_required")
		}
	}
	prepared, e := config.InspectGlobalConfig(ctx, global, legacy, defaults)
	if e != nil {
		return result, fail("configuration_invalid")
	}
	ids := []string{}
	for id, c := range snapshot.Ledger.Consumers {
		if c.RuntimeRoot == primary {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return result, fail("managed_runtime_required")
	}
	options := notifysetup.Options{ControlRoot: deps.ControlRoot, RuntimeRoot: primary, Owner: clientsetup.Managed, ConsumerID: ids[0], GlobalConfig: global, VerifyApplication: verifyAgentNotifyApplication}
	if deps.Composition.setup != nil {
		deps.Composition.setup(&options)
	}
	enabled := true
	setupRequest := notifysetup.Request{ExpectedGeneration: result.Generation, Enabled: &enabled, Route: request.Route}
	if e = notifysetup.Inspect(ctx, options, setupRequest, prepared); e != nil {
		return result, e
	}
	var clients []clientsetup.Request
	var inventories []notificationInventory
	var absentSkills []string
	inventory := deps.Inventory
	if inventory == nil {
		inventory = inspectNotificationInventory
	}
	for _, provider := range []registration.Provider{registration.Codex, registration.Claude} {
		if request.Provider != "both" && request.Provider != string(provider) {
			continue
		}
		home := deps.Home
		path := filepath.Join(home, ".claude.json")
		if provider == registration.Codex {
			home = request.CodexHome
			if home == "" {
				home = filepath.Join(deps.Home, ".codex")
			}
			path = filepath.Join(home, "config.toml")
		} else if deps.ClaudeHome != "" {
			home = deps.ClaudeHome
			path = filepath.Join(home, ".claude.json")
		}
		if !configurePhysical(home) || !configurePhysical(path) {
			return result, fail("physical_path_required")
		}
		inventoryHome := home
		if provider == registration.Claude && deps.ClaudeHome == "" {
			inventoryHome = filepath.Join(deps.Home, ".claude")
		}
		inv, e := inventory(ctx, provider, inventoryHome)
		if e != nil {
			return result, fail("inventory_unknown")
		}
		if inv.State != "clear" {
			return result, fail("inventory_" + inv.State)
		}
		r := clientsetup.Request{ControlRoot: deps.ControlRoot, RuntimeRoot: primary, Command: filepath.Join(primary, "bin", "claude-notifications"), ConfigPath: path, Provider: provider, Mode: clientsetup.Managed, ExpectedGeneration: result.Generation}
		facts, e := clientsetup.Inspect(ctx, r)
		if e != nil {
			return result, e
		}
		if provider == registration.Codex {
			destination := filepath.Join(home, "skills", "agent-notify", "SKILL.md")
			if inv.Skill {
				absentSkills = append(absentSkills, destination)
				if facts.SkillProjected {
					return result, fail("skill_collision")
				}
				if _, e := os.Lstat(destination); !os.IsNotExist(e) {
					return result, fail("skill_collision")
				}
			} else {
				r.SkillProjection = &clientsetup.SkillProjection{SourcePath: filepath.Join(primary, "skills", "agent-notify", "SKILL.md"), DestinationPath: destination}
			}
			if !configurePhysical(destination) {
				return result, fail("physical_path_required")
			}
			if _, e = clientsetup.Inspect(ctx, r); e != nil {
				return result, e
			}
		}
		if inv.DisabledPackage {
			result.Warnings = append(result.Warnings, string(provider)+": later plugin activation requires reconfiguration")
		}
		clients = append(clients, r)
		inventories = append(inventories, inv)
	}
	result.Stages = append(result.Stages, notificationConfigureStage{stage, "ready"})
	fence := func() error {
		if e := ctx.Err(); e != nil {
			return e
		}
		s, e := installruntime.ReadInstalledSnapshot(deps.ControlRoot)
		if e != nil {
			return e
		}
		if s.Recovery || s.Ledger.Generation != result.Generation {
			return fail("generation_changed")
		}
		return nil
	}
	revalidate := func() error {
		for _, inv := range inventories {
			if inv.Revalidate == nil {
				return fail("inventory_unknown")
			}
			if e := inv.Revalidate(ctx); e != nil {
				return fail("inventory_changed")
			}
		}
		if e := fence(); e != nil {
			return e
		}
		for _, path := range absentSkills {
			if !configurePhysical(path) {
				return fail("skill_collision")
			}
			if _, e := os.Lstat(path); !os.IsNotExist(e) {
				return fail("skill_collision")
			}
		}
		for _, r := range clients {
			r.ExpectedGeneration = result.Generation
			if _, e := clientsetup.Inspect(ctx, r); e != nil {
				return e
			}
		}
		return nil
	}
	if e = revalidate(); e != nil {
		return result, e
	}
	stage = "prepare"
	preparation, e := config.PrepareGlobalConfig(ctx, global, legacy, defaults)
	result.Source = preparation.Source
	result.Changed = preparation.Changed
	result.Ready = preparation.Ready
	if e != nil {
		return result, e
	}
	result.Stages = append(result.Stages, notificationConfigureStage{stage, "saved"})
	if e = fence(); e != nil {
		return result, e
	}
	stage = "parents"
	for _, r := range clients {
		paths := []string{r.ConfigPath}
		if r.SkillProjection != nil {
			paths = append(paths, r.SkillProjection.DestinationPath)
		}
		for _, p := range paths {
			if e = configureParents(ctx, p); e != nil {
				return result, e
			}
		}
	}
	result.Stages = append(result.Stages, notificationConfigureStage{stage, "ready"})
	for _, r := range clients {
		stage = "register_" + string(r.Provider)
		if e = revalidate(); e != nil {
			return result, e
		}
		r.ExpectedGeneration = result.Generation
		registered, e := clientsetup.Apply(ctx, r)
		if registered.Ledger.Generation != 0 {
			result.Generation = registered.Ledger.Generation
		}
		if e != nil {
			return result, e
		}
		result.Activation = "activation_required"
		result.Stages = append(result.Stages, notificationConfigureStage{stage, "saved"})
	}
	{
		stage = "permission"
		if e = fence(); e != nil {
			return result, e
		}
		s, e := installruntime.ReadInstalledSnapshot(deps.ControlRoot)
		if e != nil {
			return result, e
		}
		if s.Recovery || s.Ledger.Generation != result.Generation {
			return result, fail("generation_changed")
		}
		result.Permission, e = deps.Composition.permissionOperation()(ctx, deps.ControlRoot, s, request.RequestPermission)
		if e != nil {
			result.Permission = "unavailable"
			if request.RequestPermission || ctx.Err() != nil {
				return result, e
			}
		}
		if result.Permission != "allowed" && result.Permission != "denied" && result.Permission != "undetermined" {
			result.Permission = "unavailable"
		}
		if request.RequestPermission && result.Permission != "allowed" {
			return result, fail("permission_" + result.Permission)
		}
		result.Stages = append(result.Stages, notificationConfigureStage{stage, result.Permission})
	}
	stage = "enable"
	if e = revalidate(); e != nil {
		return result, e
	}
	setupRequest.ExpectedGeneration = result.Generation
	applied, e := notifysetup.Apply(ctx, options, setupRequest)
	if applied.Generation != 0 {
		result.Generation = applied.Generation
	}
	if e != nil {
		return result, e
	}
	result.Stages = append(result.Stages, notificationConfigureStage{stage, "saved"})
	result.Reason = "configured"
	return result, nil
}

func configurePhysical(path string) bool {
	return path != "" && filepath.IsAbs(path) && filepath.Clean(path) == path && agentNotifySetupPhysical(path)
}

// Retain a descriptor for each physical ancestor while creating the next parent.
func configureParents(ctx context.Context, path string) error {
	root, e := os.OpenRoot(string(filepath.Separator))
	if e != nil {
		return e
	}
	defer func() { root.Close() }()
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Dir(path), string(filepath.Separator)), string(filepath.Separator)) {
		if e := ctx.Err(); e != nil {
			return e
		}
		if part == "" {
			continue
		}
		info, e := root.Lstat(part)
		if os.IsNotExist(e) {
			if e = root.Mkdir(part, 0700); e != nil && !os.IsExist(e) {
				return e
			}
			info, e = root.Lstat(part)
		}
		if e != nil {
			return e
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("physical_parent_required")
		}
		next, e := root.OpenRoot(part)
		if e != nil {
			return e
		}
		actual, e := next.Stat(".")
		if e != nil || !os.SameFile(info, actual) {
			next.Close()
			return errors.New("parent_changed")
		}
		root.Close()
		root = next
	}
	return nil
}

func executeNotificationConfigure(ctx context.Context, args []string, out io.Writer, bundle string) int {
	request, jsonOutput, err := parseNotificationConfigure(args)
	if err != nil {
		json.NewEncoder(out).Encode(agentNotifySetupResult{Reason: "invalid_arguments", Permission: "not_checked", Activation: "not_verified"})
		return 2
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return 1
	}
	root, err := installruntime.ControlRoot()
	if err != nil {
		return 1
	}
	if bundle == "" {
		executable, e := os.Executable()
		if e != nil {
			return 1
		}
		executable, e = filepath.EvalSymlinks(executable)
		if e != nil {
			return 1
		}
		bundle = filepath.Dir(filepath.Dir(executable))
	}
	if request.CodexHome == "" {
		request.CodexHome = os.Getenv("CODEX_HOME")
	}
	result, err := configureNotifications(ctx, request, notificationConfigureDependencies{Home: home, ClaudeHome: os.Getenv("CLAUDE_CONFIG_DIR"), BundleRoot: bundle, ControlRoot: root})
	if jsonOutput {
		if e := json.NewEncoder(out).Encode(result); e != nil {
			return 1
		}
	} else {
		json.NewEncoder(out).Encode(result)
	}
	if err != nil {
		return 1
	}
	return 0
}
func parseNotificationConfigure(args []string) (r notificationConfigureRequest, jsonOutput bool, err error) {
	if len(args) > 32 {
		return r, false, errors.New("invalid_arguments")
	}
	total := 0
	for _, arg := range args {
		total += len(arg)
		if len(arg) > 4096 || !utf8.ValidString(arg) || strings.IndexFunc(arg, unicode.IsControl) >= 0 {
			return r, false, errors.New("invalid_arguments")
		}
	}
	if total > 16384 {
		return r, false, errors.New("invalid_arguments")
	}
	route := []string{"enable", "--expected-generation", "1"}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		key, value, inline := strings.Cut(strings.TrimPrefix(args[i], "--"), "=")
		if !strings.HasPrefix(args[i], "--") || seen[key] {
			return r, false, errors.New("invalid_arguments")
		}
		seen[key] = true
		if key == "request-permission" || key == "json" {
			if inline {
				return r, false, errors.New("invalid_arguments")
			}
			if key == "json" {
				jsonOutput = true
			} else {
				r.RequestPermission = true
			}
			continue
		}
		if !inline {
			i++
			if i >= len(args) {
				return r, false, errors.New("invalid_arguments")
			}
			value = args[i]
		}
		switch key {
		case "provider":
			r.Provider = value
		case "codex-home":
			r.CodexHome = value
			if !filepath.IsAbs(value) || filepath.Clean(value) != value {
				return r, false, errors.New("invalid_arguments")
			}
		case "navigation", "app", "team-id", "allow-unknown-caller", "allow-caller-asserted":
			route = append(route, "--"+key, value)
		default:
			return r, false, errors.New("invalid_arguments")
		}
	}
	if r.Provider != "codex" && r.Provider != "claude" && r.Provider != "both" {
		return r, false, errors.New("invalid_arguments")
	}
	a, _, err := parseAgentNotifySetup(route)
	r.Route = a.route
	return r, jsonOutput, err
}
