//go:build linux || darwin || windows

package installruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func configOnlyPolicyFixture(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "control")
	if err := ensureDir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := safePublish(File{Path: filepath.Join(root, "config.json"), Data: []byte(`{"notifications":{"desktop":{"enabled":true}},"TEST":"keep"}`), Mode: 0600}, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "canary"), []byte("TEST outside control, preserve bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	return base, root
}

func policyControlTree(t *testing.T, base string) map[string]string {
	t.Helper()
	entries := map[string]string{}
	err := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		entries[path] = info.Mode().String()
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			entries[path] += " " + target
		} else if !entry.IsDir() {
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			entries[path] += fmt.Sprintf(" %x", body)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestPolicySnapshotConfigOnlyRootIsReadOnlyFresh(t *testing.T) {
	for _, withLock := range []bool{false, true} {
		t.Run(fmt.Sprint(withLock), func(t *testing.T) {
			base, root := configOnlyPolicyFixture(t)
			if withLock {
				if err := os.WriteFile(filepath.Join(root, "config.json.lock"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := policyControlTree(t, base)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			snapshot, err := ReadPolicySnapshot(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Policy.SchemaVersion != 1 || snapshot.Policy.Enabled || snapshot.Preimage.Exists || snapshot.Installation.Enabled || snapshot.Installation.Recovery || snapshot.Installation.Ledger.ID != "" || snapshot.Installation.Ledger.Generation != 0 || len(snapshot.Fields) != 0 {
				t.Fatalf("config-only root gained managed authority: %+v", snapshot)
			}
			if after := policyControlTree(t, base); !reflect.DeepEqual(before, after) {
				t.Fatalf("snapshot mutated config-only root: before=%v after=%v", before, after)
			}
		})
	}
}

func TestPolicySnapshotConfigOnlyRootRefusesUnknownState(t *testing.T) {
	for _, kind := range []string{"owned-missing-component-lock", "corrupt-ledger", "missing-policy-lock", "foreign-policy", "unknown-canary", "config-symlink", "config-hardlink", "root-symlink", "parent-symlink"} {
		t.Run(kind, func(t *testing.T) {
			base, root := configOnlyPolicyFixture(t)
			write := func(name, body string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "owned-missing-component-lock":
				raw, err := json.Marshal(Ledger{Schema: ledgerSchemaV1, ID: "TEST-owned", Owner: "existing-installer", Generation: 1, Consumers: map[string]Consumer{}, Files: map[string]Identity{}})
				if err != nil {
					t.Fatal(err)
				}
				write("ownership.json", string(raw))
				if ledger, _, err := ReadOwnership(root); err != nil || ledger.ID != "TEST-owned" {
					t.Fatalf("owned fixture is invalid: %+v %v", ledger, err)
				}
			case "corrupt-ledger":
				write("ownership.json", `{broken`)
			case "missing-policy-lock":
				write(".component-install.lock", "")
			case "foreign-policy":
				write("agent-notifications.json", `{"schemaVersion":1,"enabled":true,"TEST":"foreign"}`)
			case "unknown-canary":
				write("TEST-foreign-canary", "preserve unknown control data")
			case "config-symlink":
				if err := os.Remove(filepath.Join(root, "config.json")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(base, "canary"), filepath.Join(root, "config.json")); err != nil {
					t.Skip(err)
				}
			case "config-hardlink":
				if err := os.Remove(filepath.Join(root, "config.json")); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(filepath.Join(base, "canary"), filepath.Join(root, "config.json")); err != nil {
					t.Skip(err)
				}
			case "root-symlink":
				physical := root + "-physical"
				if err := os.Rename(root, physical); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(physical, root); err != nil {
					t.Skip(err)
				}
			case "parent-symlink":
				physical := filepath.Join(base, "physical")
				if err := os.Mkdir(physical, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(root, filepath.Join(physical, "control")); err != nil {
					t.Fatal(err)
				}
				alias := filepath.Join(base, "alias")
				if err := os.Symlink(physical, alias); err != nil {
					t.Skip(err)
				}
				root = filepath.Join(alias, "control")
			}
			before := policyControlTree(t, base)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if snapshot, err := ReadPolicySnapshot(ctx, root); err == nil {
				t.Fatalf("%s became fresh managed absence: %+v", kind, snapshot)
			}
			if after := policyControlTree(t, base); !reflect.DeepEqual(before, after) {
				t.Fatalf("refusal changed %s: before=%v after=%v", kind, before, after)
			}
		})
	}
}

func TestPolicySnapshotConfigOnlyRootHonorsCancellation(t *testing.T) {
	base, root := configOnlyPolicyFixture(t)
	before := policyControlTree(t, base)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	cancel()
	if _, err := ReadPolicySnapshot(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled config-only observation: %v", err)
	}
	if !reflect.DeepEqual(before, policyControlTree(t, base)) {
		t.Fatal("canceled snapshot changed config-only root")
	}
}
