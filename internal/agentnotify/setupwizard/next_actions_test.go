package setupwizard

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portablesetup"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestUpdateRequiredLiveMixedIsUpdateOnly(t *testing.T) {
	req := Request{Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true}
	got, err := updateRequired(req, portable.Codex, nil, []string{"codex"}, nil, Result{Action: "install"}, ErrRefused)
	if err != ErrRefused || got.Reason != "update_required" {
		t.Fatalf("live mixed: %+v %v", got, err)
	}
	if len(got.NextActions) != 1 || got.NextActions[0].Kind != "update" || strings.Join(got.NextActions[0].Agents, ",") != "codex" || got.NextActions[0].Reason != "update_existing" {
		t.Fatalf("live mixed next: %+v", got.NextActions)
	}
	if cmd := strings.Join(got.NextActions[0].Command, " "); !strings.Contains(cmd, "--action update") || !strings.Contains(cmd, "--agents codex") || strings.Contains(cmd, "2-add") {
		t.Fatalf("live mixed argv: %v", got.NextActions[0].Command)
	}
	text := annotateRequiredUpdate("plan", got)
	if !strings.Contains(text, "required-update=codex") || strings.Contains(text, "2-add:") {
		t.Fatalf("live mixed plan text: %s", text)
	}
}

func TestUpdateRequiredBothLiveBehindIsUpdateOnly(t *testing.T) {
	req := Request{Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true}
	got, err := updateRequired(req, portable.Claude, nil, []string{"claude", "codex"}, nil, Result{Action: "install"}, ErrRefused)
	if err != ErrRefused || got.Reason != "update_required" {
		t.Fatalf("both behind: %+v %v", got, err)
	}
	if len(got.NextActions) != 1 || got.NextActions[0].Kind != "update" || strings.Join(got.NextActions[0].Agents, ",") != "claude,codex" || got.NextActions[0].Reason != "update_existing" {
		t.Fatalf("both behind next: %+v", got.NextActions)
	}
	if cmd := strings.Join(got.NextActions[0].Command, " "); !strings.Contains(cmd, "--action update") || !strings.Contains(cmd, "--agents claude,codex") || strings.Contains(cmd, "--action install") {
		t.Fatalf("both behind argv: %v", got.NextActions[0].Command)
	}
	text := annotateRequiredUpdate("plan", got)
	if !strings.Contains(text, "required-update=claude,codex") || strings.Contains(text, "2-add:") {
		t.Fatalf("both behind plan text: %s", text)
	}
}

func TestUpdateRequiredCodexLiveHybridAddsClaude(t *testing.T) {
	req := Request{Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true}
	got, err := updateRequired(req, portable.Codex, nil, []string{"codex"}, []string{"claude"}, Result{Action: "install"}, ErrRefused)
	if err != ErrRefused || len(got.NextActions) != 2 {
		t.Fatalf("codex hybrid: %+v %v", got, err)
	}
	if got.NextActions[0].Kind != "update" || strings.Join(got.NextActions[0].Agents, ",") != "codex" || got.NextActions[0].Reason != "update_existing_before_add" {
		t.Fatalf("codex hybrid update: %+v", got.NextActions[0])
	}
	if got.NextActions[1].Kind != "install" || strings.Join(got.NextActions[1].Agents, ",") != "claude" || got.NextActions[1].Reason != "add_after_update" {
		t.Fatalf("codex hybrid add: %+v", got.NextActions[1])
	}
	text := annotateRequiredUpdate("plan", got)
	if !strings.Contains(text, "required-update=codex") || !strings.Contains(text, "2-add:claude") {
		t.Fatalf("codex hybrid plan text: %s", text)
	}
}

