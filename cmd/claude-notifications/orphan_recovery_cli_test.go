//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Keep ownership evidence on failure instead of letting TempDir erase an
// interrupted fixture. Every successful mutation test unregisters first.
func orphanCLIUnitRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp(os.Getenv("TMPDIR"), "TEST-orphan-cli-unit-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("preserved failed ownership fixture: %s", root)
			return
		}
		ledger, pending, err := installruntime.ReadOwnership(filepath.Join(root, "control"))
		if err != nil || pending || len(ledger.Consumers) != 0 {
			t.Errorf("ownership leak; preserved %s: %v", root, err)
			return
		}
		if os.Getenv("ORPHAN_CLI_KEEP_FIXTURES") == "1" {
			t.Logf("preserved verified fixture: %s", root)
			return
		}
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	return root
}

func TestOrphanRecoveryCLIRejectsIncompleteAndConflictingFlags(t *testing.T) {
	root := orphanCLIUnitRoot(t)
	valid := []string{"--recover-orphan-consumer", "--consumer", "selected", "--runtime-root", filepath.Join(root, "orphan"), "--control-root", filepath.Join(root, "control"), "--expected-installation-id", "installation", "--expected-generation", "1", "--json"}
	for _, key := range []string{"--consumer", "--runtime-root", "--control-root", "--expected-installation-id", "--expected-generation"} {
		t.Run("missing-"+key, func(t *testing.T) {
			args := append([]string(nil), valid...)
			for i, s := range args {
				if s == key {
					args = append(args[:i], args[i+2:]...)
					break
				}
			}
			if err := installRuntime(args, io.Discard); err == nil {
				t.Fatal("missing fence accepted")
			}
		})
	}
	for _, extra := range [][]string{
		{"--stage", "/abs"}, {"--target", "/abs"}, {"--entry", "sender"}, {"--refresh=false"}, {"--remove=false"}, {"--purge-native=false"}, {"--print-native-path=false"}, {"--relocate-versioned-cache=false"}, {"--require-native=false"}, {"--recover-pending"}, {"--rollback-pending"}, {"--expected-generation", "0"}, {"--runtime-root", "relative"}, {"--runtime-root", root + "/../escape"}, {"--consumer", "bad\nvalue"}, {"--force"}, {"--hash", "arbitrary"}, {"positional"},
	} {
		t.Run(strconv.Itoa(len(extra))+extra[0], func(t *testing.T) {
			if err := installRuntime(append(append([]string(nil), valid...), extra...), io.Discard); err == nil {
				t.Fatal("conflict accepted")
			}
		})
	}
	for _, mode := range []string{"--recover-pending", "--rollback-pending"} {
		for _, extra := range [][]string{{"--consumer", "orphan"}, {"--expected-generation", "1"}, {"--expected-installation-id", "id"}, {"--runtime-root", root}, {"--dry-run=false"}, {"--stage", root}, {"--remove=false"}} {
			if err := installRuntime(append([]string{mode, "--control-root", filepath.Join(root, "control")}, extra...), io.Discard); err == nil {
				t.Fatalf("pending accepted %v", extra)
			}
		}
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("parser created state: %v %v", entries, err)
	}
}

func TestOrphanRecoveryCLIPreviewDoesNotCreateMissingControl(t *testing.T) {
	root := orphanCLIUnitRoot(t)
	var out bytes.Buffer
	err := installRuntime([]string{"--recover-orphan-consumer", "--consumer", "selected", "--runtime-root", filepath.Join(root, "orphan"), "--control-root", filepath.Join(root, "control"), "--expected-installation-id", "id", "--expected-generation", "1", "--dry-run", "--json"}, &out)
	if err == nil {
		t.Fatal("missing installation admitted")
	}
	var r runtimeRecoveryResult
	if json.Unmarshal(out.Bytes(), &r) != nil || r.Admissible || r.ConflictCode == "" {
		t.Fatalf("unbounded/invalid result %s", &out)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("preview created lock/control: %v %v", entries, err)
	}
}

