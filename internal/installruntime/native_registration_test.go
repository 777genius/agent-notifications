package installruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func registrationFixture(t *testing.T, count int) (Request, []string) {
	t.Helper()
	ctx, r := request(t)
	var err error
	r.RuntimeRoot, err = CanonicalPath(r.RuntimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for i := 0; i < count; i++ {
		change, err := StageNative(ctx, r.ControlRoot, nativeFixture(t))
		if err != nil {
			t.Fatal(err)
		}
		r.Native = change
		if _, err := Commit(ctx, r); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, change.After.Path)
	}
	return r, paths
}

func registrationBundle(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, "Contents"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "Contents", "Info.plist"), []byte("fixture bytes remain intact"), 0600); err != nil {
		t.Fatal(err)
	}
}

func registrationSpy(t *testing.T, calls *[][]string, ids map[string]string) nativeRegistrationRunner {
	t.Helper()
	return func(ctx context.Context, executable string, args ...string) ([]byte, error) {
		t.Helper()
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 10*time.Second {
			t.Fatal("registration subprocess lacks bounded deadline")
		}
		switch executable {
		case "/usr/libexec/PlistBuddy":
			if len(args) != 3 || args[0] != "-c" || args[1] != "Print :CFBundleIdentifier" {
				t.Fatalf("identity reader is not fixed/read-only: %v", args)
			}
			bundle := filepath.Dir(filepath.Dir(args[2]))
			id, ok := ids[bundle]
			if !ok {
				t.Fatalf("unexpected bundle identity read: %s", bundle)
			}
			return []byte(id + "\n"), nil
		case "/usr/bin/osascript":
			if !reflect.DeepEqual(args, []string{"-l", "JavaScript", "-e", preferredNativeRegistrationQuery}) {
				t.Fatalf("preferred registration query is not fixed/read-only: %v", args)
			}
			for bundle := range ids {
				if strings.HasPrefix(filepath.Base(bundle), "generation-") {
					return []byte(bundle), nil
				}
			}
			t.Fatal("query fixture has no active generation")
			return nil, nil
		case launchServicesRegister:
			*calls = append(*calls, append([]string{executable}, args...))
			return nil, nil
		default:
			t.Fatalf("unexpected subprocess: %s %v", executable, args)
			return nil, nil
		}
	}
}

func TestNativeRegistrationUnregistersOnlyExactKnownProductPaths(t *testing.T) {
	r, generations := registrationFixture(t, 1)
	active := generations[0]
	obsolete := filepath.Join(r.RuntimeRoot, "bin", "ClaudeNotifier.app")
	foreignID := filepath.Join(r.RuntimeRoot, "bin", "terminal-notifier.app")
	foreignTarget := filepath.Join(t.TempDir(), "ClaudeNotifier.app")
	unknown := filepath.Join(r.RuntimeRoot, "other", "ClaudeNotifier.app")
	for _, path := range []string{obsolete, foreignID, foreignTarget, unknown} {
		registrationBundle(t, path)
	}
	outsideAlias := filepath.Join(r.RuntimeRoot, "swift-notifier", "ClaudeNotifier.app")
	if err := os.MkdirAll(filepath.Dir(outsideAlias), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreignTarget, outsideAlias); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(obsolete)
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	run := registrationSpy(t, &calls, map[string]string{active: nativeProductBundleID, obsolete: nativeProductBundleID, foreignID: "other.product"})
	if err := reconcileNativeRegistration(context.Background(), r.ControlRoot, run); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{launchServicesRegister, "-u", obsolete}, {launchServicesRegister, "-f", active}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("registration touched unowned app or wrong commands: %v", calls)
	}
	after, err := os.Stat(obsolete)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("obsolete callback inode changed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(obsolete, "Contents", "Info.plist"))
	if err != nil || string(data) != "fixture bytes remain intact" {
		t.Fatalf("obsolete callback bytes changed: %q %v", data, err)
	}
}

func TestNativeRegistrationProtectsActivePreviousAndPublishedGenerations(t *testing.T) {
	r, generations := registrationFixture(t, 3)
	for i, relative := range []string{"bin/ClaudeNotifier.app", "bin/terminal-notifier.app", "swift-notifier/ClaudeNotifier.app"} {
		path := filepath.Join(r.RuntimeRoot, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(generations[i], path); err != nil {
			t.Fatal(err)
		}
	}
	active := generations[2]
	var calls [][]string
	if err := reconcileNativeRegistration(context.Background(), r.ControlRoot, registrationSpy(t, &calls, map[string]string{active: nativeProductBundleID})); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, [][]string{{launchServicesRegister, "-f", active}}) {
		t.Fatalf("durable callback identity unregistered: %v", calls)
	}
}

