package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestMigrationNeverClobbersStable(t *testing.T) {
	for _, stable := range []string{"{broken", `{ "notifications": {"desktop":{"enabled":false}} }`, ""} {
		t.Run(stable, func(t *testing.T) {
			dir := t.TempDir()
			old, target := filepath.Join(dir, "legacy.json"), filepath.Join(dir, "config.json")
			if err := os.WriteFile(old, []byte(`{"debug":{"benchmark":true}}`), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte(stable), 0600); err != nil {
				t.Fatal(err)
			}
			if err := migrateConfig(old, target); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(target)
			if err != nil || string(got) != stable {
				t.Fatalf("stable clobbered: %q, %v", got, err)
			}
		})
	}
}

func TestConcurrentMigrationCreatesOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")
	var wg sync.WaitGroup
	for _, data := range []string{`{"debug":{"benchmark":true}}`, `{"debug":{"benchmark":false}}`} {
		old := filepath.Join(t.TempDir(), "legacy.json")
		if err := os.WriteFile(old, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(old string) {
			defer wg.Done()
			if err := migrateConfig(old, target); err != nil {
				t.Error(err)
			}
		}(old)
	}
	wg.Wait()
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"debug":{"benchmark":true}}` && string(got) != `{"debug":{"benchmark":false}}` {
		t.Fatalf("partial config: %q", got)
	}
}
