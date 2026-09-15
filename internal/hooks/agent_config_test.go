package hooks

import (
	"github.com/777genius/agent-notifications/internal/config"
	"testing"
)

func TestConfigAgentCompositionMapping(t *testing.T) {
	for _, tc := range []struct {
		product Product
		want    config.AgentID
	}{{ProductClaude, config.AgentClaude}, {ProductCodex, config.AgentCodex}} {
		got, err := configAgent(tc.product)
		if err != nil || got != tc.want {
			t.Fatalf("%q: %q %v", tc.product, got, err)
		}
	}
	if _, err := configAgent(Product("unknown")); err == nil {
		t.Fatal("unsupported product accepted")
	}
}
