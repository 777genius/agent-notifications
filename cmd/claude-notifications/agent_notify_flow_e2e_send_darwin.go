//go:build darwin && cgo

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
)

func sendQueuedNativeFromActive(t *testing.T, ctx context.Context, control, spool, bundle string) {
	t.Helper()
	if os.Getenv("AGENT_NOTIFY_DARWIN_E2E") != "1" {
		t.Log("skipping Notification Center send; set AGENT_NOTIFY_DARWIN_E2E=1 for operator-visible Darwin qualification")
		return
	}
	if err := os.MkdirAll(spool, 0700); err != nil {
		t.Fatal(err)
	}
	physical, err := filepath.EvalSymlinks(spool)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil || !snapshot.Enabled || snapshot.Ledger.Native == nil {
		t.Fatalf("managed native not enabled for send: %+v %v", snapshot, err)
	}
	if snapshot.Ledger.Native.Path != bundle {
		t.Fatalf("send target %s is not generation A %s", snapshot.Ledger.Native.Path, bundle)
	}
	clock := notifier.SystemBootClock{}
	boot, now, err := clock.Now()
	if err != nil {
		t.Fatal(err)
	}
	correlation := newFlowUUID(t)
	receipt := notifier.NewStructuredDelivery(notifier.ManagedInstallation{ControlRoot: control, Expected: snapshot}, physical).Deliver(ctx, notification.Request{
		Content:       notification.Content{Title: "agent-notify e2e", Body: correlation, Category: "info"},
		CorrelationID: correlation,
		Deadline:      notification.Deadline{BootID: boot, NotAfter: now + 12},
		Policy:        notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true, SoundEnabled: false},
		Navigation:    notification.None,
		Silent:        true,
	})
	if receipt.Status != "submitted" || receipt.Reason != "os_accepted" || receipt.RetrySafe {
		t.Fatalf("native send from generation A: %+v", receipt)
	}
}

func launchGenerationAfterColdStart(t *testing.T, ctx context.Context, bundle string) {
	t.Helper()
	id := nativeBundleID(bundle)
	if id == "com.claude.desktop.notifier" && os.Getenv("AGENT_NOTIFY_DARWIN_E2E") != "1" {
		t.Log("skipping LaunchServices cold-start of production bundle; set AGENT_NOTIFY_DARWIN_E2E=1")
		return
	}
	physical, err := filepath.EvalSymlinks(bundle)
	if err != nil {
		t.Fatal(err)
	}
	var before syscall.Stat_t
	if err := syscall.Stat(physical, &before); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(physical, "Contents", "MacOS", "terminal-notifier-modern")
	killHelpersAt(t, exe)
	openCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	open := exec.CommandContext(openCtx, "/usr/bin/open", "-n", "-W", "-a", physical, "--args", "--capabilities-json")
	open.Dir = "/"
	if out, err := open.CombinedOutput(); err != nil {
		t.Fatalf("cold-start LaunchServices launch of generation failed: %s %v", out, err)
	}
	var after syscall.Stat_t
	if err := syscall.Stat(physical, &after); err != nil || after.Ino != before.Ino {
		t.Fatal("cold-start moved generation identity")
	}
}

func killHelpersAt(t *testing.T, exe string) {
	t.Helper()
	if !strings.Contains(exe, "/generation-") || !strings.HasSuffix(exe, "/Contents/MacOS/terminal-notifier-modern") {
		t.Fatal("refusing to scan processes without a published generation executable")
	}
	signalHelpersAt(exe, syscall.SIGTERM)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !helpersRunningAt(exe) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	signalHelpersAt(exe, syscall.SIGKILL)
}

func signalHelpersAt(exe string, sig syscall.Signal) {
	out, err := exec.Command("/bin/ps", "-axww", "-o", "pid=,command=").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, exe) {
			continue
		}
		var pid int
		if _, err := fmt.Sscan(line, &pid); err != nil || pid <= 1 {
			continue
		}
		_ = syscall.Kill(pid, sig)
	}
}

func helpersRunningAt(exe string) bool {
	out, err := exec.Command("/bin/ps", "-axww", "-o", "command=").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), exe)
}

func nativeBundleID(bundle string) string {
	data, err := os.ReadFile(filepath.Join(bundle, "Contents", "Info.plist"))
	if err != nil {
		return ""
	}
	key := []byte("<key>CFBundleIdentifier</key>")
	i := bytes.Index(data, key)
	if i < 0 {
		return ""
	}
	rest := data[i+len(key):]
	start := bytes.Index(rest, []byte("<string>"))
	end := bytes.Index(rest, []byte("</string>"))
	if start < 0 || end <= start {
		return ""
	}
	return string(bytes.TrimSpace(rest[start+len("<string>") : end]))
}

func newFlowUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
