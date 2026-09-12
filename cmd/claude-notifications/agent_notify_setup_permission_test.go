//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notifier"
)

func TestSetupNotificationsPermissionProductionBinding(t *testing.T) {
	if reflect.ValueOf((agentNotifySetupComposition{}).permissionOperation()).Pointer() != reflect.ValueOf(notifier.SetupPermission).Pointer() {
		t.Fatal("production must bind real native core")
	}
}

func TestSetupNotificationsPermissionStatesAndPreservation(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, op := range []string{"permission-status", "request-permission"} {
			for _, state := range []string{"allowed", "denied", "undetermined"} {
				t.Run(op+"/"+state+"/"+map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
					f := newSetupCommandFixture(t)
					if enabled {
						setupCommandRun(t, setupCommandContext(t), f.args(t, "enable", f.route()...), f.composition, 0)
					}
					expected, e := installruntime.ReadInstalledSnapshot(f.control)
					if e != nil {
						t.Fatal(e)
					}
					if expected.Enabled != enabled {
						t.Fatal("bad fixture")
					}
					// No client registration has been performed.
					before := setupCommandTree(t, f.root)
					calls := 0
					f.composition.permission = func(ctx context.Context, root string, s installruntime.InstalledSnapshot, request bool) (string, error) {
						calls++
						if root != f.control || !reflect.DeepEqual(s, expected) || request != (op == "request-permission") {
							t.Fatal("incorrect native arguments")
						}
						deadline, ok := ctx.Deadline()
						if !ok || time.Until(deadline) > 3*time.Minute || ctx.Err() != nil {
							t.Fatal("unbounded context")
						}
						return state, nil
					}
					r := setupCommandRun(t, context.Background(), f.args(t, op), f.composition, 0)
					if calls != 1 || r.Permission != state || r.Reason != "permission_"+state || r.ExplicitIntent != enabled || r.RuntimeEligible != enabled || r.Activation != "not_verified" {
						t.Fatalf("calls=%d result=%+v", calls, r)
					}
					if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
						t.Fatal("permission changed ledger, policy, journal or fixture")
					}
				})
			}
		}
	}
}

func TestSetupNotificationsPermissionRejectBeforeCall(t *testing.T) {
	for _, op := range []string{"permission-status", "request-permission"} {
		for _, mode := range []string{"nil", "canceled", "malformed", "missing", "invalid-native", "tampered-native", "tampered-runtime", "runtime-conflict", "stale", "no-generation"} {
			if op == "permission-status" && (mode == "stale" || mode == "no-generation") {
				continue
			}
			t.Run(op+"/"+mode, func(t *testing.T) {
				f := newSetupCommandFixture(t)
				args := f.args(t, op)
				ctx := context.Background()
				code := 1
				switch mode {
				case "nil":
					ctx = nil
				case "canceled":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				case "malformed":
					args = append(args, "--global-config", f.global)
					code = 2
				case "missing":
					args[2] = filepath.Join(f.root, "missing")
				case "runtime-conflict":
					args = append(args, "--runtime-root", f.root)
				case "stale":
					args[len(args)-1] = "999"
				case "no-generation":
					args = args[:4]
					code = 2
				case "tampered-runtime":
					setupCommandWrite(t, f.command, "tampered inert runtime", 0700)
				case "tampered-native":
					s, e := installruntime.ReadInstalledSnapshot(f.control)
					if e != nil {
						t.Fatal(e)
					}
					setupCommandWrite(t, filepath.Join(s.Ledger.Native.Path, "Contents", "MacOS", "terminal-notifier-modern"), "tampered inert native", 0700)
				case "invalid-native":
					s, e := installruntime.ReadInstalledSnapshot(f.control)
					if e != nil {
						t.Fatal(e)
					}
					if e = os.Rename(s.Ledger.Native.Path, s.Ledger.Native.Path+"-missing"); e != nil {
						t.Fatal(e)
					}
				}
				before := setupCommandTree(t, f.root)
				r := setupCommandRun(t, ctx, args, f.composition, code)
				if r.Permission != "unavailable" {
					t.Fatal(r)
				}
				if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
					t.Fatal("rejection changed state")
				}
			})
		}
	}
}

func TestSetupNotificationsPermissionUnavailableSanitized(t *testing.T) {
	for _, op := range []string{"permission-status", "request-permission"} {
		for _, mode := range []string{"error", "unavailable", "unknown", "empty", "canceled"} {
			for _, jsonOutput := range []bool{false, true} {
				t.Run(op+"/"+mode+"/"+map[bool]string{false: "text", true: "json"}[jsonOutput], func(t *testing.T) {
					f := newSetupCommandFixture(t)
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					calls := 0
					f.composition.permission = func(context.Context, string, installruntime.InstalledSnapshot, bool) (string, error) {
						calls++
						switch mode {
						case "error":
							return "allowed", errors.New("SECRET\n/path/private")
						case "unknown":
							return "SECRET\n/path/private", nil
						case "empty":
							return "", nil
						case "canceled":
							cancel()
							return "allowed", nil
						}
						return "unavailable", nil
					}
					args := f.args(t, op)
					if !jsonOutput {
						args = append(args[:3], args[4:]...)
					}
					before := setupCommandTree(t, f.root)
					var out bytes.Buffer
					if code := agentNotifySetupExecute(ctx, args, &out, f.composition); code != 1 {
						t.Fatalf("code %d: %s", code, &out)
					}
					text := out.String()
					if calls != 1 || !strings.Contains(text, "unavailable") {
						t.Fatalf("%d %s", calls, text)
					}
					for _, bad := range []string{"SECRET", "/path/private", "retry", "allowed", "explicitIntent", "activation"} {
						if strings.Contains(text, bad) {
							t.Fatalf("unsafe output: %s", text)
						}
					}
					if !reflect.DeepEqual(before, setupCommandTree(t, f.root)) {
						t.Fatal("failure changed state")
					}
				})
			}
		}
	}
}

func TestSetupNotificationsPermissionTextHelpAndOutput(t *testing.T) {
	f := newSetupCommandFixture(t)
	f.composition.permission = func(context.Context, string, installruntime.InstalledSnapshot, bool) (string, error) {
		return "denied", nil
	}
	args := []string{"permission-status", "--control-root", f.control}
	var out bytes.Buffer
	if agentNotifySetupExecute(context.Background(), args, &out, f.composition) != 0 || !strings.Contains(out.String(), "System Settings > Notifications") {
		t.Fatal(out.String())
	}
	if agentNotifySetupExecute(context.Background(), args, permissionBrokenWriter{}, f.composition) != 1 {
		t.Fatal("output error ignored")
	}
	for _, op := range []string{"permission-status", "request-permission"} {
		out.Reset()
		if agentNotifySetupExecute(nil, []string{op, "--help"}, &out, f.composition) != 0 {
			t.Fatal("help failed")
		}
		for _, want := range []string{"3 minutes", "130", "does not prove", "--expected-generation"} {
			if !strings.Contains(out.String(), want) {
				t.Fatal(want)
			}
		}
	}
	for _, args := range [][]string{
		{"permission-status", "--expected-generation", "1"},
		{"request-permission", "--expected-generation", "1", "--expected-generation", "1"},
		{"request-permission", "--expected-generation", "-1"},
		{"permission-status", "--app", "/A.app"},
	} {
		if _, _, err := parseAgentNotifySetup(args); err == nil {
			t.Fatalf("accepted %q", args)
		}
	}
}

type permissionBrokenWriter struct{}

func (permissionBrokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
