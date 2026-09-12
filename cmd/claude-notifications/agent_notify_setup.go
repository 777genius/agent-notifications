package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/777genius/agent-notifications/internal/agentnotify/clientsetup"
	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	notifyruntime "github.com/777genius/agent-notifications/internal/agentnotify/runtime"
	notifysetup "github.com/777genius/agent-notifications/internal/agentnotify/setup"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notifier"
)

const agentNotifySetupHelp = `Usage: claude-notifications setup-notifications OPERATION [OPTIONS]
Operations: configure | prepare | status | register | remove | enable | disable | permission-status | request-permission
Configure requires --provider codex|claude|both and an explicit fresh route.
  --codex-home ABS and --request-permission are configure-only choices.
  Configure resolves primary runtime, generation and canonical global internally.
  --control-root PATH       Existing managed control directory (OS config default)
  --runtime-root PATH       Existing runtime (default: managed ledger runtime)
  --global-config PATH      Canonical hook config (existing installer default)
  --expected-generation N   Required positive generation for every mutation
  --json                    Compact operator result, without configuration bodies
Prepare requires --legacy-config PATH --defaults-config PATH and --expected-generation N.
  It prepares mutable global settings only; registration and enable remain separate.
Register optionally accepts --skill-destination PATH (Codex only).
  The setup caller prepares the physical destination parent before registration.
  Explicit selection refreshes the managed copy; omission preserves it.
Register/remove require:
  --provider codex|claude --config PATH --command PATH
  PATHs must be absolute, clean and physical; command may be a managed stable alias.
Enable optionally replaces the route using ALL FOUR explicit choices:
  --app /physical/path/Codex.app --team-id ABCDE12345
  --allow-unknown-caller true|false --allow-caller-asserted true|false
  Or use --navigation none without --app/--team-id; both consent flags are required.
  This clears app identity. The parser does not imply consent.
  Omit all route choices to preserve the selected route and consent. Rates are preserved.
  Unknown-caller consent extends the local route to indistinguishable callers.
  Caller-asserted consent permits local CLI context; it does not attest a chat.
Status is read-only. Use its generation for one mutation, then read status again.
Only existing-installer ownership is supported. Missing runtime/global settings
require the existing installer runtime; prepare can explicitly prepare global settings.
Register preserves opt-in and client restrictions. Registration is activation-required:
refresh/restart the selected client separately; tool visibility is not confirmed.
Enable records explicit intent, subject to global opt-outs and runtime eligibility.
Disable revokes intent without app/global/journal access and preserves hook registration.
Permission-status reads native settings; request-permission explicitly requests OS
authorization and requires --expected-generation. Both work while disabled, without
client registration, using a valid managed artifact. Existing operations never prompt.
Setup is bounded to 3 minutes; native permission work holds its lease at most 130
seconds. Completion/cancellation does not prove an OS prompt has disappeared.
Uncertain permission is unavailable; read-only permission-status may clarify.
No automatic authorization retry or notification is sent.
  --help                    Show this help without accessing runtime state
`

type agentNotifySetupArgs struct {
	operation  string
	values     map[string]string
	generation uint64
	json       bool
	route      *notifysetup.Route
}

func agentNotifyDefaultNoneArgs() []string {
	return []string{"--navigation", "none", "--allow-unknown-caller", "true", "--allow-caller-asserted", "false"}
}