func TestOrphanRecoveryCLIRejectsContaminatedSelectionBeforeObservation(t *testing.T) {
	root := orphanCLIUnitRoot(t)
	for _, consumer := range []string{"bad\nvalue", "bad\u0085value", "bad\xffvalue"} {
		var out bytes.Buffer
		err := installRuntime([]string{"--recover-orphan-consumer", "--consumer", consumer, "--runtime-root", filepath.Join(root, "orphan"), "--control-root", filepath.Join(root, "control"), "--expected-installation-id", "id", "--expected-generation", "1", "--dry-run", "--json"}, &out)
		if err == nil || out.Len() != 0 || !strings.Contains(err.Error(), "requires explicit consumer") {
			t.Fatalf("contaminated selection reached observation: error=%v output=%q", err, out.String())
		}
	}
}

// The interrupted after-image no longer has the selected consumer. Pending
// rollback must use its durable decision rather than reselecting that consumer.
func TestOrphanRecoveryCLIPendingIsStandaloneAfterConsumerDisappears(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(strconv.FormatBool(rollback), func(t *testing.T) {
			root := orphanCLIUnitRoot(t)
			control, primary, orphan := filepath.Join(root, "control"), filepath.Join(root, "primary"), filepath.Join(root, "orphan")
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			_, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, Owner: "existing-installer", RuntimeRoot: primary, ConsumerID: "retained", Files: []installruntime.File{{Path: filepath.Join(primary, "keep"), Data: []byte("keep"), Mode: 0600}}})
			if err != nil {
				t.Fatal(err)
			}
			l, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, Owner: "existing-installer", RuntimeRoot: orphan, ConsumerID: "selected", Files: []installruntime.File{{Path: filepath.Join(orphan, "gone"), Data: []byte("gone"), Mode: 0600}}})
			if err != nil {
				t.Fatal(err)
			}
			if err = os.RemoveAll(orphan); err != nil {
				t.Fatal(err)
			}
			req := installruntime.OrphanRecoveryRequest{InstallationID: l.ID, ExpectedGeneration: l.Generation, ConsumerID: "selected", RuntimeRoot: orphan, Consumer: l.Consumers["selected"]}
			interrupted := errors.New("TEST ledger interruption")
			_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, OrphanRecovery: &req, Fault: func(phase string) error {
				if phase == "ledger" {
					return interrupted
				}
				return nil
			}})
			if !errors.Is(err, interrupted) {
				t.Fatalf("fault not reached: %v", err)
			}
			after, pending, err := installruntime.ReadOwnership(control)
			if err != nil || !pending {
				t.Fatal("marker missing", err)
			}
			if _, ok := after.Consumers["selected"]; ok {
				t.Fatal("after-image still has selected consumer")
			}
			mode := "--recover-pending"
			if rollback {
				mode = "--rollback-pending"
			}
			// Keep the supported schema5 marker and all its embedded before-images.
			// Only the TEST live ledger preimage acquires an unknown key.
			ledgerPath := filepath.Join(control, "ownership.json")
			validPreimage := embeddedRead(t, ledgerPath)
			marker := embeddedRead(t, filepath.Join(control, "transaction.json"))
			var envelope struct{ Transaction json.RawMessage }
			if err := json.Unmarshal(marker, &envelope); err != nil {
				t.Fatal(err)
			}
			var decision struct{ Schema int }
			if err := json.Unmarshal(envelope.Transaction, &decision); err != nil || decision.Schema != 5 {
				t.Fatalf("expected supported schema5 marker: schema=%d err=%v", decision.Schema, err)
			}
			invalidPreimage := append([]byte("{\"unexpected\":true,"), validPreimage[1:]...)
			embeddedPut(t, filepath.Join(root, "evidence", "valid-ledger.json"), validPreimage, 0600)
			embeddedPut(t, filepath.Join(root, "evidence", "schema5-marker.json"), marker, 0600)
			embeddedPut(t, filepath.Join(root, "evidence", "unknown-key-ledger.json"), invalidPreimage, 0600)
			embeddedPut(t, ledgerPath, invalidPreimage, 0600)
			beforeRejection := orphanCLITree(t, root)
			var rejected bytes.Buffer
			err = installRuntime([]string{mode, "--control-root", control, "--json"}, &rejected)
			var refusal runtimeRecoveryResult
			if err == nil || json.Unmarshal(rejected.Bytes(), &refusal) != nil || refusal.Admissible {
				t.Fatalf("unknown ownership key admitted with valid schema5 marker: %v %s", err, &rejected)
			}
			if !reflect.DeepEqual(beforeRejection, orphanCLITree(t, root)) ||
				!bytes.Equal(marker, embeddedRead(t, filepath.Join(control, "transaction.json"))) ||
				!bytes.Equal(invalidPreimage, embeddedRead(t, ledgerPath)) {
				t.Fatal("strict pending rejection changed full tree, marker or preimages")
			}
			// Restore only this test's live ledger; replay uses the original marker.
			embeddedPut(t, ledgerPath, validPreimage, 0600)
			var out bytes.Buffer
			if err = installRuntime([]string{mode, "--control-root", control, "--json"}, &out); err != nil {
				t.Fatal(err, &out)
			}
			embeddedPut(t, filepath.Join(root, "evidence", "negative-output.json"), rejected.Bytes(), 0600)
			embeddedPut(t, filepath.Join(root, "evidence", "positive-output.json"), out.Bytes(), 0600)
			embeddedPut(t, filepath.Join(root, "evidence", "converged-ledger.json"), embeddedRead(t, ledgerPath), 0600)
			result, pending, err := installruntime.ReadOwnership(control)
			if err != nil || pending {
				t.Fatal("pending not converged", err)
			}
			_, restored := result.Consumers["selected"]
			if restored != rollback {
				t.Fatalf("restored=%t rollback=%t", restored, rollback)
			}
			if !reflect.DeepEqual(result.Consumers["retained"], l.Consumers["retained"]) {
				t.Fatal("retained metadata changed")
			}
			if _, err = os.Lstat(orphan); !os.IsNotExist(err) {
				t.Fatal("pending recreated payload", err)
			}
			_, health := installruntime.ReadInstalledSnapshot(control)
			if (health != nil) != rollback {
				t.Fatalf("rollback health misreported: %v", health)
			}
			// Restore exact owned assets before unregistering this test's consumers.
			if rollback {
				embeddedPut(t, filepath.Join(orphan, "gone"), []byte("gone"), 0600)
				if _, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, Owner: "existing-installer", RuntimeRoot: orphan, ConsumerID: "selected", RemoveConsumer: true}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, Owner: "existing-installer", RuntimeRoot: primary, ConsumerID: "retained", RemoveConsumer: true}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOrphanRecoveryCLIPreviewIsBoundedAndDoesNotAcquireLocks(t *testing.T) {
	root := orphanCLIUnitRoot(t)
	control, primary, orphan := filepath.Join(root, "control"), filepath.Join(root, "primary"), filepath.Join(root, "orphan")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, Owner: "existing-installer", RuntimeRoot: primary, ConsumerID: "retained", Files: []installruntime.File{{Path: filepath.Join(primary, "keep"), Data: []byte("retained secret body"), Mode: 0600}}})
	if err != nil {
		t.Fatal(err)
	}
	files := make([]installruntime.File, 257)
	for i := range files {
		files[i] = installruntime.File{Path: filepath.Join(orphan, strconv.Itoa(1000+i)), Data: []byte("orphan secret body"), Mode: 0600}
	}
	l, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, Owner: "existing-installer", RuntimeRoot: orphan, ConsumerID: "selected", Files: files})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(orphan); err != nil {
		t.Fatal(err)
	}
	unlockComponent, err := installruntime.LockExisting(ctx, filepath.Join(control, ".component-install.lock"))
	if err != nil {
		t.Fatal(err)
	}
	unlockPolicy, err := installruntime.LockExisting(ctx, filepath.Join(control, "agent-notifications.json.lock"))
	if err != nil {
		unlockComponent()
		t.Fatal(err)
	}
	before := orphanCLITree(t, control)
	var out bytes.Buffer
	args := []string{"--recover-orphan-consumer", "--consumer", "selected", "--runtime-root", orphan, "--control-root", control, "--expected-installation-id", l.ID, "--expected-generation", strconv.FormatUint(l.Generation, 10), "--dry-run", "--json"}
	done := make(chan error, 1)
	go func() { done <- installRuntime(args, &out) }()
	timedOut := false
	select {
	case err = <-done:
	case <-time.After(2 * time.Second):
		timedOut = true
	}
	unlockPolicy()
	unlockComponent()
	if timedOut {
		err = <-done
		t.Fatalf("preview waited for permanent locks: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	var projected runtimeRecoveryResult
	if err = json.Unmarshal(out.Bytes(), &projected); err != nil || !projected.Admissible || projected.SelectedCount != 257 || len(projected.SelectedPaths) != 256 || !projected.PathsTruncated {
		t.Fatalf("bounded preview=%s err=%v", &out, err)
	}
	if !reflect.DeepEqual(before, orphanCLITree(t, control)) {
		t.Fatal("preview changed control bytes/modes/paths")
	}
	for _, secret := range []string{"secret body", "ExpectedPolicy", "Commands", "Consumers", "SHA256"} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("preview exposed %s", secret)
		}
	}
	if err = installRuntime(args[:len(args)-1], io.Discard); err != nil {
		t.Fatal(err)
	}
	// A real rejected adapter operation must retain the kernel's typed conflict
	// while main sees only the fixed public category.
	embeddedPut(t, files[0].Path, files[0].Data, 0600)
	var rejected bytes.Buffer
	publicErr := installRuntime(args, &rejected)
	var typed *installruntime.OrphanRecoveryConflict
	var public *runtimeRecoveryPublicError
	if publicErr == nil || !errors.As(publicErr, &typed) || !errors.As(publicErr, &public) || !errors.Is(publicErr, typed.Err) {
		t.Fatalf("actual adapter lost typed cause: %v", publicErr)
	}
	if len(publicErr.Error()) > 128 || strings.Contains(publicErr.Error(), root) || strings.ContainsAny(publicErr.Error(), "\n\x1b") {
		t.Fatal("actual adapter exposed raw cause")
	}
	if err := os.RemoveAll(orphan); err != nil {
		t.Fatal(err)
	}
	// Explicit cleanup removes metadata before removing the retained live assets.
	req := installruntime.OrphanRecoveryRequest{InstallationID: l.ID, ExpectedGeneration: l.Generation, ConsumerID: "selected", RuntimeRoot: orphan, Consumer: l.Consumers["selected"]}
	if _, err = installruntime.RecoverOrphanConsumer(ctx, control, req); err != nil {
		t.Fatal(err)
	}
	if _, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, Owner: "existing-installer", RuntimeRoot: primary, ConsumerID: "retained", RemoveConsumer: true}); err != nil {
		t.Fatal(err)
	}
	final, pending, err := installruntime.ReadOwnership(control)
	if err != nil || pending || len(final.Consumers) != 0 {
		t.Fatal("teardown leaked ownership", err)
	}
}

