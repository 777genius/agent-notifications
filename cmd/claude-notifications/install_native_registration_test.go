package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
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
	for _, test := range []struct {
		name      string
		printPath bool
		warning   error
	}{
		{"normal warning", false, errors.New("registration unavailable")},
		{"path warning", true, errors.New("registration unavailable")},
		{"path success", true, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := reconcileRuntimeNativeRegistration
			defer func() { reconcileRuntimeNativeRegistration = before }()
			calls := 0
			reconcileRuntimeNativeRegistration = func(ctx context.Context, control string) error {
				calls++
				snapshot, err := installruntime.ReadInstalledSnapshot(control)
				if err != nil || snapshot.Recovery || snapshot.Ledger.Generation == 0 {
					t.Fatalf("maintenance ran before a successful verified commit: %+v %v", snapshot, err)
				}
				return test.warning
			}
			root := t.TempDir()
			stage, target, control := filepath.Join(root, "stage"), filepath.Join(root, "runtime", "bin"), filepath.Join(root, "control")
			if err := os.MkdirAll(stage, 0700); err != nil {
				t.Fatal(err)
			}
			entry := "claude-notifications-linux-amd64"
			if runtime.GOOS == "windows" {
				entry = "claude-notifications-windows-" + runtime.GOARCH + ".exe"
			}
			if err := os.WriteFile(filepath.Join(stage, entry), []byte("inert sender "+installruntime.WriterProtocolMarker), 0700); err != nil {
				t.Fatal(err)
			}
			warnings, warningOutput, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			beforeStderr := os.Stderr
			os.Stderr = warningOutput
			defer func() {
				os.Stderr = beforeStderr
				_ = warningOutput.Close()
				_ = warnings.Close()
			}()
			args := []string{"--stage", stage, "--target", target, "--entry", entry, "--control-root", control}
			if test.printPath {
				args = append(args, "--print-native-path")
			}
			var output bytes.Buffer
			err = installRuntime(args, &output)
			_ = warningOutput.Close()
			os.Stderr = beforeStderr
			stderr, readErr := io.ReadAll(warnings)
			if err != nil || readErr != nil || calls != 1 {
				t.Fatalf("successful commit skipped maintenance or failed: calls=%d err=%v read=%v", calls, err, readErr)
			}
			warningText := "runtime committed; native registration reconciliation incomplete: registration unavailable"
			if test.printPath {
				if output.String() != "\n" {
					t.Fatalf("path-only output polluted: %q", output.String())
				}
				if test.warning != nil && !strings.Contains(string(stderr), warningText) {
					t.Fatalf("path-only commit lost stderr warning: %q", stderr)
				}
				if test.warning == nil && len(stderr) != 0 {
					t.Fatalf("successful registration emitted stderr: %q", stderr)
				}
			} else if !strings.Contains(output.String(), warningText) || len(stderr) != 0 {
				t.Fatalf("normal install lost its warning contract: stdout=%q stderr=%q", output.String(), stderr)
			}
		})
	}
}

// A path-only no-op observes retained ownership without invoking maintenance.
func TestInstallRuntimeNoopRefreshSkipsRegistration(t *testing.T) {
	for _, state := range []string{"native", "no-native", "absent"} {
		t.Run(state, func(t *testing.T) {
			f := newRuntimePathFixture(t, state, true)
			before := reconcileRuntimeNativeRegistration
			defer func() { reconcileRuntimeNativeRegistration = before }()
			calls := 0
			reconcileRuntimeNativeRegistration = func(context.Context, string) error {
				calls++
				return nil
			}
			for _, printPath := range []bool{false, true} {
				args := append([]string(nil), f.args...)
				if printPath {
					args = append(args, "--print-native-path")
				}
				if err := installRuntime(args, &bytes.Buffer{}); err != nil {
					t.Fatal(err)
				}
			}
			if calls != 0 {
				t.Fatalf("read-only no-op refresh invoked registration maintenance %d times", calls)
			}
		})
	}
}
