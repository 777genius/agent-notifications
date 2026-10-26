//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Cancellation must leave the pruned installation untouched; acceptance must
// restore it before the ordinary confirmation captures its generation.
func TestSetupProductsRepairPrunedCache(t *testing.T) {
	for _, accept := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "restore"}[accept], func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			control := filepath.Join(base, "control")
			cache := filepath.Join(base, "plugins", "cache", "claude-notifications-go", "claude-notifications-go")
			oldRoot, newRoot := filepath.Join(cache, "1.46.1"), filepath.Join(cache, "1.49.4")
			oldFile, newFile := filepath.Join(oldRoot, "bin", "TEST-runtime"), filepath.Join(newRoot, "bin", "TEST-runtime")
			ctx := setupCommandContext(t)
			payload := []byte("TEST managed executable" + installruntime.WriterProtocolMarker)
			if _, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, RuntimeRoot: oldRoot, Owner: "existing-installer", ConsumerID: "TEST-retained",
				Files: []installruntime.File{{Path: oldFile, Data: payload, Mode: 0700}}}); err != nil {
				t.Fatal(err)
			}
			before, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, RuntimeRoot: newRoot, Owner: "existing-installer", ConsumerID: "claude-hooks",
				Files: []installruntime.File{{Path: newFile, Data: payload, Mode: 0700}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(oldRoot); err != nil {
				t.Fatal(err)
			}
			ledgerPath := filepath.Join(control, "ownership.json")
			ledgerBefore, err := os.ReadFile(ledgerPath)
			if err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(base, "TEST-control-alias")
			if err := os.Symlink(control, alias); err != nil {
				t.Fatal(err)
			}
			answer := "n\n"
			if accept {
				answer = "y\n"
			}
			var output, prompts bytes.Buffer
			code := runSetupProducts([]string{"repair-cache", "--control-root", alias, "--plain", "--bootstrap"}, strings.NewReader(answer), &output, &prompts)
			if code != 0 || !strings.Contains(prompts.String(), "Restore missing notification files?") {
				t.Fatalf("repair code=%d out=%q prompts=%q", code, output.String(), prompts.String())
			}
			if !accept {
				after, err := os.ReadFile(ledgerPath)
				if err != nil || !bytes.Equal(after, ledgerBefore) || output.Len() != 0 {
					t.Fatalf("cancel changed ledger or authorized continuation: %v %q", err, output.String())
				}
				if _, err := os.Lstat(oldRoot); !os.IsNotExist(err) {
					t.Fatalf("cancel recreated cache: %v", err)
				}
				return
			}
			after, err := installruntime.ReadInstalledSnapshot(control)
			if err != nil || output.String() != "ready\n" || after.Ledger.Generation != before.Generation+1 || !reflect.DeepEqual(after.Ledger.Consumers, before.Consumers) {
				t.Fatalf("restored installation: %v %q %+v", err, output.String(), after)
			}
			// Healthy runs need no recovery approval, even without input.
			output.Reset()
			prompts.Reset()
			if code := runSetupProducts([]string{"repair-cache", "--control-root", control}, strings.NewReader(""), &output, &prompts); code != 0 || output.String() != "ready\n" || prompts.Len() != 0 {
				t.Fatalf("healthy installation prompted: %d %q %q", code, output.String(), prompts.String())
			}
		})
	}
}

func TestSetupProductsRepairArguments(t *testing.T) {
	for _, args := range [][]string{{"select", "--yes"}, {"select", "--bootstrap"}, {"select", "--defer-claude-relocation"}, {"repair-cache", "--products", "claude"}, {"repair-cache", "--intent-file", "/TEST/intent"}, {"repair-cache", "--yes=true"}} {
		if _, err := parseSetupProducts(args); err == nil {
			t.Fatalf("accepted incompatible repair arguments: %v", args)
		}
	}
}

