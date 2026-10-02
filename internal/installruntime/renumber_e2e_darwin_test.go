//go:build darwin

package installruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func renumberAPFS(t *testing.T, e *renumberEnv) {
	t.Helper()
	var st unix.Statfs_t
	if err := unix.Statfs(e.root, &st); err != nil {
		t.Fatal(err)
	}
	var name []byte
	for _, c := range st.Fstypename {
		if c == 0 {
			break
		}
		name = append(name, byte(c))
	}
	if string(name) != "apfs" {
		t.Fatalf("core renumber E2E requires actual sandbox APFS, got %q", name)
	}
}
func TestRenumberE2EDarwinFlows(t *testing.T) {
	e := renumberSandbox(t)
	renumberAPFS(t, e)
	stageA := e.signedStage(t, "A")
	a := e.install(t, stageA)
	pathA := a.Native.Path
	idA, _ := nativeDirectoryID(pathA)
	hashA, _ := treeFingerprint(pathA)
	alias := filepath.Join(e.target, "ClaudeNotifier.app")
	linkA, err := os.Readlink(alias)
	if err != nil {
		t.Fatal(err)
	}
	renumberLedger(t, e.control)
	if _, err := ReadInstalledSnapshot(e.control); err != nil {
		t.Fatal("snapshot after device renumber:", err)
	}
	refreshed := e.install(t, stageA)
	renumberFresh(t, refreshed)
	linkRefreshed, err := os.Readlink(alias)
	if err != nil || linkRefreshed != linkA || refreshed.Native.Path != pathA {
		t.Fatal("identity refresh changed generation path or alias bytes", err)
	}
	renumberLedger(t, e.control)
	b := e.install(t, e.signedStage(t, "B"))
	renumberFresh(t, b)
	pathB := b.Native.Path
	if pathB == pathA || b.Native.PreviousPath != pathA {
		t.Fatal("upgrade did not retain predecessor")
	}
	renumberLedger(t, e.control)
	again := e.install(t, stageA)
	renumberFresh(t, again)
	if again.Native.Path != pathA || len(again.Native.Published) != 2 {
		t.Fatal("reselection changed generation paths or inventory")
	}
	linkAgain, err := os.Readlink(alias)
	if err != nil || linkAgain != linkA {
		t.Fatal("reselection changed original alias bytes", err)
	}
	idAfter, _ := nativeDirectoryID(pathA)
	hashAfter, _ := treeFingerprint(pathA)
	if idA != idAfter || hashA != hashAfter {
		t.Fatal("update/reselect modified published generation A")
	}
	for _, path := range []string{pathA, pathB} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("retained generation missing", err)
		}
	}
	renumberLedger(t, e.control)
	e.run(t, "internal-install-runtime", "--remove", "--purge-native", "--target", e.target, "--control-root", e.control)
	for _, path := range []string{pathA, pathB} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("purge retained %s: %v", path, err)
		}
	}
	snap, err := ReadInstalledSnapshot(e.control)
	if err != nil || snap.Ledger.Native != nil {
		t.Fatal("purge snapshot", err)
	}
}

var renumberCrash = errors.New("intentional E2E crash")