func parseAgentNotifySetup(args []string) (a agentNotifySetupArgs, help bool, err error) {
	bad := func() (agentNotifySetupArgs, bool, error) { return a, false, errors.New("invalid_arguments") }
	if len(args) > 32 {
		return bad()
	}
	total := 0
	for _, s := range args {
		total += len(s)
		if len(s) > 4096 || !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0 {
			return bad()
		}
	}
	if total > 16384 {
		return bad()
	}
	if len(args) == 1 && args[0] == "--help" {
		return a, true, nil
	}
	if len(args) == 0 {
		return bad()
	}
	a.operation = args[0]
	switch a.operation {
	case "prepare", "status", "register", "remove", "enable", "disable", "permission-status", "request-permission":
	default:
		return bad()
	}
	if len(args) == 2 && args[1] == "--help" {
		return a, true, nil
	}
	a.values = map[string]string{}
	allowed := map[string]bool{"control-root": true, "runtime-root": true}
	if a.operation != "status" && a.operation != "permission-status" {
		allowed["expected-generation"] = true
	}
	if a.operation == "status" || a.operation == "enable" || a.operation == "prepare" {
		allowed["global-config"] = true
	}
	if a.operation == "register" || a.operation == "remove" {
		for _, k := range []string{"provider", "config", "command"} {
			allowed[k] = true
		}
	}
	if a.operation == "prepare" {
		allowed["legacy-config"] = true
		allowed["defaults-config"] = true
	}
	if a.operation == "register" {
		allowed["skill-destination"] = true
	}
	if a.operation == "enable" {
		allowed["navigation"] = true
		for _, k := range []string{"app", "team-id", "allow-unknown-caller", "allow-caller-asserted"} {
			allowed[k] = true
		}
	}
	for i := 1; i < len(args); i++ {
		token := args[i]
		if token == "--json" {
			if a.json {
				return bad()
			}
			a.json = true
			continue
		}
		if !strings.HasPrefix(token, "--") {
			return bad()
		}
		key, value, inline := strings.Cut(token[2:], "=")
		if !allowed[key] {
			return bad()
		}
		if _, ok := a.values[key]; ok {
			return bad()
		}
		if !inline {
			i++
			if i >= len(args) {
				return bad()
			}
			value = args[i]
		}
		if value == "" || strings.HasPrefix(value, "--") {
			return bad()
		}
		a.values[key] = value
	}
	if a.operation != "status" && a.operation != "permission-status" {
		g := a.values["expected-generation"]
		for _, c := range g {
			if c < '0' || c > '9' {
				return bad()
			}
		}
		a.generation, err = strconv.ParseUint(g, 10, 64)
		if err != nil || a.generation == 0 {
			return bad()
		}
	}
	if a.operation == "register" || a.operation == "remove" {
		if a.values["provider"] != "codex" && a.values["provider"] != "claude" {
			return bad()
		}
		if a.values["config"] == "" || a.values["command"] == "" {
			return bad()
		}
	}
	if a.operation == "prepare" && (a.values["legacy-config"] == "" || a.values["defaults-config"] == "") {
		return bad()
	}
	if a.values["skill-destination"] != "" && a.values["provider"] != "codex" {
		return bad()
	}
	for _, k := range []string{"control-root", "runtime-root", "global-config", "config", "command", "app", "legacy-config", "defaults-config", "skill-destination"} {
		if p := a.values[k]; p != "" && (!filepath.IsAbs(p) || filepath.Clean(p) != p) {
			return bad()
		}
	}
	count := 0
	for _, k := range []string{"app", "team-id", "allow-unknown-caller", "allow-caller-asserted"} {
		if a.values[k] != "" {
			count++
		}
	}
	if navigation, present := a.values["navigation"]; present {
		if navigation != "none" || a.values["app"] != "" || a.values["team-id"] != "" || count != 2 {
			return bad()
		}
		for _, k := range []string{"allow-unknown-caller", "allow-caller-asserted"} {
			if a.values[k] != "true" && a.values[k] != "false" {
				return bad()
			}
		}
		a.route = &notifysetup.Route{AllowUnknownCaller: a.values["allow-unknown-caller"] == "true", AllowCallerAsserted: a.values["allow-caller-asserted"] == "true"}
	} else if count != 0 {
		if count != 4 || filepath.Ext(a.values["app"]) != ".app" || len(a.values["app"]) > 1024 || len(a.values["team-id"]) != 10 {
			return bad()
		}
		for _, c := range a.values["team-id"] {
			if !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') {
				return bad()
			}
		}
		for _, k := range []string{"allow-unknown-caller", "allow-caller-asserted"} {
			if a.values[k] != "true" && a.values[k] != "false" {
				return bad()
			}
		}
		a.route = &notifysetup.Route{LocalRouting: true, ApplicationPath: a.values["app"], TeamID: a.values["team-id"], AllowUnknownCaller: a.values["allow-unknown-caller"] == "true", AllowCallerAsserted: a.values["allow-caller-asserted"] == "true"}
	}
	return a, false, nil
}

