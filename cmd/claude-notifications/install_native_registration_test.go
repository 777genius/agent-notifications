package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func init() {
	// Every adapter test uses an injected maintenance runner, including fixtures
	// with the real product bundle ID. Never mutate the user's LaunchServices.
	reconcileRuntimeNativeRegistration = func(context.Context, string) error { return nil }
}

func TestInstallRuntimeReportsRegistrationFailureAfterSuccessfulCommit(t *testing.T) {
	before := reconcileRuntimeNativeRegistration
	defer func() { reconcileRuntimeNativeRegistration = before }()
	called := false
	reconcileRuntimeNativeRegistration = func(ctx context.Context, control string) error {
		called = true
		snapshot, err := installruntime.ReadInstalledSnapshot(control)
		if err != nil || snapshot.Recovery || snapshot.Ledger.Generation == 0 {
			t.Fatalf("maintenance ran before a successful verified commit: %+v %v", snapshot, err)
		}
		return errors.New("registration unavailable")
	}
	root := t.TempDir()
	stage, target, control := filepath.Join(root, "stage"), filepath.Join(root, "runtime", "bin"), filepath.Join(root, "control")
	if err := os.MkdirAll(stage, 0700); err != nil {
		t.Fatal(err)
	}
	entry := "claude-notifications-linux-amd64"
	if err := os.WriteFile(filepath.Join(stage, entry), []byte("inert sender "+installruntime.WriterProtocolMarker), 0700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := installRuntime([]string{"--stage", stage, "--target", target, "--entry", entry, "--control-root", control}, &output)
	if err != nil || !called || !strings.Contains(output.String(), "runtime committed; native registration reconciliation incomplete: registration unavailable") {
		t.Fatalf("successful install misreported as failure: %v %s", err, output.String())
	}
}