func renumberFault(boundary string) func(string) error {
	return func(phase string) error {
		if phase == boundary || boundary == "purge-entry" && strings.HasPrefix(phase, "purge-entry:") {
			return renumberCrash
		}
		return nil
	}
}
func renumberPending(t *testing.T, e *renumberEnv, boundary string, purge bool) (transaction, []string) {
	t.Helper()
	a := e.install(t, e.signedStage(t, "pending-A"))
	paths := []string{a.Native.Path}
	r := Request{ControlRoot: e.control, RuntimeRoot: filepath.Dir(e.target), Owner: "existing-installer", ConsumerID: "claude-hooks", Fault: renumberFault(boundary)}
	if purge {
		r.RemoveConsumer = true
		r.PurgeNative = true
	} else {
		stage := e.signedStage(t, "pending-B")
		change, err := StageNative(e.ctx, e.control, filepath.Join(stage, "ClaudeNotifier.app"))
		if err != nil {
			t.Fatal(err)
		}
		r.Native = change
		paths = append(paths, change.After.Path)
		file := filepath.Join(e.target, "recovery-payload")
		r.Files = []File{{Path: file, Data: []byte("new bytes"), Mode: 0600}}
	}
	if _, err := Commit(e.ctx, r); !errors.Is(err, renumberCrash) {
		t.Fatalf("requested durable boundary %s not reached: %v", boundary, err)
	}
	return renumberJournal(t, e.control), paths
}
func TestRenumberE2EDarwinRecovery(t *testing.T) {
	for _, boundary := range []string{"transaction", "native", "ledger"} {
		for _, rollback := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/rollback=%t", boundary, rollback), func(t *testing.T) {
				e := renumberSandbox(t)
				renumberAPFS(t, e)
				tx, paths := renumberPending(t, e, boundary, false)
				marker := filepath.Join(e.control, "transaction.json")
				journal := renumberRead(t, marker)
				current, err := readLedger(e.control)
				if err != nil {
					t.Fatal(err)
				}
				if rollback {
					_, err = Commit(e.ctx, Request{ControlRoot: e.control, RollbackPending: true, Fault: renumberFault("ledger")})
					if !errors.Is(err, renumberCrash) {
						t.Fatal("rollback re-crash was not reached", err)
					}
					reverse, err := readTransactionFile(marker)
					if err != nil || !reverse.Rollback {
						t.Fatal("missing durable reverse decision", err)
					}
					// This is a new journal decision; pin its before-image across retries.
					journal = renumberRead(t, marker)
					current, err = readLedger(e.control)
					if err != nil {
						t.Fatal(err)
					}
					if err = recoverTransaction(e.ctx, e.control, current, reverse, renumberFault("ledger")); !errors.Is(err, renumberCrash) {
						t.Fatal("reverse cannot replay its refreshed after-image", err)
					}
					if !bytes.Equal(journal, renumberRead(t, marker)) {
						t.Fatal("reverse recovery rewrote journal before-images")
					}
					_, err = Commit(e.ctx, Request{ControlRoot: e.control, RollbackPending: true})
					if err != nil {
						t.Fatal("rollback retry did not converge", err)
					}
				} else {
					// Existing base recovery function provides a durable fault boundary. Normal
					// forward Commit ignores Request.Fault during recovery on base; no new seam.
					for attempt := 0; attempt < 2; attempt++ {
						if err = recoverTransaction(e.ctx, e.control, current, tx, renumberFault("ledger")); !errors.Is(err, renumberCrash) {
							t.Fatal("forward cannot replay renumbered/refreshed after-image", err)
						}
						if !bytes.Equal(journal, renumberRead(t, marker)) {
							t.Fatal("forward recovery rewrote transaction before-images")
						}
						current, err = readLedger(e.control)
						if err != nil {
							t.Fatal(err)
						}
						renumberFresh(t, current)
					}
					_, err = Commit(e.ctx, Request{ControlRoot: e.control, RecoverOnly: true})
					if err != nil {
						t.Fatal("forward retry did not converge", err)
					}
				}
				if _, err = os.Lstat(marker); !os.IsNotExist(err) {
					t.Fatal("journal did not retire", err)
				}
				snap, err := ReadInstalledSnapshot(e.control)
				if err != nil || snap.Recovery {
					t.Fatal("converged snapshot", err)
				}
				renumberFresh(t, snap.Ledger)
				wantNative := tx.After.Native.Path
				payload := filepath.Join(e.target, "recovery-payload")
				if rollback {
					// Publication retains the newest compatible callback for pending
					// notifications. Only an unpublished transaction reselects A.
					if boundary == "transaction" {
						wantNative = tx.Before.Native.Path
					}
					if _, err := os.Lstat(payload); !os.IsNotExist(err) {
						t.Fatalf("rollback retained new payload: %v", err)
					}
				} else if got := renumberRead(t, payload); string(got) != "new bytes" {
					t.Fatalf("forward recovery payload: %q", got)
				}
				if snap.Ledger.Native.Path != wantNative {
					t.Fatalf("recovery selected %s; want %s", snap.Ledger.Native.Path, wantNative)
				}
				if !rollback || boundary != "transaction" {
					for _, path := range paths {
						if _, err = os.Stat(path); err != nil {
							t.Fatal("published generation lost in recovery", path, err)
						}
					}
				}
			})
		}
	}
}
func TestRenumberE2EDarwinPurgeRecovery(t *testing.T) {
	for _, boundary := range []string{"transaction", "ledger", "purge-entry"} {
		t.Run(boundary, func(t *testing.T) {
			e := renumberSandbox(t)
			renumberAPFS(t, e)
			tx, paths := renumberPending(t, e, boundary, true)
			marker := filepath.Join(e.control, "transaction.json")
			before := renumberRead(t, marker)
			current, err := readLedger(e.control)
			if err != nil {
				t.Fatal(err)
			}
			if err = recoverTransaction(e.ctx, e.control, current, tx, renumberFault("ledger")); !errors.Is(err, renumberCrash) {
				t.Fatal("purge recovery re-crash", err)
			}
			if !bytes.Equal(before, renumberRead(t, marker)) {
				t.Fatal("purge recovery rewrote ownership journal")
			}
			if _, err = Commit(e.ctx, Request{ControlRoot: e.control, RecoverOnly: true}); err != nil {
				t.Fatal("purge recovery retry", err)
			}
			for _, path := range paths {
				if _, err = os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("purge left generation", path, err)
				}
			}
			snap, err := ReadInstalledSnapshot(e.control)
			if err != nil || snap.Recovery || snap.Ledger.Native != nil {
				t.Fatal("purge did not converge", err)
			}
		})
	}
}

