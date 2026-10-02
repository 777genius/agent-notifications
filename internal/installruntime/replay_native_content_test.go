package installruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Real durable journals must not authorize native bytes changed during downtime.
// The untouched-source repro enabled policy and removed the marker for both an
// in-place edit and a missing active directory (see task evidence).
func replayContentFixture(t *testing.T, native, predecessor bool) (context.Context, Request, Ledger) {
	t.Helper()
	skipUnsupportedNative(t)
	sandbox, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for key, dir := range map[string]string{"HOME": "home", "XDG_CONFIG_HOME": "config", "XDG_CACHE_HOME": "cache", "XDG_DATA_HOME": "data", "XDG_STATE_HOME": "state", "XDG_RUNTIME_DIR": "runtime", "TMPDIR": "tmp", "CLAUDE_CONFIG_DIR": "claude"} {
		path := filepath.Join(sandbox, dir)
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, path)
	}
	ctx, r := request(t)
	r.ConsumerID = "gemini-notifications"
	r.Consumer.Registration = filepath.Join(r.ControlRoot, "receipt.json")
	config := filepath.Join(r.RuntimeRoot, "config.json")
	r.ConfigPaths = []string{config}
	r.Files = []File{{Path: r.Consumer.Registration, Data: []byte("inert receipt"), Mode: 0600}, {Path: config, Data: []byte(`{"foreign":"keep"}`), Mode: 0600}}
	if native {
		r.Native, err = StageNative(ctx, r.ControlRoot, nativeFixture(t))
		if err != nil {
			t.Fatal(err)
		}
	}
	l, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	r.RuntimeRoot = l.Consumers[r.ConsumerID].RuntimeRoot
	r.Native, r.Files, r.ConfigPaths = nil, nil, nil
	if predecessor {
		source := nativeFixture(t)
		if err := os.WriteFile(filepath.Join(source, "Contents", "MacOS", "terminal-notifier-modern"), []byte("inert upgraded callback"), 0755); err != nil {
			t.Fatal(err)
		}
		r.Native, err = StageNative(ctx, r.ControlRoot, source)
		if err != nil {
			t.Fatal(err)
		}
		l, err = Commit(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		r.Native = nil
	}
	// Register OpenCode too, so each bounded channel control has real ownership.
	r.ConsumerID = "opencode-notifications"
	l, err = Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	r.RefreshOnly, r.PolicyOnly = true, true
	r.ExpectedGeneration = &l.Generation
	r.PolicyFields = map[string]json.RawMessage{"route": json.RawMessage(`{"geminiNotifications":{"desktop":true,"webhook":true},"openCodeNotifications":{"desktop":true,"webhook":true},"foreign":{"keep":true}}`)}
	l, err = Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	r.PolicyFields = nil
	return ctx, r, l
}

func replayContentJournal(t *testing.T, ctx context.Context, r Request) transaction {
	t.Helper()
	crash := errors.New("interrupted after durable transaction")
	r.Fault = func(phase string) error {
		if phase == "transaction" {
			return crash
		}
		return nil
	}
	if _, err := Commit(ctx, r); !errors.Is(err, crash) {
		t.Fatalf("did not reach durable transaction fault: %v", err)
	}
	tx, err := readTransactionFile(filepath.Join(r.ControlRoot, "transaction.json"))
	if err != nil {
		t.Fatal(err)
	}
	if tx.Native != nil {
		t.Fatal("fixture unexpectedly promotes native")
	}
	return tx
}

func replayContentPreimages(t *testing.T, root, runtimeRoot string) map[string][]byte {
	t.Helper()
	images := map[string][]byte{}
	for _, path := range []string{filepath.Join(root, "transaction.json"), filepath.Join(root, "ownership.json"), filepath.Join(root, "policy-generation.json"), filepath.Join(root, "agent-notifications.json"), filepath.Join(root, "receipt.json"), filepath.Join(runtimeRoot, "config.json")} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		images[path] = data
	}
	return images
}

