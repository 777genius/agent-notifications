//go:build linux || darwin

package installruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeParentIdentityPersistsThroughRecovery(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		t.Run(fmt.Sprint(recovery), func(t *testing.T) {
			ctx, r := request(t)
			var err error
			r.Native, err = StageNative(ctx, r.ControlRoot, nativeFixture(t))
			if err != nil {
				t.Fatal(err)
			}
			if recovery {
				r.Fault = func(phase string) error {
					if phase == "transaction" {
						return fmt.Errorf("crash")
					}
					return nil
				}
				if _, err = Commit(ctx, r); err == nil {
					t.Fatal("missing crash")
				}
				r.Fault = nil
			}
			parent := filepath.Dir(r.Native.Staged)
			if err = os.Rename(parent, parent+"-original"); err != nil {
				t.Fatal(err)
			}
			if err = os.Mkdir(parent, 0700); err != nil {
				t.Fatal(err)
			}
			// An ordinary directory replacement, not only a symlink, must be refused.
			sentinel := filepath.Join(parent, "foreign")
			if err = os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			if recovery {
				r.Native = nil
			}
			if _, err = Commit(ctx, r); err == nil {
				t.Fatal("native parent replacement accepted")
			}
			got, err := os.ReadFile(sentinel)
			if err != nil || string(got) != "preserve" {
				t.Fatal("foreign parent mutated", err)
			}
		})
	}
}

func TestNativeRootAndNestedLinksRefused(t *testing.T) {
	source := nativeFixture(t)
	alias := filepath.Join(t.TempDir(), "bundle")
	if err := os.Symlink(source, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := treeFingerprint(alias); err == nil {
		t.Fatal("linked native root accepted")
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(source, "foreign")); err != nil {
		t.Fatal(err)
	}
	if _, err := treeFingerprint(source); err == nil {
		t.Fatal("nested native link accepted")
	}
	if err := copyNativeTree(source, t.TempDir()); err == nil {
		t.Fatal("nested source link copied")
	}
}

func TestDuplicateNativeCannotDeleteArbitraryPath(t *testing.T) {
	ctx, r := request(t)
	var err error
	r.Native, err = StageNative(ctx, r.ControlRoot, nativeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	foreign := t.TempDir()
	sentinel := filepath.Join(foreign, "keep")
	if err = os.WriteFile(sentinel, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	r.Native.Before = r.Native.After
	r.Native.Staged = foreign
	if _, err = Commit(ctx, r); err == nil {
		t.Fatal("arbitrary cleanup path accepted")
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "foreign" {
		t.Fatal("foreign cleanup", err)
	}
}

func TestNativeOutputOverflowIsRecorded(t *testing.T) {
	var output limitedOutput
	if _, err := output.Write(make([]byte, 16385)); err == nil || !output.overflow {
		t.Fatal("missing overflow evidence")
	}
	if len(output.data) != 0 {
		t.Fatal("unbounded output retained")
	}
}

func TestNativeStagedInodeReplacementRefused(t *testing.T) {
	ctx, r := request(t)
	var err error
	source := nativeFixture(t)
	r.Native, err = StageNative(ctx, r.ControlRoot, source)
	if err != nil {
		t.Fatal(err)
	}
	old := r.Native.Staged + "-original"
	if err = os.Rename(r.Native.Staged, old); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(r.Native.Staged, 0700); err != nil {
		t.Fatal(err)
	}
	if err = copyNativeTree(old, r.Native.Staged); err != nil {
		t.Fatal(err)
	}
	if digest, err := treeFingerprint(r.Native.Staged); err != nil || digest != r.Native.After.SHA256 {
		t.Fatal("fixture bytes differ", err)
	}
	if _, err = Commit(ctx, r); err == nil {
		t.Fatal("identical replacement inode accepted")
	}
	if _, err = os.Stat(r.Native.After.Path); !os.IsNotExist(err) {
		t.Fatal("foreign candidate promoted")
	}
}

func TestNativeFirstPromotionCannotReplaceForeignDirectory(t *testing.T) {
	root := t.TempDir()
	staged, live := filepath.Join(root, ".candidate-new"), filepath.Join(root, "ClaudeNotifier.app")
	for _, path := range []string{staged, live} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := renameNative(staged, live, nil); err == nil {
		t.Fatal("foreign empty directory replaced")
	}
	for _, path := range []string{staged, live} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("directory lost", err)
		}
	}
}

func TestNativePurgeRecoveryRejectsIdenticalReplacement(t *testing.T) {
	ctx, r := request(t)
	var err error
	r.Native, err = StageNative(ctx, r.ControlRoot, nativeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	r.Native = nil
	r.RemoveConsumer, r.PurgeNative = true, true
	r.Fault = func(phase string) error {
		if phase == "transaction" {
			return fmt.Errorf("crash")
		}
		return nil
	}
	if _, err = Commit(ctx, r); err == nil {
		t.Fatal("missing crash")
	}
	live := ledger.Native.Path
	if err = os.Rename(live, live+"-original"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(live, 0700); err != nil {
		t.Fatal(err)
	}
	if err = copyNativeTree(live+"-original", live); err != nil {
		t.Fatal(err)
	}
	r.Fault = nil
	if _, err = Commit(ctx, r); err == nil {
		t.Fatal("purge accepted substituted inode")
	}
	if _, err = os.Stat(live); err != nil {
		t.Fatal("foreign live directory moved before refusal", err)
	}
}

func TestFailedStageCleanupPreservesReplacement(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, ".candidate-fixture")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	anchors, err := pathAnchors(stage, false)
	if err != nil {
		t.Fatal(err)
	}
	id, err := nativeDirectoryID(stage)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(stage, stage+"-original"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(stage, "foreign")
	if err = os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	// The failed candidate's identity must remain authoritative during cleanup.
	if err = removeFailedNativeStage(stage, anchors, id); err == nil {
		t.Error("replacement accepted for failed-stage cleanup")
	}
	if _, err = os.Stat(sentinel); err != nil {
		t.Fatal("foreign candidate deleted", err)
	}
}

func TestNativeCopyUsesOpenedSourceAfterPathReplacement(t *testing.T) {
	source := nativeFixture(t)
	expected, err := treeFingerprint(source)
	if err != nil {
		t.Fatal(err)
	}
	held, err := openNativeRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err = os.Rename(source, source+"-original"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "foreign"), []byte("must not copy"), 0600); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err = copyOpenedNativeTree(held, target); err != nil {
		t.Fatal(err)
	}
	if got, err := treeFingerprint(target); err != nil || got != expected {
		t.Fatal("copy followed replaced source pathname", err)
	}
}

func TestNativeRenameChecksSourceInsideHeldParent(t *testing.T) {
	root := t.TempDir()
	source, target := filepath.Join(root, ".candidate-source"), filepath.Join(root, "live")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	id, err := nativeDirectoryID(source)
	if err != nil {
		t.Fatal(err)
	}
	anchors, err := pathAnchors(target, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(source, source+"-original"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err = renameNative(source, target, anchors, id); err == nil {
		t.Fatal("replacement source renamed")
	}
	if _, err = os.Stat(source); err != nil {
		t.Fatal("foreign source moved", err)
	}
}
