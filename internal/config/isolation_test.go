package config

import (
	"github.com/777genius/agent-notifications/internal/testenv"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "agent-notifications-config-test-")
	if err != nil {
		panic(err)
	}
	if err := prepareTestRoot(root); err != nil {
		panic(err)
	}
	for k, v := range testenv.Values(root) {
		if err := os.MkdirAll(v, 0700); err != nil {
			panic(err)
		}
		if err := os.Setenv(k, v); err != nil {
			panic(err)
		}
	}
	_ = os.Unsetenv("AGENT_NOTIFICATIONS_CONFIG")
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}
