package setupwizard

import (
	"testing"
)

func TestParseCodexPluginListContract(t *testing.T) {
	status, specs := parseCodexPluginList([]byte(`{"ok":true}`))
	if status != codexListUnknown || len(specs) != 0 {
		t.Fatalf("probe contract: %v %v", status, specs)
	}
	status, specs = parseCodexPluginList([]byte(`{"installed":[]}`))
	if status != codexListAbsent || len(specs) != 0 {
		t.Fatalf("empty list: %v %v", status, specs)
	}
	present := `{"installed":[{"pluginId":"agent-notify@managed","name":"agent-notify","marketplaceName":"managed","installed":true,"enabled":true}]}`
	status, specs = parseCodexPluginList([]byte(present))
	if status != codexListPresent || len(specs) != 1 || specs[0] != "agent-notify@managed" {
		t.Fatalf("present: %v %v", status, specs)
	}
	foreign := `{"installed":[{"pluginId":"other@managed","name":"other","marketplaceName":"managed","installed":true,"enabled":true}]}`
	status, specs = parseCodexPluginList([]byte(foreign))
	if status != codexListAbsent || len(specs) != 0 {
		t.Fatalf("foreign: %v %v", status, specs)
	}
}
