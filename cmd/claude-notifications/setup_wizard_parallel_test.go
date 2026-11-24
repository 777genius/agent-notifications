package main

import (
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/internal/agentnotify/setupwizard"
)

func TestBootstrapReadyClientsCannotExpandOrReplaceConfirmedScope(t *testing.T) {
	projection := setupwizard.BootstrapMCPProjection{Profiles: map[string][]byte{
		"claude": []byte("/TEST/claude"), "codex": []byte("/TEST/codex"),
	}}
	selection := &setupwizard.BootstrapMCPSelection{Selected: []string{"claude", "codex"}, Projection: projection}
	req := setupwizard.Request{Agents: []string{"claude", "codex"}, BootstrapMCP: selection}
	if err := admitBootstrapReadyClients(&req, []string{"codex"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(req.Agents, []string{"claude", "codex"}) || req.BootstrapMCP != selection || !reflect.DeepEqual(req.BootstrapMCP.Projection, projection) {
		t.Fatal("ready subset replaced the full scope/projection before revalidation")
	}
	changed := projection
	changed.Profiles = map[string][]byte{"claude": []byte("/TEST/foreign-claude"), "codex": []byte("/TEST/codex")}
	if err := setupwizard.CheckBootstrapMCP(*req.BootstrapMCP, changed); err == nil {
		t.Fatal("omitted client profile drift was hidden by the ready subset")
	}
	for _, bad := range []setupwizard.Request{
		{Agents: []string{"claude"}, BootstrapMCP: selection},
		{Agents: []string{"codex"}},
		{Agents: []string{"cursor"}, BootstrapMCP: selection},
	} {
		if err := admitBootstrapReadyClients(&bad, []string{"codex"}); err == nil {
			t.Fatal("accepted unconfirmed/non-bootstrap client")
		}
	}
}

func TestBootstrapReadyClientsFlagRejectsAmbiguousOrForeignTargets(t *testing.T) {
	for _, args := range [][]string{
		{"--bootstrap-ready-clients"}, {"--bootstrap-ready-clients="},
		{"--bootstrap-ready-clients=claude,claude"}, {"--bootstrap-ready-clients=codex,claude"},
		{"--bootstrap-ready-clients=gemini"},
		{"--bootstrap-ready-clients=claude", "--bootstrap-ready-clients=codex"},
	} {
		if _, _, err := stripBootstrapReadyClients(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	args, ready, err := stripBootstrapReadyClients([]string{"--agents", "claude,codex", "--bootstrap-ready-clients", "codex", "--yes"})
	if err != nil || !reflect.DeepEqual(args, []string{"--agents", "claude,codex", "--yes"}) || !reflect.DeepEqual(ready, []string{"codex"}) {
		t.Fatalf("parse: %v %v %v", args, ready, err)
	}
}
