package notifier

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const notifierTimeoutHelperMode = "AGENT_NOTIFICATIONS_TEST_TIMEOUT_MODE"
const notifierTimeoutHelperPID = "AGENT_NOTIFICATIONS_TEST_TIMEOUT_PID"

func TestRunCommandWithTimeoutStopsBlockedProcess(t *testing.T) {
	// A test executable can take longer than 100ms to start on Windows. Allow
	// startup time, then check its output so an unstarted process cannot pass.
	executable := prepareNotifierTimeoutHelper(t, "block")
	started := time.Now()
	output, err := runCommandWithTimeout(3*time.Second, executable, "-test.run=^TestNotifierTimeoutHelper$")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked helper returned %v, want deadline exceeded", err)
	}
	if !strings.Contains(string(output), "helper started") {
		t.Fatalf("blocked helper did not start before cancellation: %q", output)
	}
	if elapsed := time.Since(started); elapsed > 8*time.Second {
		t.Fatalf("blocked helper took %s to stop after timeout", elapsed)
	}
}

func TestRunCommandWithTimeoutBoundsInheritedPipes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    string
		timeout time.Duration
		wantErr error
	}{
		{"blocked parent", "child-and-block", 3 * time.Second, context.DeadlineExceeded},
		{"exited parent", "child-and-exit", 15 * time.Second, exec.ErrWaitDelay},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executable := prepareNotifierTimeoutHelper(t, tc.mode)
			started := time.Now()
			output, err := runCommandWithTimeout(tc.timeout, executable, "-test.run=^TestNotifierTimeoutHelper$")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("inherited-pipe helper returned %v, want %v (output %q)", err, tc.wantErr, output)
			}
			if !strings.Contains(string(output), "child started") {
				t.Fatalf("descendant did not inherit the output pipe: %q", output)
			}
			if elapsed := time.Since(started); elapsed > 8*time.Second {
				t.Fatalf("inherited output pipes held up completion for %s", elapsed)
			}
		})
	}
}

func TestRunCommandWithTimeoutPreservesOutputAndExitStatus(t *testing.T) {
	for _, mode := range []string{"success", "failure"} {
		t.Run(mode, func(t *testing.T) {
			executable := prepareNotifierTimeoutHelper(t, mode)
			output, err := runCommandWithTimeout(15*time.Second, executable, "-test.run=^TestNotifierTimeoutHelper$")
			if !strings.Contains(string(output), "helper stdout") || !strings.Contains(string(output), "helper stderr") {
				t.Fatalf("missing combined output: %q", output)
			}
			if mode == "success" {
				if err != nil {
					t.Fatalf("successful command returned %v", err)
				}
			} else {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
					t.Fatalf("failed command returned %v, want exit code 7", err)
				}
			}
		})
	}
}

func prepareNotifierTimeoutHelper(t *testing.T, mode string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv(notifierTimeoutHelperMode, mode)
	t.Setenv(notifierTimeoutHelperPID, pidPath)
	// CommandContext kills the direct process, not this pipe-holding child.
	t.Cleanup(func() { killRecordedProcess(pidPath) })
	return executable
}

// TestNotifierTimeoutHelper is an inert, portable subprocess fixture. A nested
// copy can retain both output pipes after the direct process is gone.
func TestNotifierTimeoutHelper(t *testing.T) {
	mode := os.Getenv(notifierTimeoutHelperMode)
	if mode == "" {
		return
	}
	switch mode {
	case "success", "failure":
		fmt.Fprintln(os.Stdout, "helper stdout")
		fmt.Fprintln(os.Stderr, "helper stderr")
		if mode == "failure" {
			os.Exit(7)
		}
		os.Exit(0)
	case "block":
		fmt.Fprintln(os.Stdout, "helper started")
	case "hold-pipes":
		if err := os.WriteFile(os.Getenv(notifierTimeoutHelperPID), []byte(fmt.Sprint(os.Getpid())), 0600); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(os.Stdout, "child started")
	case "child-and-block", "child-and-exit":
		child := exec.Command(os.Args[0], "-test.run=^TestNotifierTimeoutHelper$")
		child.Env = append(os.Environ(), notifierTimeoutHelperMode+"=hold-pipes")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		// Wait until the child records its PID before exiting, so the outer test
		// always knows which process to clean up even on a slow host.
		for {
			if _, err := os.Stat(os.Getenv(notifierTimeoutHelperPID)); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if mode == "child-and-exit" {
			os.Exit(0)
		}
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
	// Finite as a last-resort cleanup if the parent test itself is interrupted.
	time.Sleep(30 * time.Second)
	os.Exit(0)
}