func TestNativeRegistrationRefusesUnverifiedSnapshotOrWrongActiveIdentity(t *testing.T) {
	for _, broken := range []string{"bytes", "identity", "recovery"} {
		t.Run(broken, func(t *testing.T) {
			r, generations := registrationFixture(t, 1)
			active := generations[0]
			id := nativeProductBundleID
			switch broken {
			case "bytes":
				if err := os.WriteFile(filepath.Join(active, "unexpected"), []byte("tampered"), 0600); err != nil {
					t.Fatal(err)
				}
			case "identity":
				id = "other.product"
			case "recovery":
				if err := os.WriteFile(filepath.Join(r.ControlRoot, "transaction.json"), []byte("interrupted"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var calls [][]string
			err := reconcileNativeRegistration(context.Background(), r.ControlRoot, registrationSpy(t, &calls, map[string]string{active: id}))
			if err == nil || len(calls) != 0 {
				t.Fatalf("unsafe snapshot produced registration effects: %v %v", calls, err)
			}
		})
	}
}

func TestNativeRegistrationStillRegistersActiveAfterCleanupFailure(t *testing.T) {
	r, generations := registrationFixture(t, 1)
	active := generations[0]
	obsolete := filepath.Join(r.RuntimeRoot, "bin", "ClaudeNotifier.app")
	registrationBundle(t, obsolete)
	var calls [][]string
	spy := registrationSpy(t, &calls, map[string]string{active: nativeProductBundleID, obsolete: nativeProductBundleID})
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		out, err := spy(ctx, name, args...)
		if name == launchServicesRegister && args[0] == "-u" {
			return nil, errors.New("cleanup rejected")
		}
		return out, err
	}
	err := reconcileNativeRegistration(context.Background(), r.ControlRoot, run)
	if err == nil || !strings.Contains(err.Error(), "cleanup rejected") {
		t.Fatalf("cleanup failure hidden: %v", err)
	}
	if !reflect.DeepEqual(calls, [][]string{{launchServicesRegister, "-u", obsolete}, {launchServicesRegister, "-f", active}}) {
		t.Fatalf("active registration suppressed by cleanup failure: %v", calls)
	}
}

func TestNativeRegistrationReportsPreferredUnmanagedReaderWithoutUnregisteringIt(t *testing.T) {
	r, generations := registrationFixture(t, 1)
	active := generations[0]
	unknown := filepath.Join(t.TempDir(), "ClaudeNotifier.app")
	registrationBundle(t, unknown)
	var calls [][]string
	spy := registrationSpy(t, &calls, map[string]string{active: nativeProductBundleID})
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "/usr/bin/osascript" {
			return []byte(unknown), nil
		}
		return spy(ctx, name, args...)
	}
	err := reconcileNativeRegistration(context.Background(), r.ControlRoot, run)
	if err == nil || !strings.Contains(err.Error(), unknown) || !strings.Contains(err.Error(), "explicitly reconcile") {
		t.Fatalf("preferred stale helper hidden: %v", err)
	}
	if !reflect.DeepEqual(calls, [][]string{{launchServicesRegister, "-f", active}}) {
		t.Fatalf("unmanaged development copy was modified: %v", calls)
	}
}

func TestNativeRegistrationWarnsWhenRetainedPreviousReaderIsPreferred(t *testing.T) {
	r, generations := registrationFixture(t, 2)
	var calls [][]string
	spy := registrationSpy(t, &calls, map[string]string{generations[1]: nativeProductBundleID})
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "/usr/bin/osascript" {
			return []byte(generations[0]), nil
		}
		return spy(ctx, name, args...)
	}
	err := reconcileNativeRegistration(context.Background(), r.ControlRoot, run)
	if err == nil || !strings.Contains(err.Error(), generations[0]) {
		t.Fatalf("old published reader incorrectly considered active: %v", err)
	}
	if !reflect.DeepEqual(calls, [][]string{{launchServicesRegister, "-f", generations[1]}}) {
		t.Fatalf("previous published reader was unregistered: %v", calls)
	}
}
