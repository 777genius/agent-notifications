//go:build darwin

package installruntime

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeGenerationRemainsExecutableAfterUpdate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	_, r := request(t)
	first := exactHeadNativeApp(t, "generation-A")
	change, err := StageNative(ctx, r.ControlRoot, first)
	if err != nil {
		t.Fatal(err)
	}
	r.Native = change
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	pathA := change.After.Path
	inodeA, err := nativeDirectoryID(pathA)
	if err != nil {
		t.Fatal(err)
	}
	exeA := filepath.Join(pathA, "Contents", "MacOS", "terminal-notifier-modern")
	probe := func() {
		t.Helper()
		cmd := exec.CommandContext(ctx, exeA, "--capabilities-json")
		cmd.Dir = "/"
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("generation A not executable: %s %v", out, err)
		}
		if !bytes.Contains(out, []byte("protocolVersion")) && !bytes.Contains(out, []byte("{")) {
			t.Fatalf("capabilities missing: %s", out)
		}
	}
	probe()
	second := exactHeadNativeApp(t, "generation-B")
	change, err = StageNative(ctx, r.ControlRoot, second)
	if err != nil {
		t.Fatal(err)
	}
	r.Native = change
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	id, err := nativeDirectoryID(pathA)
	if err != nil || id != inodeA {
		t.Fatal("generation A identity moved")
	}
	if _, err := os.Stat(pathA); err != nil {
		t.Fatal("generation A disappeared")
	}
	probe()
	cmd := exec.CommandContext(ctx, "/usr/bin/open", "-n", "-W", "-a", pathA, "--args", "--capabilities-json")
	cmd.Dir = "/"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("LaunchServices path launch of generation A failed after update: %s %v", out, err)
	}
}