func TestRenumberE2EDarwinRefusals(t *testing.T) {
	for _, kind := range []string{"inode", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			e := renumberSandbox(t)
			l := e.install(t, e.signedStage(t, "refuse"))
			renumberLedger(t, e.control)
			path := l.Native.Path
			retained := path + ".retained"
			if err := os.Rename(path, retained); err != nil {
				t.Fatal(err)
			}
			if kind == "symlink" {
				if err := os.Symlink(retained, path); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(path, 0755); err != nil {
					t.Fatal(err)
				}
				if err := copyNativeTree(retained, path); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ReadInstalledSnapshot(e.control); err == nil {
				t.Fatal("snapshot accepted substituted native", kind)
			}
			if out, err := e.command("internal-install-runtime", "--remove", "--purge-native", "--target", e.target, "--control-root", e.control); err == nil {
				t.Fatalf("purge accepted substitution: %s", out)
			}
			if got, err := treeFingerprint(retained); err != nil || got != l.Native.SHA256 {
				t.Fatal("refusal harmed retained original", err)
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatal("refusal deleted substitute", err)
			}
		})
	}
	for _, kind := range []string{"renumber", "inode", "content"} {
		t.Run("purge-file/"+kind, func(t *testing.T) {
			e := renumberSandbox(t)
			dir := filepath.Join(e.root, "tree")
			path := filepath.Join(dir, "file")
			renumberWrite(t, path, []byte("same bytes"), 0600)
			entries, err := capturePurgeTree(dir)
			if err != nil {
				t.Fatal(err)
			}
			var rewritten map[string]PurgeEntry
			if err = json.Unmarshal(renumberPersisted(t, entries), &rewritten); err != nil {
				t.Fatal(err)
			}
			if kind == "inode" {
				if err = os.Rename(path, path+".keep"); err != nil {
					t.Fatal(err)
				}
				renumberWrite(t, path, []byte("same bytes"), 0600)
				if err = os.Remove(path + ".keep"); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "content" {
				renumberWrite(t, path, []byte("foreign bytes"), 0600)
			}
			err = cleanupPurgeTree(PurgeTree{Path: dir, Entries: rewritten}, nil)
			if kind == "renumber" {
				if err != nil {
					t.Fatal("purge must tolerate regular file dev renumber", err)
				}
				if _, err = os.Stat(dir); !os.IsNotExist(err) {
					t.Fatal("purge did not remove owned tree", err)
				}
			} else {
				if err == nil {
					t.Fatal("purge accepted replaced file", kind)
				}
				if _, err = os.Stat(path); err != nil {
					t.Fatal("refusal deleted foreign file", err)
				}
			}
		})
	}
}