func replayContentAssertRetained(t *testing.T, images map[string][]byte) {
	t.Helper()
	for path, before := range images {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Errorf("refusal changed or removed %s: %v", path, err)
		}
	}
}

func replayContentDamage(t *testing.T, record *NativeRecord, damage string) {
	t.Helper()
	switch damage {
	case "bytes":
		asset := filepath.Join(record.Path, "Contents", "MacOS", "terminal-notifier-modern")
		before, err := os.Stat(asset)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(asset, []byte("foreign same-inode native bytes"), before.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
		after, err := os.Stat(asset)
		if err != nil || !os.SameFile(before, after) {
			t.Fatal("fixture replaced the file inode", err)
		}
		id, err := nativeDirectoryID(record.Path)
		if err != nil || id != record.DirectoryID {
			t.Fatal("fixture replaced the native directory inode", err)
		}
		digest, err := treeFingerprint(record.Path)
		if err != nil || digest == record.SHA256 {
			t.Fatal("fixture did not change native bytes", err)
		}
	case "missing", "inode", "symlink":
		saved := record.Path + "-saved"
		if err := os.Rename(record.Path, saved); err != nil {
			t.Fatal(err)
		}
		if damage == "inode" {
			if err := os.Mkdir(record.Path, 0700); err != nil {
				t.Fatal(err)
			}
			if err := copyNativeTree(saved, record.Path); err != nil {
				t.Fatal(err)
			}
			digest, err := treeFingerprint(record.Path)
			if err != nil || digest != record.SHA256 {
				t.Fatal("replacement does not have identical bytes", err)
			}
			id, err := nativeDirectoryID(record.Path)
			if err != nil || id == record.DirectoryID {
				t.Fatal("replacement has original inode", err)
			}
		} else if damage == "symlink" {
			if err := os.Symlink(saved, record.Path); err != nil {
				t.Fatal(err)
			}
		} else if _, err := os.Lstat(record.Path); !os.IsNotExist(err) {
			t.Fatal("active path still exists", err)
		}
	default:
		t.Fatal("unknown fixture damage")
	}
}

func TestReplayNativeContentRefusesChangedActive(t *testing.T) {
	for _, mutation := range []string{"enable", "admission"} {
		for _, damage := range []string{"bytes", "missing", "inode", "symlink"} {
			t.Run(mutation+"/"+damage, func(t *testing.T) {
				ctx, r, before := replayContentFixture(t, true, false)
				r.ExpectedGeneration = &before.Generation
				if mutation == "enable" {
					on := true
					r.PolicyEnabled = &on
				} else {
					on := true
					r.PolicyEnabled = &on
					r.PolicyFields = map[string]json.RawMessage{"route": json.RawMessage(`{"geminiNotifications":{"desktop":false,"webhook":false}}`)}
					var err error
					before, err = Commit(ctx, r)
					if err != nil {
						t.Fatal(err)
					}
					r.ExpectedGeneration, r.PolicyEnabled = &before.Generation, nil
					r.PolicyFields = map[string]json.RawMessage{"route": json.RawMessage(`{"geminiNotifications":{"desktop":true,"webhook":false}}`)}
				}
				tx := replayContentJournal(t, ctx, r)
				if reflect.DeepEqual(tx.Before, tx.After) {
					t.Fatal("journal has no mutation")
				}
				preimages := replayContentPreimages(t, r.ControlRoot, r.RuntimeRoot)
				replayContentDamage(t, before.Native, damage)
				// Retry must continue to refuse, retaining the original decision and bytes.
				for attempt := 0; attempt < 2; attempt++ {
					_, err := Commit(ctx, Request{ControlRoot: r.ControlRoot, RecoverOnly: true})
					if err == nil {
						t.Errorf("RecoverOnly accepted %s active native during %s replay", damage, mutation)
					}
					replayContentAssertRetained(t, preimages)
				}
			})
		}
	}
}

func TestReplayNativeContentValidControls(t *testing.T) {
	for _, mode := range []string{"active", "no-native", "missing-retired"} {
		t.Run(mode, func(t *testing.T) {
			ctx, r, before := replayContentFixture(t, mode != "no-native", mode == "missing-retired")
			on := true
			r.PolicyEnabled, r.ExpectedGeneration = &on, &before.Generation
			tx := replayContentJournal(t, ctx, r)
			if mode == "missing-retired" {
				if before.Native.PreviousPath == "" {
					t.Fatal("fixture lacks predecessor")
				}
				if err := os.Rename(before.Native.PreviousPath, before.Native.PreviousPath+"-saved"); err != nil {
					t.Fatal(err)
				}
			}
			after, err := Commit(ctx, Request{ControlRoot: r.ControlRoot, RecoverOnly: true})
			if err != nil {
				t.Fatal("valid policy replay refused", err)
			}
			if !reflect.DeepEqual(after, tx.After) {
				t.Fatal("replay changed the journal's ledger intent")
			}
			policy, err := ReadUserPolicy(r.ControlRoot)
			if err != nil || !policy.Enabled {
				t.Fatal("enable not published", err)
			}
			if _, err := os.Lstat(filepath.Join(r.ControlRoot, "transaction.json")); !os.IsNotExist(err) {
				t.Fatal("completed marker retained", err)
			}
			snapshot, err := ReadInstalledSnapshot(r.ControlRoot)
			if err != nil || !snapshot.Enabled || snapshot.Recovery {
				t.Fatal("valid replay did not restore admission", err)
			}
		})
	}
}

func TestReplayNativeContentBoundedRevocation(t *testing.T) {
	for _, channel := range []string{"global", "gemini", "opencode"} {
		for _, damage := range []string{"bytes", "missing", "inode", "symlink"} {
			t.Run(channel+"/"+damage, func(t *testing.T) {
				ctx, r, before := replayContentFixture(t, true, false)
				on := true
				r.PolicyEnabled, r.ExpectedGeneration = &on, &before.Generation
				before, err := Commit(ctx, r)
				if err != nil {
					t.Fatal(err)
				}
				r.PolicyEnabled = nil
				s, err := ReadRevocationSnapshot(ctx, r.ControlRoot)
				if err != nil {
					t.Fatal(err)
				}
				r.ExpectedGeneration, r.ExpectedPolicy = &s.Generation, &s.Preimage
				switch channel {
				case "global":
					off := false
					r.PolicyEnabled = &off
				case "gemini":
					r.ConsumerID = "gemini-notifications"
					r.RevokeGemini = true
					r.PolicyFields = map[string]json.RawMessage{"route": json.RawMessage(`{"geminiNotifications":{"desktop":false,"webhook":false}}`)}
				case "opencode":
					r.RevokeOpenCode = true
					r.PolicyFields = map[string]json.RawMessage{"route": json.RawMessage(`{"openCodeNotifications":{"desktop":false,"webhook":false}}`)}
				}
				tx := replayContentJournal(t, ctx, r)
				preimages := replayContentPreimages(t, r.ControlRoot, r.RuntimeRoot)
				replayContentDamage(t, before.Native, damage)
				after, err := Commit(ctx, Request{ControlRoot: r.ControlRoot, RecoverOnly: true})
				if err != nil {
					t.Fatal("damaged native blocked bounded revocation", err)
				}
				if !reflect.DeepEqual(after, tx.After) || !reflect.DeepEqual(after.Native, before.Native) {
					t.Fatal("revocation changed unverified native ownership or ledger intent")
				}
				for _, file := range tx.Files {
					data, err := os.ReadFile(file.Path)
					if err != nil || !bytes.Equal(data, file.Data) {
						t.Fatal("bounded policy patch not published exactly", err)
					}
				}
				for _, path := range []string{filepath.Join(r.ControlRoot, "receipt.json"), filepath.Join(r.RuntimeRoot, "config.json")} {
					data, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(data, preimages[path]) {
						t.Fatal("revocation changed unrelated assets/config", err)
					}
				}
				if _, err := os.Lstat(filepath.Join(r.ControlRoot, "transaction.json")); !os.IsNotExist(err) {
					t.Fatal("completed revoke marker retained", err)
				}
			})
		}
	}
}
