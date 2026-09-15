// Package testenv provides a complete disposable environment for filesystem tests.
package testenv

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// Values returns an allowlist suitable for child processes. It never inherits
// credentials or host configuration. Callers add only required executable paths.
func Values(root string) map[string]string {
	m := map[string]string{"HOME": root, "USERPROFILE": root}
	for _, key := range []string{"APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "CODEX_HOME", "CLAUDE_HOME", "CLAUDE_CONFIG_DIR", "TMP", "TEMP", "TMPDIR"} {
		m[key] = filepath.Join(root, key)
	}
	m["CLAUDE_CONFIG_DIR"] = m["CLAUDE_HOME"]
	return m
}

// Set installs isolated paths and clears the explicit notification override.
// Like testing.T.Setenv, it must not be used by parallel tests.
func Set(t *testing.T, root string) {
	t.Helper()
	missingHome := root == ""
	if missingHome {
		root = t.TempDir()
	}
	for k, v := range Values(root) {
		if err := os.MkdirAll(v, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(k, v)
	}
	if missingHome {
		t.Setenv("HOME", "")
		t.Setenv("USERPROFILE", "")
	}
	old, present := os.LookupEnv("AGENT_NOTIFICATIONS_CONFIG")
	if err := os.Unsetenv("AGENT_NOTIFICATIONS_CONFIG"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if present {
			_ = os.Setenv("AGENT_NOTIFICATIONS_CONFIG", old)
		} else {
			_ = os.Unsetenv("AGENT_NOTIFICATIONS_CONFIG")
		}
	})
}

// Env creates disposable paths and a process environment with platform essentials.
func Env(t *testing.T, root string) []string {
	t.Helper()
	values := Values(root)
	for _, value := range values {
		if err := os.MkdirAll(value, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"PATH", "SystemRoot", "WINDIR", "ComSpec", "PATHEXT"} {
		if value, ok := os.LookupEnv(key); ok {
			values[key] = value
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

// Build retains only the caller's explicit Go toolchain/cache settings.
func Build(t *testing.T, root string) []string {
	t.Helper()
	result := Env(t, root)
	for _, key := range []string{"GOROOT", "GOPATH", "GOMODCACHE", "GOCACHE", "GOTMPDIR", "GOPROXY", "GOSUMDB", "GOTOOLCHAIN", "GOMAXPROCS", "CGO_ENABLED", "CC", "CXX"} {
		if value, ok := os.LookupEnv(key); ok {
			result = append(result, key+"="+value)
		}
	}
	return result
}
