package testenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetIsolatesPaths(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", "/outside-canary")
	Set(t, root)
	if _, ok := os.LookupEnv("AGENT_NOTIFICATIONS_CONFIG"); ok {
		t.Fatal("override inherited")
	}
	for k, want := range Values(root) {
		if os.Getenv(k) != want {
			t.Errorf("%s not isolated", k)
		}
		rel, err := filepath.Rel(root, want)
		if err != nil || rel == ".." {
			t.Fatal("escaped root")
		}
	}
}

func TestProcessEnvironmentAllowlist(t *testing.T) {
	t.Setenv("AGENT_NOTIFICATIONS_CONFIG", "/outside-canary")
	t.Setenv("UNRELATED_SECRET_CANARY", "never-inherit")
	root := t.TempDir()
	for _, env := range [][]string{Env(t, root), Build(t, root)} {
		values := make(map[string]string)
		for _, entry := range env {
			key, value, ok := strings.Cut(entry, "=")
			if !ok {
				t.Fatal("invalid environment entry")
			}
			if _, exists := values[key]; exists {
				t.Fatal("duplicate environment key")
			}
			values[key] = value
		}
		for key, value := range Values(root) {
			if values[key] != value {
				t.Errorf("%s not isolated", key)
			}
		}
		for _, key := range []string{"AGENT_NOTIFICATIONS_CONFIG", "UNRELATED_SECRET_CANARY"} {
			if _, exists := values[key]; exists {
				t.Errorf("inherited %s", key)
			}
		}
	}
}
