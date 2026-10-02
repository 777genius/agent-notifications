package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Removing the flag, printing the default status, or returning an alias/stage
// instead of the committed Native.Path makes this contract test fail.
func TestInstallRuntimePrintNativePath(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-native", true: "durable-native"}[native], func(t *testing.T) {
			if native && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
				t.Skip("native staging is Unix-only")
			}
			root := isolatedRuntimePathRoot(t)
			stage := filepath.Join(root, "stage")
			target := filepath.Join(root, "runtime", "bin")
			control := filepath.Join(root, "control")
			if err := os.MkdirAll(stage, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stage, "claude-notifications-linux-amd64"), []byte("inert "+installruntime.WriterProtocolMarker), 0700); err != nil {
				t.Fatal(err)
			}
			if native {
				app := filepath.Join(stage, "ClaudeNotifier.app", "Contents", "MacOS")
				if err := os.MkdirAll(app, 0700); err != nil {
					t.Fatal(err)
				}
				// Unknown native packages are copied and hashed, never executed.
				if err := os.WriteFile(filepath.Join(app, "terminal-notifier-modern"), []byte("inert native"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			args := []string{"--stage", stage, "--target", target, "--control-root", control, "--print-native-path"}
			if err := installRuntime(args, &output); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(control, "ownership.json"))
			if err != nil {
				t.Fatal(err)
			}
			var ledger installruntime.Ledger
			if err = json.Unmarshal(raw, &ledger); err != nil {
				t.Fatal(err)
			}
			want := ""
			if ledger.Native != nil {
				want = ledger.Native.Path
			}
			if output.String() != want+"\n" {
				t.Fatalf("stdout=%q want %q", output.String(), want+"\n")
			}
			if native {
				if want == "" || strings.Contains(want, ".candidate-") || strings.HasPrefix(want, stage) {
					t.Fatalf("not published: %s", want)
				}
				if _, err := os.Stat(want); err != nil {
					t.Fatal(err)
				}
			}
			output.Reset()
			if err := installRuntime(args[:len(args)-1], &output); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(output.String(), "managed-runtime committed generation=") {
				t.Fatalf("default output changed: %q", output.String())
			}
			output.Reset()
			if err := installRuntime([]string{"--remove", "--purge-native", "--target", target, "--control-root", control, "--print-native-path"}, &output); err != nil {
				t.Fatal(err)
			}
			if output.String() != "\n" {
				t.Fatalf("purge stdout=%q", output.String())
			}

		})
	}
}

// Keep every environment-derived path physical and private, including the
// default control root: it is shared state, independent of --target.
func isolatedRuntimePathRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "TMPDIR", "TMP", "TEMP", "CLAUDE_CONFIG_DIR"} {
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(name, path)
	}
	return root
}

type runtimePathFixture struct {
	root, stage, target, control string
	ledger                       installruntime.Ledger
	args                         []string
}

