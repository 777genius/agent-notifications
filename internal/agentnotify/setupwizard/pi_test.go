//go:build linux

package setupwizard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/piauthority"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/agentnotify/portableasset"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestPiWizardReservedHandoffAndFreshConsumer(t *testing.T) {
	control, runtimeRoot, global, primary, generation := managedRuntime(t)
	ctx := testCtx(t)
	ledger, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: piauthority.PreparationConsumer, ExpectedGeneration: &generation})
	if err != nil {
		t.Fatal(err)
	}
	ledger, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, RuntimeRoot: runtimeRoot, Owner: ledger.Owner, ConsumerID: "existing", RemoveConsumer: true, ExpectedGeneration: &ledger.Generation})
	if err != nil {
		t.Fatal(err)
	}
	on, off := true, false
	root := filepath.Dir(control)
	profile := filepath.Join(root, "TEST-pi-agent")
	pkg := filepath.Join(root, "TEST-pi-package")
	if _, err := portableasset.Build(portableasset.BuildRequest{Version: "1.0.0", GOOS: "linux", GOARCH: "amd64", Executable: primary, OutputRoot: pkg}); err != nil {
		t.Fatal(err)
	}
	req := Request{Action: ActionInstall, Agents: []string{"pi"}, Hooks: &off, AgentNotify: &on, Yes: true, PackageRoot: pkg, ControlRoot: control, RuntimeRoot: runtimeRoot, GlobalConfig: global, PiAgentDir: profile, ClientExecutable: primary, Helper: primary, PiDesktop: &off, PiWebhook: &on}
	plan, err := Plan(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatalf("plan: %+v %v", plan.Result, err)
	}
	before, _, err := installruntime.ReadOwnership(control)
	if err != nil || len(before.Consumers) != 1 || before.Consumers[piauthority.PreparationConsumer].RuntimeRoot != runtimeRoot {
		t.Fatal("preparation did not remain truthful before apply", err)
	}
	result, err := Run(ctx, req)
	if err != nil || result.Outcome != "completed" {
		t.Fatalf("run: %+v %v", result, err)
	}
	s, err := installruntime.ReadPolicySnapshot(ctx, control)
	if err != nil {
		t.Fatal(err)
	}
	if s.Policy.Enabled || s.Installation.Enabled {
		t.Fatal("Pi setup enabled unrelated portable MCP policy")
	}
	if s.Installation.Ledger.PendingMutation != nil || len(s.Installation.Ledger.Consumers) != 1 {
		t.Fatalf("unfinished/fake consumer: %+v", s.Installation.Ledger.Consumers)
	}
	var binding portable.Binding
	for key, c := range s.Installation.Ledger.Consumers {
		if key == "claude-hooks" || key == piauthority.PreparationConsumer {
			t.Fatal("preparation/hook consumer survived")
		}
		if json.Unmarshal([]byte(c.Registration), &binding) != nil || binding.Integration != portable.Pi {
			t.Fatal("not a real Pi registration")
		}
	}
	var channels map[string]map[string]bool
	if json.Unmarshal(s.Fields["route"], &channels) != nil || channels[piauthority.ChannelKey]["desktop"] || !channels[piauthority.ChannelKey]["webhook"] {
		t.Fatal("explicit Pi consent was not committed")
	}
	if _, err := os.Stat(filepath.Join(binding.DataRoot, piauthority.RuntimeFile)); err != nil {
		t.Fatal("derived runtime missing", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude")); !os.IsNotExist(err) {
		t.Fatal("Pi setup touched Claude profile", err)
	}
}
