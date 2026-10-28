package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/clients/cursor"
)

func TestCursorCommonSelectionDefaultsAndChannelPairChoices(t *testing.T) {
	for _, products := range []string{"cursor", "qoder,cursor", "codex,qoder,cursor", "claude,codex,cursor"} {
		args, err := parseSetupProducts([]string{"confirm", "--products", products, "--desktop=false", "--webhook=true"})
		if err != nil || !args.DesktopSet || args.Desktop || !args.WebhookSet || !args.Webhook {
			t.Fatalf("common selection %s: %+v, %v", products, args, err)
		}
	}
	for _, tc := range []struct {
		flags            []string
		desktop, webhook bool
	}{
		{nil, true, true}, {[]string{"--desktop=false"}, false, false}, {[]string{"--webhook=false"}, false, false},
		{[]string{"--desktop"}, true, false}, {[]string{"--webhook"}, false, true},
	} {
		a, err := parseSetupProducts(append([]string{"confirm", "--products", "cursor"}, tc.flags...))
		if err != nil {
			t.Fatal(err)
		}
		d, w, err := resolveBootstrapChannels(a, "cursor", []byte(`{"cursorNotifications":{"desktop":true,"webhook":true}}`))
		if err != nil || d != tc.desktop || w != tc.webhook {
			t.Fatalf("channel pair %v = %v,%v: %v", tc.flags, d, w, err)
		}
	}
	e, a := setupProductsDiscoveryFixture(t)
	cli := filepath.Join(e.PATH, fixtureProductName("cursor-agent"))
	if err := os.WriteFile(cli, []byte("TEST inert; never execute discovery"), 0o700); err != nil {
		t.Fatal(err)
	}
	facts, scopes, err := discoverProducts(context.Background(), a, e)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := cursor.New().ResolveProfileRoot(filepath.Join(e.Home, ".cursor"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range facts {
		if fact.ID == "cursor" && (!fact.Present || fact.Profile != profile || scopes["scope-root"] != profile || scopes["client-executable"] != fact.Executable) {
			t.Fatalf("default Cursor authority lost: %+v, %v", fact, scopes)
		}
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatalf("discovery created Cursor profile: %v", err)
	}
}

func TestImplicitCursorProfileFailureKeepsOtherProductsSelectable(t *testing.T) {
	e, a := setupProductsDiscoveryFixture(t)
	profile := filepath.Join(e.Home, ".cursor")
	if err := os.WriteFile(profile, []byte("TEST not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	facts, scopes, err := discoverProducts(context.Background(), a, e)
	if err != nil {
		t.Fatal(err)
	}
	if scopes["scope-root"] != "" || scopes["client-executable"] != "" {
		t.Fatal("invalid ambient Cursor profile acquired authority", scopes)
	}
	for _, fact := range facts {
		if fact.ID == "cursor" && (fact.Selectable || fact.Reason == "") {
			t.Fatal("invalid Cursor profile offered without a reason", fact)
		}
		if fact.ID == "claude" && !fact.Selectable {
			t.Fatal("ambient Cursor profile blocked Claude", fact)
		}
	}
	a.Products = []string{"cursor"}
	if _, _, err := discoverProducts(context.Background(), a, e); err == nil {
		t.Fatal("explicit Cursor selection ignored its invalid profile")
	}
	a.Products = nil
	a.Scopes = map[string]string{"scope-root": profile}
	if _, _, err := discoverProducts(context.Background(), a, e); err == nil {
		t.Fatal("explicit profile authority was silently discarded")
	}
}

func TestCursorExecutableAuthorityFollowsSelectedProfile(t *testing.T) {
	for _, product := range []string{"claude", "qoder", "cursor"} {
		t.Run(product, func(t *testing.T) {
			e, _ := setupProductsDiscoveryFixture(t)
			a, err := parseSetupProducts([]string{"confirm", "--products", product})
			if err != nil {
				t.Fatal(err)
			}
			cli := filepath.Join(e.PATH, fixtureProductName("cursor-agent"))
			if err := os.WriteFile(cli, []byte("TEST inert Cursor agent"), 0o700); err != nil {
				t.Fatal(err)
			}
			facts, scopes, err := discoverProducts(context.Background(), a, e)
			if err != nil {
				t.Fatal(err)
			}
			for _, fact := range facts {
				if fact.ID == "cursor" && (!fact.Present || fact.Executable == "") {
					t.Fatal("unselected CLI presence was lost", fact)
				}
			}
			if product == "cursor" {
				if scopes["scope-root"] == "" || scopes["client-executable"] == "" {
					t.Fatal("selected Cursor lost profile or CLI authority", scopes)
				}
			} else if scopes["scope-root"] != "" || scopes["client-executable"] != "" {
				t.Fatal("unselected Cursor leaked into confirmation authority", scopes)
			}
		})
	}
}

func TestPreservedCursorUnitProjectsSkippedInsteadOfCompleted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	body, err := json.Marshal(setupwizard.Result{Action: "install", Outcome: "unchanged", Reason: "existing_opt_out", Targets: preservedUnitTargets([]string{"cursor"})})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := writeInstallerResult(path, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "cursor\tMCP\tskipped\n") || strings.Contains(out.String(), "completed") {
		t.Fatal("preserved Cursor unit projected as installed", out.String())
	}
}

func TestBootstrapWizardChildKeepsOriginalScopeAndProjection(t *testing.T) {
	parent, _ := bootstrapCodecFixture(t)
	parent.Request.Products = []string{"claude", "codex", "qoder", "cursor"}
	parent.MCP = setupwizard.BootstrapMCPSelection{Selected: []string{"claude", "cursor"}, Skipped: []string{"codex"}, Projection: setupwizard.BootstrapMCPProjection{InstallationID: "TEST-original", Profiles: map[string][]byte{"claude": []byte("/TEST/claude"), "codex": []byte("/TEST/codex"), "cursor": []byte("/TEST/.cursor")}, Bindings: []setupwizard.BootstrapMCPBinding{{Client: "claude", ID: "TEST-claude"}, {Client: "cursor", ID: "TEST-cursor"}}}}
	parent.Units = []bootstrapProductUnits{{Product: "claude", MCP: true}, {Product: "codex", PreservedOff: true}, {Product: "qoder", Native: true}, {Product: "cursor", MCP: true, Desktop: true}}
	before, _ := json.Marshal(parent)
	for _, agents := range [][]string{{"claude", "codex"}, {"cursor"}} {
		child, err := bootstrapWizardChild(parent, agents)
		if err != nil || !reflect.DeepEqual(child.Request.Products, agents) || len(child.MCP.Projection.Profiles) != len(agents) || !reflect.DeepEqual(child.Initial, parent.Initial) {
			t.Fatalf("child %v: %+v %v", agents, child, err)
		}
		for _, binding := range child.MCP.Projection.Bindings {
			if !containsProduct(agents, binding.Client) {
				t.Fatal("sibling binding entered child")
			}
		}
		if child.MCP.Projection.InstallationID != parent.MCP.Projection.InstallationID {
			t.Fatal("child replaced original installation identity")
		}
	}
	for _, invalid := range [][]string{nil, {"claude"}, {"codex"}, {"qoder"}, {"claude", "cursor"}, {"cursor", "codex"}} {
		if _, err := bootstrapWizardChild(parent, invalid); err == nil {
			t.Fatalf("unconfirmed child admitted: %v", invalid)
		}
	}
	after, _ := json.Marshal(parent)
	if !bytes.Equal(before, after) {
		t.Fatal("child derivation modified immutable parent")
	}
}
