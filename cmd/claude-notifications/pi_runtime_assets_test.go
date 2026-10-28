package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	notificationassets "github.com/777genius/agent-notifications"
	"github.com/777genius/agent-notifications/internal/agentnotify/piauthority"
	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestPiRuntimePreparationOwnsCanonicalPresentationAssets(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(root, "stage")
	runtimeRoot := filepath.Join(root, "runtime")
	control := filepath.Join(root, "control")
	if err := os.MkdirAll(stage, 0700); err != nil {
		t.Fatal(err)
	}
	entry := "claude-notifications-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		entry += ".exe"
	}
	if err := os.WriteFile(filepath.Join(stage, entry), []byte("TEST inert writer "+installruntime.WriterProtocolMarker), 0700); err != nil {
		t.Fatal(err)
	}
	installerNativeFixture(t, stage)
	args := []string{"--stage", stage, "--target", filepath.Join(runtimeRoot, "bin"), "--entry", entry, "--control-root", control, "--consumer", piauthority.PreparationConsumer}
	if err := installRuntime(args, io.Discard); err != nil {
		t.Fatal(err)
	}
	snapshot, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Ledger.Consumers) != 1 || snapshot.Ledger.Consumers[piauthority.PreparationConsumer].RuntimeRoot != runtimeRoot {
		t.Fatalf("fake/missing preparation consumer: %+v", snapshot.Ledger.Consumers)
	}
	if snapshot.Enabled {
		t.Fatal("Pi byte preparation seeded unrelated portable service enablement")
	}
	for _, name := range []string{"claude_icon.png", "sounds/task-complete.mp3", "sounds/review-complete.mp3", "sounds/question.mp3", "sounds/plan-ready.mp3", "sounds/error.mp3"} {
		path := filepath.Join(runtimeRoot, filepath.FromSlash(name))
		wanted, err := notificationassets.NotificationAssets.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(body, wanted) {
			t.Fatal("canonical asset missing", name, err)
		}
		if _, ok := installruntime.OwnedFile(snapshot.Ledger, path); !ok {
			t.Fatal("asset not owned", name)
		}
	}
	if err := installRuntime(args, io.Discard); err != nil {
		t.Fatalf("idempotent preparation: %v", err)
	}
	path := filepath.Join(runtimeRoot, "sounds", "question.mp3")
	if err := os.WriteFile(path, []byte("TEST user edit"), 0600); err != nil {
		t.Fatal(err)
	}
	before, _, err := installruntime.ReadOwnership(control)
	if err != nil {
		t.Fatal(err)
	}
	if err := installRuntime(args, io.Discard); err == nil {
		t.Fatal("edited owned sound overwritten")
	}
	after, _, err := installruntime.ReadOwnership(control)
	if err != nil || after.Generation != before.Generation {
		t.Fatal("refused preparation changed ledger", err)
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "TEST user edit" {
		t.Fatal("edited sound not preserved", err)
	}
}
