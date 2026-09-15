package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func processStoreEnv(root string) EnvSnapshot {
	return EnvSnapshot{GOOS: runtime.GOOS, Vars: map[string]string{"HOME": root, "USERPROFILE": root, "APPDATA": filepath.Join(root, "neutral"), "XDG_CONFIG_HOME": filepath.Join(root, "neutral")}, Lstat: os.Lstat, ReadDir: os.ReadDir, Canonicalize: canonicalParent}
}
func awaitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("barrier timeout: %s", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func TestStoreProcessHelper(t *testing.T) {
	root := os.Getenv("STORE_TEST_ROOT")
	if root == "" {
		return
	}
	mode := os.Getenv("STORE_TEST_MODE")
	id := os.Getenv("STORE_TEST_ID")
	env := processStoreEnv(root)
	if mode == "init-alias" {
		env.Vars[OverrideEnv] = filepath.Join(root, "alias", "config.json")
		mode = "init"
	}
	if mode == "init-legacy" {
		env.Vars[OverrideEnv] = filepath.Join(root, ".claude", "claude-notifications-go", "config.json")
		mode = "init"
	}
	if err := os.WriteFile(filepath.Join(root, "ready-"+id), nil, 0600); err != nil {
		t.Fatal(err)
	}
	awaitFile(t, filepath.Join(root, "start"))
	var err error
	switch mode {
	case "init":
		_, err = EnsureInitialized(context.Background(), InitRequest{Env: env})
	case "edit", "retry":
		revision := os.Getenv("STORE_TEST_REVISION")
		for attempt := 0; attempt < 100; attempt++ {
			if mode == "retry" {
				d, _, e := ReadDocument(ReadRequest{Env: env, ReadSnapshot: ReadFileSnapshot})
				if e != nil {
					var ce *Error
					if errors.As(e, &ce) && (ce.Code == ConfigChanged || ce.Code == ConfigLockTimeout) {
						time.Sleep(10 * time.Millisecond)
						continue
					}
					t.Fatal(e)
				}
				revision = d.Revision()
			}
			_, err = ApplyEdits(context.Background(), EditRequest{Env: env, ExpectRevision: revision, Edits: Edits{Set: map[string]json.RawMessage{"/statuses/worker" + id + "/title": json.RawMessage(`"saved"`)}}})
			var ce *Error
			if errors.As(err, &ce) && (ce.Code == ConfigConflict || (mode == "retry" && ce.Code == ConfigLockTimeout)) {
				if mode == "edit" {
					os.Exit(23)
				}
				time.Sleep(10 * time.Millisecond)
				continue
			}
			break
		}
	case "hold-lock", "crash-before-lock", "crash-lock", "crash-temp", "crash-temp-write", "crash-temp-sync", "crash-backup", "crash-before-replace", "crash-after-replace", "crash-before-dir-sync":
		if mode == "crash-before-lock" {
			os.Exit(24)
		}
		ctx, cancel := context.WithTimeout(context.Background(), MutationTimeout)
		defer cancel()
		m, e := beginMutation(ctx, env)
		if e != nil {
			t.Fatal(e)
		}
		if mode == "hold-lock" {
			if e = os.WriteFile(filepath.Join(root, "held"), nil, 0600); e != nil {
				t.Fatal(e)
			}
			for {
				time.Sleep(time.Hour)
			}
		}
		var old, next Document
		if m.selection.Exists {
			old, e = currentDocument(m, AssetContext{})
			if e != nil {
				t.Fatal(e)
			}
			next, e = ApplyRawEdits(old, Edits{Set: map[string]json.RawMessage{"/debug/benchmark": json.RawMessage(`true`)}}, AssetContext{})
		} else {
			next, e = SeedDocument(m.selection.Path)
		}
		if e != nil {
			t.Fatal(e)
		}
		if mode == "crash-temp-write" || mode == "crash-temp-sync" {
			name, e := uniqueStoreName(filepath.Base(m.selection.Path), "tmp")
			if e != nil {
				t.Fatal(e)
			}
			f, e := os.OpenFile(filepath.Join(filepath.Dir(m.selection.Path), name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.Write(next.Bytes()); e != nil {
				t.Fatal(e)
			}
			if mode == "crash-temp-sync" {
				if e = f.Sync(); e != nil {
					t.Fatal(e)
				}
			}
			os.Exit(24)
		}
		if mode == "crash-before-replace" || mode == "crash-after-replace" || mode == "crash-before-dir-sync" {
			m.parent = &crashMutationFiles{mutationFiles: m.parent, phase: mode}
			if _, e = commitDocument(ctx, env, m, old, next, Result{Selection: m.selection, Revision: old.Revision()}); e != nil {
				t.Fatal(e)
			}
			t.Fatal("crash boundary was not reached")
		}
		if mode == "crash-temp" {
			if _, e = m.parent.writeArtifact(filepath.Base(m.selection.Path), "tmp", next.Bytes()); e != nil {
				t.Fatal(e)
			}
		}
		if mode == "crash-backup" {
			if _, e = m.parent.writeArtifact(filepath.Base(m.selection.Path), "backup", old.Bytes()); e != nil {
				t.Fatal(e)
			}
		}
		os.Exit(24) // Deliberately skips unlock and deferred cleanup.
	default:
		t.Fatal("unknown helper mode")
	}
	if err != nil {
		t.Fatal(err)
	}
}
func spawnStoreChild(t *testing.T, root, mode, id, revision string) (*exec.Cmd, *strings.Builder) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestStoreProcessHelper$")
	// An allowlist: no host agent, profile, notification, or proxy environment.
	cmd.Env = []string{"HOME=" + root, "USERPROFILE=" + root, "TMPDIR=" + root, "TMP=" + root, "TEMP=" + root, "GOMAXPROCS=2", "STORE_TEST_ROOT=" + root, "STORE_TEST_MODE=" + mode, "STORE_TEST_ID=" + id, "STORE_TEST_REVISION=" + revision}
	output := new(strings.Builder)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return cmd, output
}
func TestStoreTwentyProcesses(t *testing.T) {
	for _, mode := range []string{"init", "aliases", "edit", "retry"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			env := processStoreEnv(root)
			revision := ""
			if mode == "aliases" {
				neutral, _, e := neutralPath(env, root)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.MkdirAll(filepath.Dir(neutral), 0700); e != nil {
					t.Fatal(e)
				}
				if e = os.Symlink(filepath.Dir(neutral), filepath.Join(root, "alias")); e != nil {
					if runtime.GOOS == "windows" {
						t.Skip("native symlink privilege unavailable")
					}
					t.Fatal(e)
				}
			}
			if mode != "init" && mode != "aliases" {
				r, e := EnsureInitialized(context.Background(), InitRequest{Env: env})
				if e != nil {
					t.Fatal(e)
				}
				revision = r.Revision
			}
			cmds := make([]*exec.Cmd, 20)
			outputs := make([]*strings.Builder, 20)
			for i := range cmds {
				childMode := mode
				if mode == "aliases" {
					childMode = "init"
					if i%2 == 0 {
						childMode = "init-alias"
					}
				}
				cmds[i], outputs[i] = spawnStoreChild(t, root, childMode, strconv.Itoa(i), revision)
			}
			for i := range cmds {
				awaitFile(t, filepath.Join(root, fmt.Sprintf("ready-%d", i)))
			}
			if e := os.WriteFile(filepath.Join(root, "start"), nil, 0600); e != nil {
				t.Fatal(e)
			}
			successes := 0
			conflicts := 0
			for i, cmd := range cmds {
				e := cmd.Wait()
				if e == nil {
					successes++
					continue
				}
				var exit *exec.ExitError
				if errors.As(e, &exit) && exit.ExitCode() == 23 {
					conflicts++
					continue
				}
				t.Fatalf("child %d: %v %s", i, e, outputs[i])
			}
			if mode == "edit" {
				if successes != 1 || conflicts != 19 {
					t.Fatalf("success=%d conflict=%d", successes, conflicts)
				}
			} else if successes != 20 {
				t.Fatalf("success=%d", successes)
			}
			d, _, e := ReadDocument(ReadRequest{Env: env, ReadSnapshot: ReadFileSnapshot})
			if e != nil {
				t.Fatal(e)
			}
			if mode == "retry" {
				var raw map[string]json.RawMessage
				_ = json.Unmarshal(d.Raw()["statuses"], &raw)
				for i := 0; i < 20; i++ {
					if _, ok := raw[fmt.Sprintf("worker%d", i)]; !ok {
						t.Fatalf("lost worker %d", i)
					}
				}
			}
		})
	}
}
func TestStoreProcessCrashReleasesLock(t *testing.T) {
	oldBytes := []byte("{\n  \"notifications\": {\"desktop\": {\"enabled\": false, \"sound\": false, \"volume\": 0.37}},\n  \"future\": {\"integer\": 9007199254740993}\n}\n")
	for _, existing := range []bool{false, true} {
		for _, mode := range []string{"crash-before-lock", "crash-lock", "crash-temp-write", "crash-temp-sync", "crash-temp", "crash-backup", "crash-before-replace", "crash-after-replace", "crash-before-dir-sync"} {
			if !existing && mode == "crash-backup" {
				continue
			}
			name := "fresh/" + mode
			if existing {
				name = "existing/" + mode
			}
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				env := processStoreEnv(root)
				selection, e := Resolve(env)
				if e != nil {
					t.Fatal(e)
				}
				var nextBytes []byte
				if existing {
					if e = os.MkdirAll(filepath.Dir(selection.Path), 0700); e != nil {
						t.Fatal(e)
					}
					if e = os.WriteFile(selection.Path, oldBytes, 0644); e != nil {
						t.Fatal(e)
					}
					if runtime.GOOS != "windows" {
						// os.WriteFile honors the process umask for a new fixture.
						if e = os.Chmod(selection.Path, 0644); e != nil {
							t.Fatal(e)
						}
					}
					old, e := ParseDocument(oldBytes, selection.Path, true)
					if e != nil {
						t.Fatal(e)
					}
					next, e := ApplyRawEdits(old, Edits{Set: map[string]json.RawMessage{"/debug/benchmark": json.RawMessage(`true`)}}, AssetContext{})
					if e != nil {
						t.Fatal(e)
					}
					nextBytes = next.Bytes()
				} else {
					seed, e := SeedDocument(selection.Path)
					if e != nil {
						t.Fatal(e)
					}
					nextBytes = seed.Bytes()
				}
				permissions := captureCrashPermissions(t, selection.Path, existing)
				cmd, output := spawnStoreChild(t, root, mode, "0", "")
				awaitFile(t, filepath.Join(root, "ready-0"))
				if e := os.WriteFile(filepath.Join(root, "start"), nil, 0600); e != nil {
					t.Fatal(e)
				}
				e = cmd.Wait()
				var exit *exec.ExitError
				if !errors.As(e, &exit) || exit.ExitCode() != 24 {
					t.Fatalf("%v %s", e, output)
				}
				// Assert the physical crash point before invoking any recovery path.
				got, readErr := os.ReadFile(selection.Path)
				published := mode == "crash-after-replace" || mode == "crash-before-dir-sync"
				switch {
				case published:
					if readErr != nil || !bytes.Equal(got, nextBytes) {
						t.Fatalf("published bytes: err=%v got=%q want=%q", readErr, got, nextBytes)
					}
					assertCrashPermissions(t, selection.Path, permissions, existing, published)
				case existing:
					if readErr != nil || !bytes.Equal(got, oldBytes) {
						t.Fatalf("original bytes: err=%v got=%q want=%q", readErr, got, oldBytes)
					}
					assertCrashPermissions(t, selection.Path, permissions, true, published)
				case readErr == nil || !os.IsNotExist(readErr):
					t.Fatalf("fresh target unexpectedly exists: err=%v bytes=%q", readErr, got)
				}

				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_, e = EnsureInitialized(ctx, InitRequest{Env: env})
				wantRecovery := !existing && (mode == "crash-temp" || mode == "crash-temp-write" || mode == "crash-temp-sync" || mode == "crash-before-replace")
				if wantRecovery {
					var ce *Error
					if !errors.As(e, &ce) || ce.Code != ConfigRecoveryRequired {
						t.Fatalf("%v", e)
					}
				} else if e != nil {
					t.Fatal(e)
				}
				if !wantRecovery {
					want := nextBytes
					if existing && !published {
						want = oldBytes
					}
					if got, e = os.ReadFile(selection.Path); e != nil || !bytes.Equal(got, want) {
						t.Fatalf("recovery bytes: err=%v got=%q want=%q", e, got, want)
					}
				}
			})
		}
	}
}

