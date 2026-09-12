package runtime

import (
	"context"
	"github.com/777genius/agent-notifications/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func TestPreparedGlobalUsesRuntimeValidator(t *testing.T) {
	d := t.TempDir()
	c := filepath.Join(d, "config.json")
	legacy := filepath.Join(d, "legacy.json")
	defaults := filepath.Join(d, "defaults.json")
	if e := os.WriteFile(defaults, []byte(`{"notifications":{"desktop":{"enabled":false}}}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := config.PrepareGlobalConfig(context.Background(), c, legacy, defaults); e != nil {
		t.Fatal(e)
	}
	raw, e := readGlobal(c)
	if e != nil {
		t.Fatal(e)
	}
	enabled, sound, focus, e := config.ValidateGlobalDesktop(raw)
	if e != nil || enabled || !sound || !focus {
		t.Fatal(enabled, sound, focus, e)
	}
}
