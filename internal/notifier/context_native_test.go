//go:build linux || darwin

package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type contextNativeClock struct{}

func (contextNativeClock) Now() (string, float64, error) { return "TEST-boot", 100, nil }

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

func TestContextNativeClipsLongParentToSignedHelperHorizon(t *testing.T) {
	ops, request, stopped := contextNativeFixture(t)
	parent, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	launch := ops.launch
	ops.launch = func(ctx context.Context, request, receipt string) error {
		end, ok := ctx.Deadline()
		if !ok || time.Until(end) <= 14*time.Second || time.Until(end) > 15*time.Second {
			t.Fatal("native child escaped the signed helper's 15s horizon")
		}
		return launch(ctx, request, receipt)
	}
	if err := runContextNativeApp(parent, contextNativeClock{}, "/TEST-owned-helper", []string{"-title", "Pi"}, ops); err != nil {
		t.Fatal(err)
	}
	if request.NotAfter <= 114 || request.NotAfter > 115 || parent.Err() != nil || *stopped != 0 {
		t.Fatalf("native clipping changed parent ownership: request=%+v parent=%v stopped=%d", request, parent.Err(), *stopped)
	}
}

func TestContextNativeStillRequiresFiniteParentDeadline(t *testing.T) {
	ops, _, _ := contextNativeFixture(t)
	ops.probe = func(context.Context) ([]byte, error) { t.Fatal("probed unbounded caller"); return nil, nil }
	if err := runContextNativeApp(context.Background(), contextNativeClock{}, "/TEST-owned-helper", nil, ops); err == nil {
		t.Fatal("unbounded caller gained an implicit native deadline")
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