func TestStoreProcessSelectionGuardOrders(t *testing.T) {
	for _, first := range []string{"legacy", "neutral"} {
		t.Run(first, func(t *testing.T) {
			root := t.TempDir()
			env := processStoreEnv(root)
			if first == "legacy" {
				env.Vars[OverrideEnv] = filepath.Join(root, ".claude", "claude-notifications-go", "config.json")
			}
			m, e := beginMutation(context.Background(), env)
			if e != nil {
				t.Fatal(e)
			}
			childMode := "init"
			if first == "neutral" {
				childMode = "init-legacy"
			}
			cmd, output := spawnStoreChild(t, root, childMode, "0", "")
			awaitFile(t, filepath.Join(root, "ready-0"))
			if e = os.WriteFile(filepath.Join(root, "start"), nil, 0600); e != nil {
				m.close()
				t.Fatal(e)
			}
			seed, e := SeedDocument(m.selection.Path)
			if e != nil {
				m.close()
				t.Fatal(e)
			}
			_, e = commitDocument(context.Background(), env, m, Document{}, seed, Result{Selection: m.selection})
			m.close()
			if e != nil {
				t.Fatal(e)
			}
			if e = cmd.Wait(); e != nil {
				t.Fatalf("%v %s", e, output)
			}
			automatic := processStoreEnv(root)
			selected, e := Resolve(automatic)
			if e != nil || selected.Source != "legacy" || !selected.Exists {
				t.Fatalf("%+v %v", selected, e)
			}
			neutral, _, e := neutralPath(automatic, root)
			if e != nil {
				t.Fatal(e)
			}
			_, e = os.Stat(neutral)
			if first == "legacy" && !os.IsNotExist(e) {
				t.Fatal("auto created N despite L winner")
			}
			if first == "neutral" && e != nil {
				t.Fatal("N winner lost")
			}
		})
	}
}