func TestOrphanRecoveryCLIPendingRefusesUnsafeStateWithoutMutation(t *testing.T) {
	for _, mode := range []string{"--recover-pending", "--rollback-pending"} {
		for _, state := range []string{"absent", "empty", "public", "invalid-ledger", "no-journal", "unsafe-ledger", "missing-lock"} {
			t.Run(mode+"/"+state, func(t *testing.T) {
				root, err := os.MkdirTemp(os.Getenv("TMPDIR"), "TEST-pending-guard-")
				if err != nil {
					t.Fatal(err)
				}
				root, err = filepath.EvalSymlinks(root)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if t.Failed() {
						t.Logf("PRESERVED %s", root)
						return
					}
					if os.Getenv("ORPHAN_CLI_KEEP_FIXTURES") == "1" {
						t.Logf("preserved verified negative fixture: %s", root)
						return
					}
					if err := os.RemoveAll(root); err != nil {
						t.Error(err)
					}
				})
				control := filepath.Join(root, "control")
				if state != "absent" {
					if err := os.Mkdir(control, 0700); err != nil {
						t.Fatal(err)
					}
				}
				if state == "public" {
					if err := os.Chmod(control, 0755); err != nil {
						t.Fatal(err)
					}
				}
				if state == "invalid-ledger" {
					embeddedPut(t, filepath.Join(control, "ownership.json"), []byte("{}"), 0600)
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				var originalLedger []byte
				if state == "no-journal" || state == "unsafe-ledger" || state == "missing-lock" {
					if _, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, Owner: "existing-installer", RuntimeRoot: filepath.Join(root, "primary"), ConsumerID: "retained"}); err != nil {
						t.Fatal(err)
					}
					originalLedger = embeddedRead(t, filepath.Join(control, "ownership.json"))
					if state == "unsafe-ledger" {
						path := filepath.Join(control, "ownership.json")
						if err := os.Rename(path, filepath.Join(root, "foreign-ledger")); err != nil {
							t.Fatal(err)
						}
						if err := os.Symlink(filepath.Join(root, "foreign-ledger"), path); err != nil {
							t.Fatal(err)
						}
					}
					if state == "missing-lock" {
						embeddedPut(t, filepath.Join(control, "transaction.json"), []byte("{}"), 0600)
						if err := os.Remove(filepath.Join(control, ".component-install.lock")); err != nil {
							t.Fatal(err)
						}
					}
				}
				before := orphanCLITree(t, root)
				var out bytes.Buffer
				err = installRuntime([]string{mode, "--control-root", control, "--json"}, &out)
				var r runtimeRecoveryResult
				if err == nil || out.Len() > 1024 || json.Unmarshal(out.Bytes(), &r) != nil || r.Admissible {
					t.Fatalf("unsafe pending admitted: %v %q", err, &out)
				}
				if !reflect.DeepEqual(before, orphanCLITree(t, root)) {
					t.Fatal("rejected pending created locks/root or changed preimages")
				}
				// No payload was installed; unregister validated test ownership before deletion.
				if originalLedger != nil {
					if state == "unsafe-ledger" {
						if err := os.Remove(filepath.Join(control, "ownership.json")); err != nil {
							t.Fatal(err)
						}
					}
					embeddedPut(t, filepath.Join(control, "ownership.json"), originalLedger, 0600)
					if state == "missing-lock" {
						if err := os.Remove(filepath.Join(control, "transaction.json")); err != nil {
							t.Fatal(err)
						}
					}
					if state == "missing-lock" {
						embeddedPut(t, filepath.Join(control, ".component-install.lock"), nil, 0600)
					}
					if _, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, Owner: "existing-installer", RuntimeRoot: filepath.Join(root, "primary"), ConsumerID: "retained", RemoveConsumer: true}); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestOrphanRecoveryCLIEvidenceOverrideRejectsFixtureAliases(t *testing.T) {
	root, err := os.MkdirTemp(os.Getenv("TMPDIR"), "TEST-evidence-namespace-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if t.Failed() {
			t.Logf("preserved failed evidence fixture: %s", root)
			return
		}
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove evidence fixture %s; remaining evidence preserved: %v", root, err)
		}
	}()
	for _, path := range []string{root, filepath.Join(root, "tmp", "evidence"), filepath.Join(root, "control"), filepath.Join(root, "codex", "evidence"), filepath.Dir(root), "relative"} {
		if err := orphanCLIValidateEvidence(root, path); err == nil {
			t.Fatalf("unsafe evidence accepted: %s", path)
		}
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(filepath.Dir(root), alias); err != nil {
		t.Fatal(err)
	}
	if err := orphanCLIValidateEvidence(root, filepath.Join(alias, "outside")); err == nil {
		t.Fatal("symlink evidence accepted")
	}
	if err := orphanCLIValidateEvidence(root, root+"-sibling"); err != nil {
		t.Fatal(err)
	}
}