func newRuntimePathFixture(t *testing.T, state string, explicitControl bool) runtimePathFixture {
	t.Helper()
	if state == "native" && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("native staging is Unix-only")
	}
	f := runtimePathFixture{root: isolatedRuntimePathRoot(t)}
	f.stage = filepath.Join(f.root, "stage")
	f.target = filepath.Join(f.root, "runtime", "bin")
	var err error
	f.control, err = installruntime.ControlRoot()
	if err != nil {
		t.Fatal(err)
	}
	f.control, err = installruntime.CanonicalPath(f.control)
	if err != nil {
		t.Fatal(err)
	}
	if explicitControl {
		f.control = filepath.Join(f.root, "separate-control")
	}
	for _, path := range []string{f.stage, f.target} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeRuntimePathFile(t, filepath.Join(f.stage, "sound-preview"), []byte("inert utility"), 0700)
	f.args = []string{"--stage", f.stage, "--target", f.target}
	if explicitControl {
		f.args = append(f.args, "--control-root", f.control)
	}
	if state == "native" {
		writeRuntimePathFile(t, filepath.Join(f.stage, "claude-notifications-linux-amd64"), []byte("inert "+installruntime.WriterProtocolMarker), 0700)
		// Unknown retained helpers remain compatible; nothing executes this fixture.
		writeRuntimePathFile(t, filepath.Join(f.stage, "ClaudeNotifier.app", "Contents", "MacOS", "terminal-notifier-modern"), []byte("inert native"), 0700)
	}
	if state == "absent" {
		writeRuntimePathFile(t, filepath.Join(f.target, "sound-preview"), []byte("inert utility"), 0700)
	} else {
		if err := installRuntime(f.args, io.Discard); err != nil {
			t.Fatal(err)
		}
		// Preserve explicit user policy and foreign fields byte for byte.
		writeRuntimePathFile(t, filepath.Join(f.control, "agent-notifications.json"), []byte(`{"schemaVersion":1,"enabled":false,"userChoice":"keep"}`+"\n"), 0600)
		snapshot, err := installruntime.ReadInstalledSnapshot(f.control)
		if err != nil || snapshot.Recovery || snapshot.Ledger.ID == "" {
			t.Fatalf("invalid fixture snapshot: %+v, %v", snapshot, err)
		}
		f.ledger = snapshot.Ledger
		if state == "native" && (f.ledger.Native == nil || f.ledger.Native.Path == "") {
			t.Fatal("fixture has no durable native generation")
		}
	}
	if state == "native" {
		if err := os.Remove(filepath.Join(f.stage, "claude-notifications-linux-amd64")); err != nil {
			t.Fatal(err)
		}
	}
	// An unchanged utility absent from ownership must not be adopted by refresh.
	for _, path := range []string{f.stage, f.target} {
		writeRuntimePathFile(t, filepath.Join(path, "list-sounds"), []byte("unmanaged utility"), 0700)
	}
	f.args = append(f.args, "--refresh") // intentionally no --entry
	return f
}

func writeRuntimePathFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

type runtimePathState struct {
	info     os.FileInfo
	identity installruntime.Identity
}

