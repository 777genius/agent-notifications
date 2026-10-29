//go:build linux || darwin

package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

type contextNativeClock struct{}

func (contextNativeClock) Now() (string, float64, error) { return "TEST-boot", 100, nil }

func contextNativeDiagnosticRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "TEST-pi-installed-macos-reader", "case-completion", "tmp")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", root)
	return filepath.Join(root, "native-context-reader.json")
}

func TestContextNativeOriginalReaderDiagnosticDefaultOff(t *testing.T) {
	path := contextNativeDiagnosticRoot(t)
	t.Setenv("AGENT_NOTIFICATIONS_TEST_NATIVE_READER", "")
	ops, _, stopped := contextNativeFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := runContextNativeApp(ctx, contextNativeClock{}, "/TEST-owned-helper", []string{"-title", "Pi", "-message", "TEST private"}, ops); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) || *stopped != 0 {
		t.Fatal("disabled observation changed output or ownership", err, *stopped)
	}
}

func TestContextNativeOriginalReaderDiagnosticPreservesActualResults(t *testing.T) {
	for _, mode := range []string{"submitted", "rejected", "launch_error", "joined_signal"} {
		t.Run(mode, func(t *testing.T) {
			path := contextNativeDiagnosticRoot(t)
			t.Setenv("AGENT_NOTIFICATIONS_TEST_NATIVE_READER", "1")
			ops, request, stopped := contextNativeFixture(t)
			launch := ops.launch
			var directory string
			var launchFailure error
			launchError := mode == "launch_error" || mode == "joined_signal"
			if mode == "launch_error" {
				launchFailure = exec.Command("/bin/sh", "-c", "exit 7").Run()
			} else if mode == "joined_signal" {
				launchFailure = errors.Join(exec.Command("/bin/sh", "-c", "kill -TERM $$").Run(), context.DeadlineExceeded, context.Canceled)
			}
			ops.launch = func(ctx context.Context, input, receipt string) error {
				directory = filepath.Dir(input)
				if err := launch(ctx, input, receipt); err != nil {
					return err
				}
				if mode == "submitted" {
					return nil
				}
				if err := os.Remove(receipt); err != nil {
					t.Fatal(err)
				}
				if launchError {
					return launchFailure
				}
				body, _ := json.Marshal(map[string]any{"schemaVersion": 1, "correlationID": request.CorrelationID,
					"nonce": request.Nonce, "notificationID": request.CorrelationID, "status": "rejected", "reason": "permission_denied", "retrySafe": false})
				return writePrivate(receipt, body)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			returned := runContextNativeApp(ctx, contextNativeClock{}, "/TEST-owned-helper", []string{"-title", "TEST-private-payload", "-message", "TEST-private-body"}, ops)
			if mode == "submitted" && returned != nil || mode == "rejected" && returned != ErrDesktopSuppressed || launchError && !errors.Is(returned, launchFailure) {
				t.Fatalf("original result changed: %s: %v", mode, returned)
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatal("original transport cleanup changed", err)
			}
			raw, err := readPrivate(path, 1024)
			if err != nil {
				t.Fatal(err)
			}
			for _, private := range []string{"TEST-private", request.CorrelationID, request.Nonce, request.BootID, "/TEST-owned-helper"} {
				if strings.Contains(string(raw), private) {
					t.Fatal("private native data escaped the closed diagnostic")
				}
			}
			var actual contextNativeDiagnostic
			if err := json.Unmarshal(raw, &actual); err != nil || !actual.DiagnosticOnly || actual.QualificationInput || !actual.LaunchCompleted {
				t.Fatal("invalid diagnostic", err, string(raw))
			}
			if launchError {
				if actual.Stage != "receipt_wait" || actual.LaunchErrorClass != "process_exit" || actual.LaunchExitCode == nil || *stopped != 1 {
					t.Fatal("original launch failure or cleanup lost", string(raw), *stopped)
				}
				if mode == "joined_signal" {
					if !actual.DeadlineInError || !actual.CancelledInError || actual.LaunchSignaled == nil || !*actual.LaunchSignaled || actual.LaunchSignal == nil || *actual.LaunchSignal != int(syscall.SIGTERM) || *actual.LaunchExitCode != -1 {
						t.Fatal("actual joined deadline/cancellation/signal facts lost", string(raw))
					}
				} else if actual.DeadlineInError || actual.CancelledInError || actual.LaunchSignaled == nil || *actual.LaunchSignaled || actual.LaunchSignal != nil || *actual.LaunchExitCode != 7 {
					t.Fatal("numeric exit misclassified as signal/cancellation", string(raw))
				}
			} else if actual.Stage != "native_receipt" || actual.ReceiptStatus != mode || actual.LaunchErrorClass != "none" || *stopped != 0 {
				t.Fatal("actual normal receipt changed", string(raw), *stopped)
			}
		})
	}
}

func TestContextNativeOriginalReaderDiagnosticRefusesUnsafeOrExistingSink(t *testing.T) {
	for _, mode := range []string{"existing", "symlink", "nonprivate", "outside"} {
		t.Run(mode, func(t *testing.T) {
			path := contextNativeDiagnosticRoot(t)
			t.Setenv("AGENT_NOTIFICATIONS_TEST_NATIVE_READER", "1")
			const sentinel = "TEST original file must remain unchanged"
			var protected string
			switch mode {
			case "existing":
				protected = path
				if err := os.WriteFile(path, []byte(sentinel), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				protected = filepath.Join(t.TempDir(), "foreign")
				if err := os.WriteFile(protected, []byte(sentinel), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(protected, path); err != nil {
					t.Fatal(err)
				}
			case "nonprivate":
				if err := os.Chmod(filepath.Dir(filepath.Dir(path)), 0755); err != nil {
					t.Fatal(err)
				}
			case "outside":
				t.Setenv("TMPDIR", t.TempDir())
			}
			ops, _, _ := contextNativeFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := runContextNativeApp(ctx, contextNativeClock{}, "/TEST-owned-helper", nil, ops); err != nil {
				t.Fatal("refused diagnostic changed original operation", err)
			}
			if protected != "" {
				if raw, err := os.ReadFile(protected); err != nil || string(raw) != sentinel {
					t.Fatal("diagnostic modified an existing/foreign inode", err)
				}
			} else if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("unsafe diagnostic sink created", err)
			}
		})
	}
}

func contextNativeFixture(t *testing.T) (contextNativeOperations, *contextNativeRequest, *int) {
	t.Helper()
	var captured contextNativeRequest
	stopped := 0
	process := contextNativeProcess{PID: 456, StartSeconds: 30, StartMicroseconds: 40}
	ops := contextNativeOperations{
		owner: func() (contextNativeProcess, error) { return contextNativeProcess{123, 10, 20}, nil },
		probe: func(context.Context) ([]byte, error) {
			return []byte(`{"schemaVersion":1,"contextNotificationVersions":[1]}`), nil
		},
		stop: func(got contextNativeProcess, executable string) error {
			stopped++
			if got != process || executable != "/TEST-owned-helper" {
				t.Fatalf("foreign process selected: %+v %q", got, executable)
			}
			return nil
		},
	}
	ops.launch = func(ctx context.Context, request, receipt string) error {
		data, err := readPrivate(request, 16384)
		if err != nil || json.Unmarshal(data, &captured) != nil {
			t.Fatal("request unavailable", err)
		}
		started, _ := json.Marshal(contextNativeStarted{1, captured.CorrelationID, captured.Nonce, process})
		if err := writePrivate(strings.TrimSuffix(request, ".request")+".started", started); err != nil {
			t.Fatal(err)
		}
		if err := writePrivate(strings.TrimSuffix(request, ".request")+".claim", []byte{}); err != nil {
			t.Fatal(err)
		}
		response, _ := json.Marshal(map[string]any{"schemaVersion": 1, "correlationID": captured.CorrelationID, "nonce": captured.Nonce, "notificationID": captured.CorrelationID, "status": "submitted", "reason": "os_accepted", "retrySafe": false})
		return writePrivate(receipt, response)
	}
	return ops, &captured, &stopped
}

func TestContextNativePreservesPresentationAndOriginalDeadline(t *testing.T) {
	ops, request, stopped := contextNativeFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	args := []string{"-title", "Pi", "-message", "Pi completed a task", "-execute", "'/TEST/runtime' focus-window 'com.apple.Terminal' '/TEST/project'", "-soundFile", "/TEST/cue.mp3", "-soundVolume", "0.25", "-nosound"}
	if err := runContextNativeApp(ctx, contextNativeClock{}, "/TEST-owned-helper", args, ops); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request.Arguments, args) || request.NotAfter <= 100 || request.NotAfter > 102 || request.Owner != (contextNativeProcess{123, 10, 20}) || request.BootID != "TEST-boot" || *stopped != 0 {
		t.Fatalf("changed context request: %+v, stopped=%d", request, *stopped)
	}
}

func TestContextNativeCancellationRevokesBeforeStoppingExactApp(t *testing.T) {
	ops, _, stopped := contextNativeFixture(t)
	launch := ops.launch
	var directory string
	ops.launch = func(ctx context.Context, request, receipt string) error {
		directory = filepath.Dir(request)
		if err := launch(ctx, request, receipt); err != nil {
			return err
		}
		if err := os.Remove(receipt); err != nil {
			t.Fatal(err)
		}
		<-ctx.Done()
		// Joining open must not leave its independently launched app admitted.
		// Hold that join until the cancellation lease has actually disappeared.
		until := time.Now().Add(time.Second)
		for {
			_, requestErr := os.Stat(request)
			_, claimErr := os.Stat(strings.TrimSuffix(request, ".request") + ".claim")
			if os.IsNotExist(requestErr) && os.IsNotExist(claimErr) {
				break
			}
			if time.Now().After(until) {
				t.Fatal("native effect lease stayed live while launcher join was pending")
			}
			time.Sleep(time.Millisecond)
		}
		return ctx.Err()
	}
	stop := ops.stop
	ops.stop = func(process contextNativeProcess, executable string) error {
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".request") || strings.HasSuffix(entry.Name(), ".claim") {
				t.Fatal("effect lease still live when signalling native app")
			}
		}
		return stop(process, executable)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := runContextNativeApp(ctx, contextNativeClock{}, "/TEST-owned-helper", []string{"-title", "Pi", "-message", "fixed"}, ops); !errors.Is(err, context.DeadlineExceeded) || *stopped != 1 {
		t.Fatalf("cancel=%v stopped=%d", err, *stopped)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("private attempt not removed", err)
	}
}

