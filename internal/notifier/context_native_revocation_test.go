//go:build linux || darwin

package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestContextNativeRevokedRequestBeforeFixtureRead(t *testing.T) {
	ops, _, stopped := contextNativeFixture(t)
	// The finite parent bounds a stuck fixture. Cancellation is triggered only
	// after request publication has reached the actual launch boundary.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	launch := ops.launch
	var directory string
	entered := false
	ops.launch = func(ctx context.Context, request, receipt string) error {
		entered = true
		directory = filepath.Dir(request)
		// Hold the synthetic native reader until the actual cancellation callback
		// has revoked its request. No native identity or effect has been published.
		cancel()
		<-ctx.Done()
		until := time.Now().Add(time.Second)
		for {
			if _, err := os.Stat(request); os.IsNotExist(err) {
				break
			}
			if time.Now().After(until) {
				t.Fatal("native request remained readable after cancellation")
			}
			time.Sleep(time.Millisecond)
		}
		err := launch(ctx, request, receipt)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("revoked native reader: %v", err)
		}
		for _, path := range []string{strings.TrimSuffix(request, ".request") + ".started", receipt} {
			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Fatalf("revoked native reader published %s: %v", filepath.Base(path), statErr)
			}
		}
		return err
	}
	err := runContextNativeApp(ctx, contextNativeClock{}, "/TEST-owned-helper", []string{"-title", "Pi"}, ops)
	if !entered || !errors.Is(err, context.Canceled) || *stopped != 0 {
		t.Fatalf("unstarted native app entered=%t cancellation=%v stopped=%d", entered, err, *stopped)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("private attempt not removed", err)
	}
}

func TestContextNativeFixtureKeepsActiveReadAndDecodeFailures(t *testing.T) {
	for _, mode := range []string{"active-missing", "active-malformed", "cancelled-malformed"} {
		t.Run(mode, func(t *testing.T) {
			ops, _, stopped := contextNativeFixture(t)
			directory := t.TempDir()
			request, receipt := filepath.Join(directory, "fixture.request"), filepath.Join(directory, "fixture.receipt")
			if mode != "active-missing" {
				if err := writePrivate(request, []byte("{")); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled-malformed" {
				cancel()
			}
			err := ops.launch(ctx, request, receipt)
			if mode == "active-missing" {
				if !os.IsNotExist(err) {
					t.Fatalf("active read error lost: %v", err)
				}
			} else {
				var syntax *json.SyntaxError
				if !errors.As(err, &syntax) {
					t.Fatalf("decode error lost: %v", err)
				}
			}
			if *stopped != 0 {
				t.Fatalf("invalid request stopped a process: %d", *stopped)
			}
			for _, path := range []string{filepath.Join(directory, "fixture.started"), receipt} {
				if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
					t.Fatalf("invalid request published %s: %v", filepath.Base(path), statErr)
				}
			}
		})
	}
}