// Crash barriers are test-only adapters around the actual transaction methods.
type crashMutationFiles struct {
	mutationFiles
	phase     string
	published bool
}

func (f *crashMutationFiles) publish(ctx context.Context, temp, name string, exists bool, old, next []byte) error {
	if f.phase == "crash-before-replace" {
		os.Exit(24)
	}
	err := f.mutationFiles.publish(ctx, temp, name, exists, old, next)
	if err == nil {
		f.published = true
	}
	if err == nil && f.phase == "crash-after-replace" {
		os.Exit(24)
	}
	return err
}
func (f *crashMutationFiles) sync() error {
	if f.phase == "crash-before-dir-sync" && f.published {
		os.Exit(24)
	}
	return f.mutationFiles.sync()
}

func TestStoreKilledLockOwner(t *testing.T) {
	root := t.TempDir()
	env := processStoreEnv(root)
	cmd, output := spawnStoreChild(t, root, "hold-lock", "0", "")
	awaitFile(t, filepath.Join(root, "ready-0"))
	if e := os.WriteFile(filepath.Join(root, "start"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	awaitFile(t, filepath.Join(root, "held"))
	guard := filepath.Join(root, ".agent-notifications-config.lock")
	before, e := os.Stat(guard)
	if e != nil {
		t.Fatal(e)
	}
	if e = cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	if e = cmd.Wait(); e == nil {
		t.Fatalf("killed child succeeded: %s", output)
	}
	// Exercise crash recovery with the same budget as a real mutation. This
	// includes path validation, ACL checks and initialization, not just locking.
	ctx, cancel := context.WithTimeout(context.Background(), MutationTimeout)
	defer cancel()
	if _, e = EnsureInitialized(ctx, InitRequest{Env: env}); e != nil {
		t.Fatal(e)
	}
	after, e := os.Stat(guard)
	if e != nil || !os.SameFile(before, after) {
		t.Fatal("persistent lock was replaced")
	}
}
