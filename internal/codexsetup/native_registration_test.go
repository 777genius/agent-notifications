package codexsetup

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func init() {
	// Setup fixtures must never register product bundles with real LaunchServices.
	reconcileNativeRegistration = func(context.Context, string) error { return nil }
}

func TestRunReportsNativeRegistrationFailureWithoutFailingCommittedSetup(t *testing.T) {
	before := reconcileNativeRegistration
	defer func() { reconcileNativeRegistration = before }()
	called := false
	reconcileNativeRegistration = func(ctx context.Context, control string) error {
		called = true
		snapshot, err := installruntime.ReadInstalledSnapshot(control)
		if err != nil || snapshot.Recovery || snapshot.Ledger.Generation == 0 {
			t.Fatalf("maintenance ran before verified commit: %+v %v", snapshot, err)
		}
		return errors.New("registration unavailable")
	}
	source, home := fakeBundle(t), t.TempDir()
	opts := Options{ControlRoot: filepath.Join(home, "control"), CodexHome: home, PluginRoot: source}
	result, err := Run(opts)
	if err != nil || !called || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "runtime committed; native registration reconciliation incomplete") {
		t.Fatalf("successful setup misreported: %+v %v", result, err)
	}
	called = false
	opts.Remove = true
	if _, err := Run(opts); err != nil || called {
		t.Fatalf("removal attempted native registration: %v called=%v", err, called)
	}
}