type agentNotifySetupResult struct {
	GlobalConfiguration string `json:"globalConfiguration,omitempty"`
	Source              string `json:"source,omitempty"`
	Changed             bool   `json:"changed,omitempty"`
	Ready               bool   `json:"ready,omitempty"`
	Reason              string `json:"reason"`
	Generation          uint64 `json:"generation,omitempty"`
	ExplicitIntent      bool   `json:"explicitIntent"`
	RuntimeEligible     bool   `json:"runtimeEligible"`
	Configuration       string `json:"configuration,omitempty"`
	DesktopEnabled      *bool  `json:"desktopEnabled,omitempty"`
	OfflineCapability   string `json:"offlineCapability,omitempty"`
	Permission          string `json:"permission"`
	Activation          string `json:"activation"`
}

// Composition overrides are private to hosted tests. Production always uses the
// real offline verifier and platform clocks; there are no environment bypasses.
type agentNotifySetupComposition struct {
	globalConfigPath func() (string, error)
	setup            func(*notifysetup.Options)
	permission       func(context.Context, string, installruntime.InstalledSnapshot, bool) (string, error)
}

func (c agentNotifySetupComposition) permissionOperation() func(context.Context, string, installruntime.InstalledSnapshot, bool) (string, error) {
	if c.permission != nil {
		return c.permission
	}
	return notifier.SetupPermission
}