func TestUpdateRequiredHybridKeepsAddOfUnbound(t *testing.T) {
	req := Request{Action: ActionInstall, Agents: []string{"claude", "codex"}, Yes: true}
	got, err := updateRequired(req, portable.Claude, nil, []string{"claude"}, []string{"codex"}, Result{Action: "install"}, ErrRefused)
	if err != ErrRefused || got.Reason != "update_required" {
		t.Fatalf("hybrid: %+v %v", got, err)
	}
	if len(got.NextActions) != 2 || got.NextActions[0].Kind != "update" || got.NextActions[1].Kind != "install" {
		t.Fatalf("hybrid next: %+v", got.NextActions)
	}
	if strings.Join(got.NextActions[0].Agents, ",") != "claude" || got.NextActions[0].Reason != "update_existing_before_add" {
		t.Fatalf("hybrid update: %+v", got.NextActions[0])
	}
	if strings.Join(got.NextActions[1].Agents, ",") != "codex" || got.NextActions[1].Reason != "add_after_update" {
		t.Fatalf("hybrid add: %+v", got.NextActions[1])
	}
	if cmd := strings.Join(got.NextActions[0].Command, " "); !strings.Contains(cmd, "--action update") || !strings.Contains(cmd, "--agents claude") {
		t.Fatalf("hybrid update argv: %v", got.NextActions[0].Command)
	}
	if cmd := strings.Join(got.NextActions[1].Command, " "); !strings.Contains(cmd, "--action install") || !strings.Contains(cmd, "--agents codex") {
		t.Fatalf("hybrid add argv: %v", got.NextActions[1].Command)
	}
	text := annotateRequiredUpdate("plan", got)
	if !strings.Contains(text, "required-update=claude") || !strings.Contains(text, "2-add:codex") {
		t.Fatalf("hybrid plan text: %s", text)
	}
}

func TestUpdateRequiredAbsentSecondClientIsTwoPhase(t *testing.T) {
	req := Request{Action: ActionInstall, Agents: []string{"codex"}, Yes: true}
	got, err := updateRequired(req, portable.Codex, []string{"claude"}, nil, nil, Result{Action: "install"}, ErrRefused)
	if err != ErrRefused || len(got.NextActions) != 2 {
		t.Fatalf("two-phase: %+v %v", got, err)
	}
	if got.NextActions[0].Kind != "update" || strings.Join(got.NextActions[0].Agents, ",") != "claude" || got.NextActions[0].Reason != "update_existing_before_add" {
		t.Fatalf("two-phase update: %+v", got.NextActions[0])
	}
	if got.NextActions[1].Kind != "install" || strings.Join(got.NextActions[1].Agents, ",") != "codex" || got.NextActions[1].Reason != "add_after_update" {
		t.Fatalf("two-phase add: %+v", got.NextActions[1])
	}
	if cmd := strings.Join(got.NextActions[0].Command, " "); !strings.Contains(cmd, "--action update") || !strings.Contains(cmd, "--agents claude") || strings.Contains(cmd, "codex") {
		t.Fatalf("unselected Claude must not be silently grouped: %v", got.NextActions[0].Command)
	}
	if cmd := strings.Join(got.NextActions[1].Command, " "); !strings.Contains(cmd, "--action install") || !strings.Contains(cmd, "--agents codex") {
		t.Fatalf("two-phase add argv: %v", got.NextActions[1].Command)
	}
	text := annotateRequiredUpdate("plan", got)
	if !strings.Contains(text, "required-update=claude") || !strings.Contains(text, "2-add:codex") {
		t.Fatalf("two-phase plan text: %s", text)
	}
}

func TestLiveNotifyClientEmptyIDSkipsStore(t *testing.T) {
	mat := portablesetup.Materializer{}
	if liveNotifyClient(mat, "", "claude") || liveNotifyClient(mat, "id", "") {
		t.Fatal("empty identity loaded store")
	}
	unbound := unboundSelectedAgents(Request{Agents: []string{"claude", "codex"}}, mat, portablesetup.Identity{}, portable.Codex)
	if strings.Join(unbound, ",") != "claude,codex" {
		t.Fatalf("unbound: %v", unbound)
	}
}

func TestOfferPostSetupActionsRetainedUpdateSkipsDelivery(t *testing.T) {
	req := Request{Action: ActionUpdate, ControlRoot: filepath.Join(t.TempDir(), "control"), Agents: []string{"codex"}}
	out := Result{
		Action: "update", Outcome: "completed", Reason: "retained_source_updated", Generation: 2,
		NextActions: []NextAction{{Kind: "data_compatibility", Reason: "warn"}},
	}
	got := offerPostSetupActions(req, []portable.Integration{portable.Codex}, out, false)
	for _, next := range got.NextActions {
		if next.Kind == "request-permission" || next.Kind == "test-notification" {
			t.Fatalf("retained update offered delivery: %+v", got.NextActions)
		}
	}
	if len(got.NextActions) != 1 || got.NextActions[0].Kind != "data_compatibility" {
		t.Fatalf("retained update next: %+v", got.NextActions)
	}
}