// Real volume boundaries only run in a disposable GitHub macOS machine. The
// image and all mountpoints are under RUNNER_TEMP and detached before removal.
func TestRenumberE2EDarwinMountRefusal(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_TEMP") == "" {
		t.Skip("hdiutil mount E2E requires ephemeral GitHub runner and RUNNER_TEMP")
	}
	e := renumberSandbox(t)
	runner, err := filepath.EvalSymlinks(os.Getenv("RUNNER_TEMP"))
	if err != nil {
		t.Fatal(err)
	}
	volume, err := os.MkdirTemp(runner, "renumber-volume-")
	if err != nil {
		t.Fatal(err)
	}
	mount := filepath.Join(volume, "native", "Fixture.app", "Contents", "Resources", "mounted")
	if err = os.MkdirAll(mount, 0700); err != nil {
		t.Fatal(err)
	}
	attached := false
	t.Cleanup(func() {
		// An attach that returns an error can still have mounted the image.
		// Never recurse into a mounted tree before detaching our own volume.
		var leaf, parent unix.Stat_t
		if unix.Stat(mount, &leaf) == nil && unix.Stat(filepath.Dir(mount), &parent) == nil && leaf.Dev != parent.Dev {
			attached = true
		}
		if attached {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			out, err := exec.CommandContext(cleanupCtx, "hdiutil", "detach", mount, "-force").CombinedOutput()
			if err != nil {
				t.Errorf("detach failed; retained %s: %v %s", volume, err, out)
				return
			}
		}
		if err := os.RemoveAll(volume); err != nil {
			t.Error(err)
		}
	})
	image := filepath.Join(volume, "test.dmg")
	if out, err := exec.CommandContext(e.ctx, "hdiutil", "create", "-size", "32m", "-fs", "APFS", "-volname", "ANRenumberE2E", image).CombinedOutput(); err != nil {
		t.Skipf("disposable APFS image unsupported: %v %s", err, out)
	}
	if out, err := exec.CommandContext(e.ctx, "hdiutil", "attach", image, "-nobrowse", "-mountpoint", mount).CombinedOutput(); err != nil {
		t.Skipf("disposable APFS mount unsupported: %v %s", err, out)
	}
	attached = true
	id, err := nativeDirectoryID(mount)
	if err != nil {
		t.Fatal(err)
	}
	if err = checkNativeDirectoryID(mount, e2eRenumberID(t, id)); err == nil {
		t.Fatal("accepted renumber relaxation across mounted root boundary")
	}
	// Create a journal whose stored grouping says the mounted ancestor was on
	// its parent's volume, keeping every actual inode and content fingerprint.
	control := filepath.Join(volume, "control")
	target := filepath.Join(mount, "runtime", "file")
	r := Request{ControlRoot: control, RuntimeRoot: filepath.Dir(target), Owner: "existing-installer", ConsumerID: "mount-fixture", Files: []File{{Path: target, Data: []byte("managed"), Mode: 0600}}, Fault: renumberFault("transaction")}
	if _, err = Commit(e.ctx, r); !errors.Is(err, renumberCrash) {
		t.Fatal("mount journal setup", err)
	}
	marker := filepath.Join(control, "transaction.json")
	tx, err := readTransactionFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	mountID := id
	parentID, err := nativeDirectoryID(filepath.Dir(mount))
	if err != nil {
		t.Fatal(err)
	}
	mountDev := strings.Split(mountID, ":")[0]
	parentDev := strings.Split(parentID, ":")[0]
	changed := false
	for i := range tx.Files {
		for j := range tx.Files[i].Parents {
			a := &tx.Files[i].Parents[j]
			parts := strings.Split(a.Identity, ":")
			if parts[0] == mountDev {
				a.Identity = parentDev + ":" + parts[1]
				changed = true
			}
		}
	}
	if !changed {
		t.Fatal("mount ancestor not captured")
	}
	if err = writeTransaction(marker, tx); err != nil {
		t.Fatal(err)
	}
	before := renumberRead(t, marker)
	if _, err = Commit(e.ctx, Request{ControlRoot: control, RecoverOnly: true}); err == nil {
		t.Fatal("recovery accepted changed ancestor device grouping")
	}
	if !bytes.Equal(before, renumberRead(t, marker)) {
		t.Fatal("mount refusal rewrote journal")
	}
	if _, err = os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("mount refusal published payload", err)
	}
}