// No filesystem access occurs during parsing. Results never include core errors,
// which may contain config fragments or paths. The caller owns the output writer.
func agentNotifySetupExecute(ctx context.Context, args []string, out io.Writer, composition agentNotifySetupComposition) int {
	if len(args) > 0 && args[0] == "configure" {
		if len(args) == 2 && args[1] == "--help" {
			_, e := io.WriteString(out, agentNotifySetupHelp)
			if e != nil {
				return 1
			}
			return 0
		}
		return executeNotificationConfigure(ctx, args[1:], out, "")
	}
	a, help, e := parseAgentNotifySetup(args)
	emit := func(r agentNotifySetupResult, code int) int {
		permissionOp := a.operation == "permission-status" || a.operation == "request-permission"
		if r.Permission == "" {
			r.Permission = "not_checked"
		}
		if permissionOp && code != 0 {
			r.Permission = "unavailable"
		}
		if r.Activation == "" {
			r.Activation = "not_verified"
		}
		var err error
		if a.json {
			if code != 0 && permissionOp {
				err = json.NewEncoder(out).Encode(struct {
					Reason     string `json:"reason"`
					Generation uint64 `json:"generation,omitempty"`
					Permission string `json:"permission"`
				}{r.Reason, r.Generation, r.Permission})
			} else if a.operation == "prepare" && r.Source != "" {
				err = json.NewEncoder(out).Encode(struct {
					agentNotifySetupResult
					Changed bool `json:"changed"`
					Ready   bool `json:"ready"`
				}{r, r.Changed, r.Ready})
			} else if code != 0 && !(a.operation == "status" && r.Configuration != "") {
				err = json.NewEncoder(out).Encode(struct {
					Reason     string `json:"reason"`
					Generation uint64 `json:"generation,omitempty"`
				}{r.Reason, r.Generation})
			} else {
				err = json.NewEncoder(out).Encode(r)
			}
		} else if code != 0 && permissionOp {
			_, err = fmt.Fprintf(out, "%s; generation=%d. Permission: unavailable. Read-only permission-status may clarify; consult setup-notifications --help for prerequisites.\n", r.Reason, r.Generation)
		} else if code != 0 {
			_, err = fmt.Fprintf(out, "%s; generation=%d. Read setup-notifications --help; prepare or repair managed installation/settings with the existing installer, then reread status before retrying.\n", r.Reason, r.Generation)
		} else {
			_, err = fmt.Fprintf(out, "%s; generation=%d; explicit intent=%t; runtime eligibility=%t.\n", r.Reason, r.Generation, r.ExplicitIntent, r.RuntimeEligible)
			if err == nil && r.GlobalConfiguration != "" {
				_, err = fmt.Fprintf(out, "Global configuration: %s.\n", r.GlobalConfiguration)
			}
			if err == nil && r.Source != "" {
				_, err = fmt.Fprintf(out, "Preparation: source=%s; changed=%t; ready=%t.\n", r.Source, r.Changed, r.Ready)
			}
			if err == nil && r.DesktopEnabled != nil {
				_, err = fmt.Fprintf(out, "Configuration: %s. Global desktop: %t. Offline capability: %s.\n", r.Configuration, *r.DesktopEnabled, r.OfflineCapability)
			}
			if err == nil {
				_, err = fmt.Fprintf(out, "Client activation: %s. Permission: %s.\n", r.Activation, r.Permission)
			}
			if err == nil && r.Permission == "denied" {
				_, err = io.WriteString(out, "Review this managed notifier in macOS System Settings > Notifications to change authorization.\n")
			}
			if err == nil && r.Activation == "activation_required" {
				_, err = io.WriteString(out, "Refresh/restart the selected client separately; tool visibility is not confirmed.\n")
			}
		}
		if !a.json && code != 0 && err == nil {
			if r.Configuration != "" {
				_, err = fmt.Fprintf(out, "Configuration: %s. Global configuration: %s. Permission: %s.\n", r.Configuration, r.GlobalConfiguration, r.Permission)
			}
			if r.Source != "" && err == nil {
				_, err = fmt.Fprintf(out, "Preparation: source=%s; changed=%t; ready=%t.\n", r.Source, r.Changed, r.Ready)
			}
		}
		if err != nil {
			return 1
		}
		return code
	}
	if e != nil {
		return emit(agentNotifySetupResult{Reason: "invalid_arguments"}, 2)
	}
	if help {
		if _, e = io.WriteString(out, agentNotifySetupHelp); e != nil {
			return 1
		}
		return 0
	}
	if ctx == nil {
		return emit(agentNotifySetupResult{Reason: "context_required"}, 1)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if ctx.Err() != nil {
		return emit(agentNotifySetupResult{Reason: "canceled"}, 1)
	}
	if a.operation == "prepare" || a.operation == "status" || a.operation == "enable" {
		resolveGlobal := canonicalNotificationGlobalConfig
		if composition.globalConfigPath != nil {
			resolveGlobal = composition.globalConfigPath
		}
		globalPath, globalErr := resolveGlobal()
		if globalErr != nil || (a.values["global-config"] != "" && a.values["global-config"] != globalPath) {
			return emit(agentNotifySetupResult{Reason: "unsupported_global_config"}, 2)
		}
		a.values["global-config"] = globalPath
	}
	root := a.values["control-root"]
	if root == "" {
		root, e = installruntime.ControlRoot()
		if e != nil {
			return emit(agentNotifySetupResult{Reason: "managed_runtime_required"}, 1)
		}
	}
	for _, p := range []string{root, a.values["runtime-root"], a.values["config"], a.values["global-config"], a.values["app"]} {
		if p != "" && !agentNotifySetupPhysical(p) {
			return emit(agentNotifySetupResult{Reason: "physical_path_required"}, 1)
		}
	}
	s, e := installruntime.ReadInstalledSnapshot(root)
	// Revocation is validated by the restricted policy-only kernel transaction.
	// Snapshot can return its ledger with an unavailable native artifact; that
	// must not turn native health into a prerequisite for explicit disable.
	if e != nil && (a.operation != "disable" || s.Ledger.ID == "") {
		return emit(agentNotifySetupResult{Reason: "installation_invalid"}, 1)
	}
	if s.Ledger.ID == "" {
		return emit(agentNotifySetupResult{Reason: "managed_runtime_required"}, 1)
	}
	r := agentNotifySetupResult{Generation: s.Ledger.Generation, RuntimeEligible: s.Enabled}
	if s.Recovery {
		r.Reason = "recovery_required"
		return emit(r, 1)
	}
	if s.Ledger.Owner != clientsetup.Managed {
		r.Reason = "owner_conflict"
		return emit(r, 1)
	}
	runtimeRoot := a.values["runtime-root"]
	if runtimeRoot == "" {
		runtimeRoot = s.Ledger.RuntimeRoot
	}
	if runtimeRoot != s.Ledger.RuntimeRoot {
		r.Reason = "runtime_conflict"
		return emit(r, 1)
	}
	if a.operation == "status" {
		o := notifyruntime.Options{ControlRoot: root, GlobalConfig: a.values["global-config"]}
		b, err := notifyruntime.New(o)
		if err != nil {
			r.Reason = "configuration_invalid"
			return emit(r, 1)
		}
		status := b.Status(ctx)
		_ = b.Close(ctx)
		r.Reason = status.Configuration
		r.Configuration = status.Configuration
		r.ExplicitIntent = status.ExplicitIntent
		global := b.InspectGlobalConfiguration()
		r.GlobalConfiguration = global.Configuration
		r.DesktopEnabled = global.DesktopEnabled
		r.OfflineCapability = status.OfflineCapability
		if ctx.Err() != nil {
			r.Reason = "canceled"
			return emit(r, 1)
		}
		if global.Configuration != "configured" {
			r.Reason = global.Configuration
			return emit(r, 1)
		}
		if status.Configuration == "configuration_invalid" || status.Configuration == "configuration_required" {
			return emit(r, 1)
		}
		return emit(r, 0)
	}
	if a.operation != "permission-status" && a.generation != s.Ledger.Generation {
		r.Reason = "generation_changed"
		return emit(r, 1)
	}
	if ctx.Err() != nil {
		r.Reason = "canceled"
		return emit(r, 1)
	}
	if a.operation == "prepare" {
		global := a.values["global-config"]
		if global == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				r.Reason = "configuration_invalid"
				return emit(r, 1)
			}
			global = filepath.Join(home, ".claude", "claude-notifications-go", "config.json")
		}
		prepared, err := config.PrepareGlobalConfig(ctx, global, a.values["legacy-config"], a.values["defaults-config"])
		r.Source, r.Changed, r.Ready = prepared.Source, prepared.Changed, prepared.Ready
		// Preparation has its own config transaction, never a nested component lock.
		// Observe installation again; a concurrent registration is not rolled back.
		latest, snapshotErr := installruntime.ReadInstalledSnapshot(root)
		r.Generation = latest.Ledger.Generation
		r.RuntimeEligible = latest.Enabled
		policy, policyErr := installruntime.ReadUserPolicy(root)
		r.ExplicitIntent = policy.Enabled
		if err != nil {
			r.Reason = "configuration_invalid"
			if ctx.Err() != nil {
				r.Reason = "canceled"
			}
			return emit(r, 1)
		}
		if snapshotErr != nil || policyErr != nil {
			r.Reason = "preparation_saved_status_unavailable"
			return emit(r, 1)
		}
		if latest.Ledger.Generation != a.generation {
			r.Reason = "generation_changed"
			return emit(r, 1)
		}
		r.Reason = "prepared"
		return emit(r, 0)
	}
	if a.operation == "permission-status" || a.operation == "request-permission" {
		if s.Ledger.Native == nil || s.Ledger.Native.DecoderFloor < 1 {
			r.Reason = "permission_unavailable"
			return emit(r, 1)
		}
		p, err := installruntime.ReadUserPolicy(root)
		if err != nil {
			r.Reason = "configuration_invalid"
			return emit(r, 1)
		}
		r.ExplicitIntent = p.Enabled
		if ctx.Err() != nil {
			r.Reason = "canceled"
			return emit(r, 1)
		}
		permission, err := composition.permissionOperation()(ctx, root, s, a.operation == "request-permission")
		if err != nil || ctx.Err() != nil {
			r.Reason = "permission_unavailable"
			return emit(r, 1)
		}
		switch permission {
		case "allowed", "denied", "undetermined":
			r.Permission = permission
			r.Reason = "permission_" + permission
			return emit(r, 0)
		default:
			r.Reason = "permission_unavailable"
			return emit(r, 1)
		}
	}
	if a.operation == "register" || a.operation == "remove" {
		var projection *clientsetup.SkillProjection
		if destination := a.values["skill-destination"]; destination != "" {
			projection = &clientsetup.SkillProjection{SourcePath: filepath.Join(s.Ledger.RuntimeRoot, "skills", "agent-notify", "SKILL.md"), DestinationPath: destination}
		}
		result, err := clientsetup.Apply(ctx, clientsetup.Request{SkillProjection: projection, ControlRoot: root, RuntimeRoot: runtimeRoot, Command: a.values["command"], ConfigPath: a.values["config"], Provider: registration.Provider(a.values["provider"]), Mode: clientsetup.Managed, ExpectedGeneration: a.generation, Remove: a.operation == "remove"})
		if err != nil {
			r.Reason = agentNotifySetupReason(ctx, err)
			return emit(r, 1)
		}
		r.Generation = result.Ledger.Generation
		r.RuntimeEligible = result.Ledger.Enabled
		r.Reason = "registered"
		r.Activation = "activation_required"
		if a.operation == "remove" {
			r.Reason = "removed"
			r.Activation = "refresh_required"
		}
		// Registration does not write policy. Read intent independently from eligibility.
		p, err := installruntime.ReadUserPolicy(root)
		if err != nil {
			r.Reason = "registration_saved_status_unavailable"
			return emit(r, 1)
		}
		r.ExplicitIntent = p.Enabled
		return emit(r, 0)
	}
	ids := []string{}
	for id, c := range s.Ledger.Consumers {
		if c.RuntimeRoot == runtimeRoot {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		r.Reason = "registered_runtime_required"
		return emit(r, 1)
	}
	o := notifysetup.Options{ControlRoot: root, RuntimeRoot: runtimeRoot, Owner: clientsetup.Managed, ConsumerID: ids[0], GlobalConfig: a.values["global-config"], VerifyApplication: verifyAgentNotifyApplication}
	if composition.setup != nil {
		composition.setup(&o)
	}
	enabled := a.operation == "enable"
	result, err := notifysetup.Apply(ctx, o, notifysetup.Request{ExpectedGeneration: a.generation, Enabled: &enabled, Route: a.route})
	r.Generation = result.Generation
	if err != nil {
		r.Reason = agentNotifySetupReason(ctx, err)
		return emit(r, 1)
	}
	r.Reason = "enabled"
	if !enabled {
		r.Reason = "disabled"
	}
	r.ExplicitIntent = enabled
	r.RuntimeEligible = result.Enabled
	return emit(r, 0)
}

