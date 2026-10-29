//go:build linux || darwin

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/config"
)

func bootstrapConfigOnlyTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		result[path] = info.Mode().String()
		if !entry.IsDir() {
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			result[path] += fmt.Sprintf(" %x", body)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// The default global preferences may already occupy the managed control-root
// directory after config init. Confirmation/preflight must observe fresh managed
// absence without creating either permanent lock or invoking the selected CLI.
func TestBootstrapIntentAfterConfigInitIsReadOnly(t *testing.T) {
	for _, product := range []string{"gemini", "claude"} {
		t.Run(product, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			xdg := filepath.Join(home, "xdg")
			control := filepath.Join(xdg, "agent-notifications")
			if err := os.MkdirAll(control, 0700); err != nil {
				t.Fatal(err)
			}
			global := filepath.Join(control, "config.json")
			if err := os.WriteFile(global, []byte(`{"notifications":{"desktop":{"enabled":false,"sound":false,"clickToFocus":false}},"TEST":"preserve preferences"}`), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(control, "config.json.lock"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, "canary"), []byte("TEST untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			tools := filepath.Join(home, "tools")
			if err := os.Mkdir(tools, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(tools, product), []byte("TEST inert executable, never invoke"), 0700); err != nil {
				t.Fatal(err)
			}
			env := productEnvironment{Home: home, PATH: tools, DefaultControlRoot: control, Values: map[string]string{}, Config: config.EnvSnapshot{GOOS: "linux", Vars: map[string]string{"HOME": home, "XDG_CONFIG_HOME": xdg}, Lstat: os.Lstat, ReadDir: os.ReadDir}}
			flags := []string{"confirm", "--products", product}
			if product == "claude" {
				flags = append(flags, "--agent-notify", "--navigation", "none", "--allow-unknown-caller", "false", "--allow-caller-asserted", "false")
			} else {
				flags = append(flags, "--desktop")
			}
			args, err := parseSetupProducts(flags)
			if err != nil {
				t.Fatal(err)
			}
			before := bootstrapConfigOnlyTree(t, home)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			provenance := selectorProvenance{Version: "TEST", SourceCommit: strings.Repeat("1", 40), SHA256: strings.Repeat("2", 64), Stage: []byte(home)}
			intent, rows, err := buildConfirmedBootstrapIntent(ctx, args, env, provenance)
			if err != nil {
				t.Fatal(err)
			}
			if intent.Initial.LedgerID != "" || intent.Initial.Owner != "" || intent.Initial.Generation != 0 || intent.Initial.Policy.Exists || len(rows) == 0 || string(intent.Scopes["global-config"]) != global {
				t.Fatalf("config-only prepare invented managed policy/authority: %+v", intent.Initial)
			}
			if err := preflightBootstrapIntent(ctx, intent, args); err != nil {
				t.Fatalf("unchanged config-only confirmation could not preflight: %v", err)
			}
			if after := bootstrapConfigOnlyTree(t, home); !reflect.DeepEqual(before, after) {
				t.Fatalf("prepare/preflight changed fresh profile: before=%v after=%v", before, after)
			}
		})
	}
}