func TestOfferPostSetupActionsCompletedInstallOffersDelivery(t *testing.T) {
	req := Request{Action: ActionInstall, ControlRoot: filepath.Join(t.TempDir(), "control"), Agents: []string{"codex"}}
	out := Result{
		Action: "install", Outcome: "completed", Generation: 2,
		Targets: []TargetResult{{Client: "codex", Unit: "agent-notify", Outcome: "installed"}},
	}
	got := offerPostSetupActions(req, []portable.Integration{portable.Codex}, out, true)
	kinds := map[string]bool{}
	for _, next := range got.NextActions {
		kinds[next.Kind] = true
	}
	if !kinds["restart-client"] || !kinds["request-permission"] || !kinds["test-notification"] {
		t.Fatalf("completed install next: %+v", got.NextActions)
	}
}

func TestOfferPostSetupActionsInspectSkipsPermission(t *testing.T) {
	req := Request{Action: ActionInspect, ControlRoot: filepath.Join(t.TempDir(), "control"), Agents: []string{"codex"}}
	out := Result{
		Action: "inspect", Outcome: "completed", Generation: 2,
		Targets: []TargetResult{{Client: "codex", Unit: "agent-notify", Outcome: "installed"}},
	}
	got := offerPostSetupActions(req, []portable.Integration{portable.Codex}, out, false)
	for _, next := range got.NextActions {
		if next.Kind == "request-permission" {
			t.Fatalf("inspect offered permission dialog: %+v", got.NextActions)
		}
	}
}

func TestOfferPostSetupActionsUninstallSkipsDelivery(t *testing.T) {
	req := Request{Action: ActionUninstall, ControlRoot: filepath.Join(t.TempDir(), "control"), Agents: []string{"codex"}}
	out := Result{
		Action: "uninstall", Outcome: "completed", Generation: 2,
		Targets: []TargetResult{{Client: "codex", Unit: "agent-notify", Outcome: "installed"}},
	}
	got := offerPostSetupActions(req, []portable.Integration{portable.Codex}, out, true)
	for _, next := range got.NextActions {
		if next.Kind == "request-permission" || next.Kind == "test-notification" || next.Kind == "restart-client" {
			t.Fatalf("uninstall offered delivery: %+v", got.NextActions)
		}
	}
}

func commitControlRuntime(t *testing.T) (ctx context.Context, control string) {
	t.Helper()
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	control = filepath.Join(root, "control")
	runtime := filepath.Join(root, "runtime")
	primary := filepath.Join(runtime, "primary")
	if _, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: "existing",
		Files: []installruntime.File{{Path: primary, Data: []byte("inert"), Mode: 0700}},
	}); err != nil {
		t.Fatal(err)
	}
	return ctx, control
}

func TestRunEmptyAgentsIsInvalidWithoutIntent(t *testing.T) {
	ctx, control := commitControlRuntime(t)
	for _, action := range []Action{ActionInstall, ActionUpdate, ActionRepair, ActionUninstall} {
		got, err := Run(ctx, Request{Action: action, Yes: true, ControlRoot: control})
		if err == nil || got.Outcome != "invalid" || got.Reason != "agents_required" || got.ExitCode() != 2 {
			t.Fatalf("%s empty agents: %+v %v", action, got, err)
		}
	}
	plan, err := Plan(ctx, Request{Action: ActionInstall, ControlRoot: control})
	if err == nil || plan.Ready || plan.Result.Outcome != "invalid" || plan.Result.Reason != "agents_required" {
		t.Fatalf("empty agents plan: %+v %v", plan, err)
	}
}

func TestInspectOmittedAgentsStillReportsBoth(t *testing.T) {
	ctx, control := commitControlRuntime(t)
	got, err := Run(ctx, Request{Action: ActionInspect, ControlRoot: control})
	if err != nil || got.Outcome != "completed" || got.Reason == "agents_required" || got.ExitCode() != 0 {
		t.Fatalf("omitted inspect: %+v %v", got, err)
	}
	saw := map[string]string{}
	for _, target := range got.Targets {
		if target.Unit == "agent-notify" {
			saw[target.Client] = target.Outcome
		}
	}
	if saw["claude"] != "absent" || saw["codex"] != "absent" {
		t.Fatalf("omitted inspect clients: %+v", got.Targets)
	}
	plan, err := Plan(ctx, Request{Action: ActionInspect, ControlRoot: control})
	if err != nil || !plan.Ready || plan.Result.Reason == "agents_required" || plan.Result.ExitCode() != 0 {
		t.Fatalf("omitted inspect plan: %+v %v", plan, err)
	}
}