func agentNotifySetupReason(ctx context.Context, err error) string {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "canceled"
	}
	var se *notifysetup.Error
	if errors.As(err, &se) {
		return se.Reason
	}
	if errors.Is(err, clientsetup.ErrRecovery) || errors.Is(err, installruntime.ErrPolicyRecovery) {
		return "recovery_required"
	}
	if errors.Is(err, clientsetup.ErrConflict) || errors.Is(err, registration.ErrConflict) {
		return "registration_conflict"
	}
	if errors.Is(err, registration.ErrInvalid) {
		return "configuration_invalid"
	}
	return "mutation_failed"
}

// Check existing ancestors without creating parents; the core performs its own
// descriptor/identity validation under the commit lock. This is no security lease.
func agentNotifySetupPhysical(p string) bool {
	for {
		actual, e := filepath.EvalSymlinks(p)
		if e == nil {
			return actual == p
		}
		if !os.IsNotExist(e) {
			return false
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		p = parent
	}
}

func agentNotifySetupMain(args []string) (code int) {
	defer func() {
		if recover() != nil {
			code = 1
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	output, e := agentNotifyFile(os.Stdout)
	if e != nil {
		return 1
	}
	defer func() { _ = output.Close(); output.release() }()
	done := make(chan struct{})
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-ctx.Done():
			_ = output.Close()
		case <-done:
		}
	}()
	defer func() { close(done); <-joined }()
	code = agentNotifySetupExecute(ctx, args, output, agentNotifySetupComposition{})
	return code
}

func canonicalNotificationGlobalConfig() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return "", errors.New("physical_path_required")
	}
	return config.GetStableConfigPath()
}