func TestSetupProductsRepairPreservesSoleClaudeRelocation(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	control := filepath.Join(base, "control")
	runtime := filepath.Join(base, "plugins", "cache", "claude-notifications-go", "claude-notifications-go", "1.46.1")
	file := filepath.Join(runtime, "TEST-file")
	if _, err := installruntime.Commit(setupCommandContext(t), installruntime.Request{ControlRoot: control, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: "claude-hooks",
		Files: []installruntime.File{{Path: file, Data: []byte("owned"), Mode: 0700}}}); err != nil {
		t.Fatal(err)
	}
	// Claude may restore marketplace bytes before our explicit upgrade retires
	// this sole cache. Repair must not block or overwrite that existing flow.
	if err := os.WriteFile(file, []byte("marketplace restored bytes"), 0700); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(control, "ownership.json"))
	if err != nil {
		t.Fatal(err)
	}
	var output, prompts bytes.Buffer
	if code := runSetupProducts([]string{"repair-cache", "--control-root", control, "--yes", "--bootstrap"}, strings.NewReader(""), &output, &prompts); code != 0 || output.String() != "ready\n" || prompts.Len() != 0 {
		t.Fatalf("safe existing relocation blocked: %d %q %q", code, output.String(), prompts.String())
	}
	after, err := os.ReadFile(filepath.Join(control, "ownership.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("deferral changed ownership")
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "marketplace restored bytes" {
		t.Fatal("deferral changed the retired cache")
	}
	output.Reset()
	if code := runSetupProducts([]string{"repair-cache", "--control-root", control, "--yes"}, strings.NewReader(""), &output, &prompts); code == 0 {
		t.Fatal("direct repair accepted changed owned bytes")
	}
	marker := filepath.Join(control, "transaction.json")
	if err := os.WriteFile(marker, []byte("TEST existing journal"), 0600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	prompts.Reset()
	if code := runSetupProducts([]string{"repair-cache", "--control-root", control, "--yes", "--bootstrap"}, strings.NewReader(""), &output, &prompts); code != 0 || output.String() != "ready\n" || prompts.Len() != 0 {
		t.Fatalf("bootstrap blocked existing journal handling: %d %q %q", code, output.String(), prompts.String())
	}
	journal, err := os.ReadFile(marker)
	if err != nil || string(journal) != "TEST existing journal" {
		t.Fatal("repair consumed existing journal")
	}
}

func TestSetupProductsRepairPreservesOrdinaryCodexReinstall(t *testing.T) {
	for _, damage := range []string{"missing", "foreign"} {
		t.Run(damage, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			control, runtime := filepath.Join(base, "control"), filepath.Join(base, "codex", "claude-notifications-go")
			file := filepath.Join(runtime, "bin", "TEST-runtime")
			ctx := setupCommandContext(t)
			r := installruntime.Request{ControlRoot: control, RuntimeRoot: runtime, Owner: "existing-installer", ConsumerID: "codex:TEST-hooks",
				Files: []installruntime.File{{Path: file, Data: []byte("verified runtime"), Mode: 0700}}}
			before, err := installruntime.Commit(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			if damage == "missing" {
				err = os.Remove(file)
			} else {
				err = os.WriteFile(file, []byte("user edit"), 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			var output, prompts bytes.Buffer
			if code := runSetupProducts([]string{"repair-cache", "--control-root", control, "--bootstrap", "--yes"}, strings.NewReader(""), &output, &prompts); code != 0 || output.String() != "ready\n" || prompts.Len() != 0 {
				t.Fatalf("optional precheck became installer prerequisite: %d %q %q", code, output.String(), prompts.String())
			}
			observed, _, err := installruntime.ReadOwnership(control)
			if err != nil || !reflect.DeepEqual(observed, before) {
				t.Fatal("precheck changed installation")
			}
			r.Files[0].Before, err = installruntime.Fingerprint(file)
			if err != nil {
				t.Fatal(err)
			}
			_, err = installruntime.Commit(ctx, r)
			if damage == "missing" {
				if err != nil {
					t.Fatalf("ordinary reinstall no longer restores missing owned file: %v", err)
				}
				if _, err := installruntime.ReadInstalledSnapshot(control); err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil {
					t.Fatal("ordinary reinstall overwrote user edit")
				}
				data, readErr := os.ReadFile(file)
				if readErr != nil || string(data) != "user edit" {
					t.Fatal("foreign bytes changed")
				}
			}
		})
	}
}

func TestSetupProductsRepairBootstrapCancellationIsFatal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output, prompts bytes.Buffer
	if code := runSetupProductsContext(ctx, []string{"repair-cache", "--control-root", filepath.Join(t.TempDir(), "absent"), "--bootstrap", "--yes"}, strings.NewReader(""), &output, &prompts); code == 0 || output.Len() != 0 {
		t.Fatalf("cancellation allowed bootstrap continuation: %d %q", code, output.String())
	}
}