func TestContextNativeCorrelatedReceiptReleasesAcceptedCallbackWork(t *testing.T) {
	ops, _, stopped := contextNativeFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	launch := ops.launch
	ops.launch = func(ctx context.Context, request, receipt string) error {
		if err := launch(ctx, request, receipt); err != nil {
			return err
		}
		// Submission has already completed. An exiting/cancelled open process
		// cannot retract it or kill user click work owned by the native app.
		cancel()
		return ctx.Err()
	}
	if err := runContextNativeApp(ctx, contextNativeClock{}, "/TEST-owned-helper", []string{"-title", "Pi", "-message", "fixed"}, ops); err != nil || *stopped != 0 {
		t.Fatalf("accepted callback was killed: err=%v stops=%d", err, *stopped)
	}
}

func TestContextNativeWaitsForExclusiveReceiptPublication(t *testing.T) {
	ops, _, stopped := contextNativeFixture(t)
	launch := ops.launch
	publication := make(chan error, 1)
	ops.launch = func(ctx context.Context, request, receipt string) error {
		if err := launch(ctx, request, receipt); err != nil {
			return err
		}
		temporary := receipt + ".tmp"
		if err := os.Link(receipt, temporary); err != nil {
			return err
		}
		go func() {
			time.Sleep(30 * time.Millisecond)
			publication <- os.Remove(temporary)
		}()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runContextNativeApp(ctx, contextNativeClock{}, "/TEST-owned-helper", []string{"-title", "Pi", "-message", "fixed"}, ops); err != nil || *stopped != 0 {
		t.Fatalf("in-progress publication rejected: %v stops=%d", err, *stopped)
	}
	if err := <-publication; err != nil {
		t.Fatal(err)
	}
}

func TestContextNativeOldHelperAndMalformedIdentityCannotLaunchOrSignal(t *testing.T) {
	for _, mode := range []string{"old-helper", "malformed-started"} {
		t.Run(mode, func(t *testing.T) {
			ops, _, stopped := contextNativeFixture(t)
			if mode == "old-helper" {
				ops.probe = func(context.Context) ([]byte, error) { return []byte(`{"protocolVersions":[1]}`), nil }
				ops.launch = func(context.Context, string, string) error { t.Fatal("launched unsupported helper"); return nil }
			} else {
				ops.launch = func(_ context.Context, request, _ string) error {
					if err := writePrivate(strings.TrimSuffix(request, ".request")+".started", []byte(`{"schemaVersion":1,"process":{"pid":456}}`)); err != nil {
						t.Fatal(err)
					}
					return errors.New("launcher failed after possible handoff")
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := runContextNativeApp(ctx, contextNativeClock{}, "/TEST-owned-helper", []string{"-title", "Pi", "-message", "fixed"}, ops); err == nil || *stopped != 0 {
				t.Fatalf("invalid native state=%v stopped=%d", err, *stopped)
			}
		})
	}
}