func runtimePathTree(t *testing.T, root string) map[string]runtimePathState {
	t.Helper()
	state := map[string]runtimePathState{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		var id installruntime.Identity
		if !entry.IsDir() {
			id, err = installruntime.Fingerprint(path)
			if err != nil {
				return err
			}
		}
		state[path] = runtimePathState{info, id}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return state
}

func assertRuntimePathTree(t *testing.T, root string, before map[string]runtimePathState) {
	t.Helper()
	after := runtimePathTree(t, root)
	if len(before) != len(after) {
		t.Errorf("refresh changed tree entries: before=%d after=%d", len(before), len(after))
	}
	for path, want := range before {
		got, ok := after[path]
		if !ok || !os.SameFile(want.info, got.info) || want.info.Mode() != got.info.Mode() ||
			!want.info.ModTime().Equal(got.info.ModTime()) || want.identity != got.identity {
			t.Errorf("refresh mutated or replaced %s", path)
		}
	}
}

func TestInstallRuntimeRefreshPrintNativePath(t *testing.T) {
	for _, state := range []string{"native", "no-native", "absent"} {
		for _, explicit := range []bool{false, true} {
			name := state + map[bool]string{false: "/default-control", true: "/explicit-control"}[explicit]
			t.Run(name, func(t *testing.T) {
				f := newRuntimePathFixture(t, state, explicit)
				before := runtimePathTree(t, f.root)
				var output bytes.Buffer
				if err := installRuntime(f.args, &output); err != nil {
					t.Fatal(err)
				}
				if output.Len() != 0 {
					t.Errorf("default no-op refresh stdout=%q", output.String())
				}
				assertRuntimePathTree(t, f.root, before)
				if err := installRuntime(append(f.args, "--print-native-path"), &output); err != nil {
					t.Fatal(err)
				}
				want := "\n"
				if f.ledger.Native != nil {
					want = f.ledger.Native.Path + "\n"
				}
				if output.String() != want {
					t.Errorf("no-op refresh stdout=%q want %q", output.String(), want)
				}
				assertRuntimePathTree(t, f.root, before)
			})
		}
	}
}

func TestInstallRuntimeRefreshPrintNativePathRefusesInvalidState(t *testing.T) {
	for _, damage := range []string{"malformed-ledger", "ownership-read-error", "foreign-owner", "foreign-native-root", "pending-transaction", "policy-generation", "native-content", "native-inode", "native-missing", "native-symlink", "native-binding"} {
		t.Run(damage, func(t *testing.T) {
			f := newRuntimePathFixture(t, "native", true)
			ownership := filepath.Join(f.control, "ownership.json")
			writeLedger := func() {
				data, err := json.Marshal(f.ledger)
				if err != nil {
					t.Fatal(err)
				}
				writeRuntimePathFile(t, ownership, data, 0600)
			}
			switch damage {
			case "malformed-ledger":
				writeRuntimePathFile(t, ownership, []byte("{"), 0600)
			case "ownership-read-error":
				if err := os.Remove(ownership); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(ownership, 0700); err != nil {
					t.Fatal(err)
				}
			case "foreign-owner":
				f.ledger.Owner = "foreign-installer"
				writeLedger()
			case "foreign-native-root":
				// A structurally valid snapshot from another control root is not
				// this component's durable generation, even when hashes match.
				other := newRuntimePathFixture(t, "native", true)
				f.ledger.Native = other.ledger.Native
				writeLedger()
			case "pending-transaction":
				writeRuntimePathFile(t, filepath.Join(f.control, "transaction.json"), []byte("pending, do not recover"), 0600)
			case "policy-generation":
				f.ledger.PolicyGeneration++
				writeLedger()
			case "native-content":
				writeRuntimePathFile(t, filepath.Join(f.ledger.Native.Path, "Contents", "MacOS", "terminal-notifier-modern"), []byte("foreign native"), 0700)
			case "native-inode", "native-missing", "native-symlink":
				old := f.ledger.Native.Path + ".retained"
				if err := os.Rename(f.ledger.Native.Path, old); err != nil {
					t.Fatal(err)
				}
				if damage == "native-inode" {
					writeRuntimePathFile(t, filepath.Join(f.ledger.Native.Path, "Contents", "MacOS", "terminal-notifier-modern"), []byte("inert native"), 0700)
				} else if damage == "native-symlink" {
					if err := os.Symlink(old, f.ledger.Native.Path); err != nil {
						t.Fatal(err)
					}
				}
			case "native-binding":
				f.ledger.Native.InstalledTreeSHA256 = "invalid"
				writeLedger()
			}
			before := runtimePathTree(t, f.root)
			var output bytes.Buffer
			// Default no-op refresh stays silent and does not inspect/adopt state.
			if err := installRuntime(f.args, &output); err != nil || output.Len() != 0 {
				t.Fatalf("default no-op refresh changed: %q, %v", output.String(), err)
			}
			if err := installRuntime(append(f.args, "--print-native-path"), &output); err == nil {
				t.Error("invalid installed state reported success")
			}
			if output.Len() != 0 {
				t.Errorf("invalid native path printed: %q", output.String())
			}
			assertRuntimePathTree(t, f.root, before)
		})
	}
}

type runtimePathErrorWriter struct{ err error }

func (w runtimePathErrorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestInstallRuntimeRefreshPrintNativePathOutputError(t *testing.T) {
	f := newRuntimePathFixture(t, "no-native", true)
	before := runtimePathTree(t, f.root)
	want := errors.New("output failed")
	if err := installRuntime(append(f.args, "--print-native-path"), runtimePathErrorWriter{want}); !errors.Is(err, want) {
		t.Errorf("output error=%v want %v", err, want)
	}
	assertRuntimePathTree(t, f.root, before)
}