func TestRenumberE2EDarwinSetupOwnership(t *testing.T) {
	e := renumberSandbox(t)
	stage := e.signedStage(t, "setup")
	// A signed, attested native fixture gives real OS qualification; no injected
	// floor, no mocked setup verifier, and no notification-capable executable.
	l := e.install(t, stage)
	if l.Native.DecoderFloor != 1 {
		t.Fatal("native fixture not qualified")
	}
	global := filepath.Join(e.root, "global.json")
	renumberWrite(t, global, []byte(`{"notifications":{"desktop":{"enabled":false,"sound":false,"clickToFocus":false}}}`), 0600)
	enable := func() ([]byte, error) {
		l, err := readLedger(e.control)
		if err != nil {
			t.Fatal(err)
		}
		return e.command("setup-notifications", "enable", "--control-root", e.control, "--runtime-root", filepath.Dir(e.target), "--global-config", global, "--expected-generation", fmt.Sprint(l.Generation), "--navigation", "none", "--allow-unknown-caller", "false", "--allow-caller-asserted", "false", "--json")
	}
	if out, err := enable(); err != nil {
		t.Fatalf("setup fixture initialization: %v %s", err, out)
	}
	policyPath := filepath.Join(e.control, "agent-notifications.json")
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(renumberRead(t, policyPath), &fields); err != nil {
		t.Fatal(err)
	}
	raw, ok := fields["setupState"]
	if !ok {
		t.Fatal("CLI did not provision ownership")
	}
	var owner map[string]any
	if err := json.Unmarshal(raw, &owner); err != nil {
		t.Fatal(err)
	}
	oldOwner := map[string]any{}
	for k, v := range owner {
		oldOwner[k] = v
	}
	owner["directoryID"] = e2eRenumberID(t, owner["directoryID"].(string))
	fields["setupState"], _ = json.Marshal(owner)
	body, _ := json.Marshal(fields)
	renumberWrite(t, policyPath, body, 0600)
	if out, err := enable(); err != nil {
		t.Fatalf("setup rejects same-inode device renumber: %v %s", err, out)
	}
	if err := json.Unmarshal(renumberRead(t, policyPath), &fields); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fields["setupState"], &owner); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"token", "namespace", "installationID"} {
		if !reflect.DeepEqual(owner[key], oldOwner[key]) {
			t.Fatal("setup reinitialized ownership", key)
		}
	}
	state := filepath.Join(e.control, "state")
	original := state + ".original"
	if err := os.Rename(state, original); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := copyNativeTree(original, state); err != nil {
		t.Fatal(err)
	}
	if out, err := enable(); err == nil {
		t.Fatalf("setup accepted replaced state inode: %s", out)
	}
	if _, err := os.Stat(filepath.Join(original, "journal")); err != nil {
		t.Fatal("setup refusal harmed original journal", err)
	}
}

func renumberSignedApp(t *testing.T, e *renumberEnv, app string) {
	t.Helper()
	source := filepath.Join(e.root, "native.c")
	renumberWrite(t, source, []byte(`#include <stdio.h>
#include <string.h>
int main(int argc, char **argv) {
 if (argc == 2 && strcmp(argv[1], "--capabilities-json") == 0) {
  puts("{\"schemaVersion\":1,\"protocolVersions\":[1],\"actionKinds\":[\"none\"],\"receiptSupport\":true,\"backend\":\"macos.usernotifications\",\"explicitFeatureEnabledByDefault\":false}");
  return 0;
 }
 return 97; /* No notification or application-launch implementation. */
}
`), 0600)
	exe := filepath.Join(app, "Contents", "MacOS", "terminal-notifier-modern")
	if out, err := exec.CommandContext(e.ctx, "cc", source, "-o", exe).CombinedOutput(); err != nil {
		t.Fatalf("compile inert native fixture: %v %s", err, out)
	}
	renumberWrite(t, filepath.Join(app, "Contents", "Info.plist"), []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.777genius.agent-notifications</string><key>CFBundleExecutable</key><string>terminal-notifier-modern</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleVersion</key><string>1</string></dict></plist>`), 0644)
	renumberWrite(t, filepath.Join(app, "Contents", "Resources", "managed-runtime.json"), []byte(`{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1}`), 0644)
	if out, err := exec.CommandContext(e.ctx, "codesign", "--force", "--sign", "-", "--timestamp=none", "--identifier", "com.777genius.agent-notifications", app).CombinedOutput(); err != nil {
		t.Fatalf("sign native fixture: %v %s", err, out)
	}
	attestation := fmt.Sprintf(`{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1,"ExecutableSHA256":"%x"}`, sha256.Sum256(renumberRead(t, exe)))
	renumberWrite(t, app+".managed-runtime.json", []byte(attestation), 0600)
}

// Darwin core flows use the host sender basename, real signature/architecture
// checks, and an externally attested fixture. Only its delivery is inert.
func (e *renumberEnv) signedStage(t *testing.T, marker string) string {
	t.Helper()
	stage := e.stage(t, marker)
	app := filepath.Join(stage, "ClaudeNotifier.app")
	renumberWrite(t, filepath.Join(app, "Contents", "Resources", "generation.marker"), []byte(marker), 0644)
	renumberSignedApp(t, e, app)
	from := filepath.Join(stage, "claude-notifications-linux-amd64")
	to := filepath.Join(stage, "claude-notifications-darwin-"+runtime.GOARCH)
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
	return stage
}
