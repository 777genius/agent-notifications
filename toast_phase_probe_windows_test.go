//go:build windows

package notifier

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/opencodeinstall"
)

const probeOriginalScriptSHA = "485891c20f7b34ebd774e24ad3fdfdb6eae2555f301adf47b806022669ac6d40"
const probePrefix = "AN_TOAST_PHASE:"

type probeStream struct {
	mu      sync.Mutex
	hash    hash.Hash
	bytes   int64
	pending string
	unknown int
	phases  []map[string]any
	allowed map[string]bool
	began   time.Time
}

func (s *probeStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hash.Write(p)
	s.bytes += int64(len(p))
	if s.allowed == nil {
		return len(p), nil
	}
	for _, b := range p {
		if b == '\n' {
			line := strings.TrimSuffix(s.pending, "\r")
			if s.allowed[line] {
				s.phases = append(s.phases, map[string]any{"phase": strings.TrimPrefix(line, probePrefix), "observedElapsedNS": time.Since(s.began).Nanoseconds()})
				s.allowed[line] = false // repeated/unknown markers never acquire phase authority
			} else {
				s.unknown++
			}
			s.pending = ""
		} else if len(s.pending) < 256 {
			s.pending += string(b)
		}
	}
	return len(p), nil
}

// Diagnostic overlay only. Existing readiness, request deadlines and submission
// classification are real; the existing submit variable wraps command observation.
func TestTESTWindowsToastPhaseProbe(t *testing.T) {
	began := time.Now()
	parent, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	clock := SystemBootClock{}
	boot, now, err := clock.Now()
	if err != nil {
		t.Fatal("TEST_probe_clock_unavailable")
	}
	root, executable := os.Getenv("TEST_AN_TOAST_CONTROL_ROOT"), os.Getenv("TEST_AN_TOAST_INSTALLED_EXECUTABLE")
	sandbox := os.Getenv("AN_TEST_ROOT")
	if !filepath.IsAbs(sandbox) || !regexp.MustCompile(`^TEST-installed-[a-f0-9]{24}$`).MatchString(filepath.Base(sandbox)) || root != filepath.Join(sandbox, "control") || executable != filepath.Join(sandbox, "runtime", "claude-notifications-windows-amd64.exe") {
		t.Fatal("TEST_probe_inputs_missing")
	}
	originalSHA := sha256.Sum256([]byte(windowsToastPowerShell))
	if hex.EncodeToString(originalSHA[:]) != probeOriginalScriptSHA {
		t.Fatal("TEST_probe_original_script_changed")
	}
	script := windowsToastPowerShell
	pieces := []struct{ name, code string }{
		{"add_type", "Add-Type -AssemblyName System.Runtime.WindowsRuntime"},
		{"xml_type", "$doc=[Windows.Data.Xml.Dom.XmlDocument,Windows.Data.Xml.Dom.XmlDocument,ContentType=WindowsRuntime]::New()"},
		{"xml_load", "$doc.LoadXml($xmlText)"},
		{"toast_new", "$toast=[Windows.UI.Notifications.ToastNotification,Windows.UI.Notifications,ContentType=WindowsRuntime]::New($doc)"},
		{"notifier_show", "[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($env:AGENT_NOTIFICATIONS_TOAST_APP_ID).Show($toast)"},
	}
	allowed := map[string]bool{}
	markers := []string{"Write-Output '" + probePrefix + "script.enter'; "}
	allowed[probePrefix+"script.enter"] = true
	for _, piece := range pieces {
		if strings.Count(script, piece.code) != 1 {
			t.Fatal("TEST_probe_script_boundary_changed")
		}
		before, after := "Write-Output '"+probePrefix+piece.name+".before'; ", "; Write-Output '"+probePrefix+piece.name+".after'"
		markers = append(markers, before, after)
		allowed[probePrefix+piece.name+".before"], allowed[probePrefix+piece.name+".after"] = true, true
		script = strings.Replace(script, piece.code, before+piece.code+after, 1)
	}
	script = markers[0] + script
	projection := script
	for _, marker := range markers {
		projection = strings.ReplaceAll(projection, marker, "")
	}
	if projection != windowsToastPowerShell {
		t.Fatal("TEST_probe_projection_changed")
	}
	stdout := &probeStream{hash: sha256.New(), allowed: allowed, began: began}
	stderr := &probeStream{hash: sha256.New()}
	var called, cancelled, cancelSucceeded atomic.Bool
	var exitCode *int
	var startNS, startReturnNS, waitNS int64
	var processStarted, shortcutReady bool
	previous := submitWindowsToast
	t.Cleanup(func() { submitWindowsToast = previous })
	submitWindowsToast = func(ctx context.Context, payload windowsToastPayload) error {
		called.Store(true)
		data, err := encodeWindowsToast(payload)
		if err != nil {
			return err
		}
		powershell, err := resolveWindowsPowerShell()
		if err != nil {
			return err
		}
		cmd := windowsToastCommand(ctx, powershell, data, payload.AppID)
		cmd.Args[len(cmd.Args)-1] = script // sole command mutation: reversible markers
		cmd.Stdout, cmd.Stderr = stdout, stderr
		originalCancel := cmd.Cancel
		cmd.Cancel = func() error {
			cancelled.Store(true)
			err := originalCancel()
			cancelSucceeded.Store(err == nil)
			return err
		}
		startNS = time.Since(began).Nanoseconds()
		err = cmd.Start() // same Run semantics, with observed Start/Wait boundaries
		startReturnNS = time.Since(began).Nanoseconds()
		processStarted = err == nil
		if err == nil {
			err = cmd.Wait() // never return submitted merely because Start succeeded
			waitNS = time.Since(began).Nanoseconds()
		}
		if cmd.ProcessState != nil {
			code := cmd.ProcessState.ExitCode()
			exitCode = &code
		}
		if ctx.Err() != nil {
			return errors.Join(ctx.Err(), err)
		}
		return err
	}
	delivery := NewTrustedWindowsToastDelivery(clock, opencodeinstall.OpenCodeToastAppID, func(ctx context.Context) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := opencodeinstall.WindowsShortcutReady(root, executable)
		shortcutReady = err == nil
		return err
	})
	receipt := delivery.Deliver(parent, notification.Request{Content: notification.Content{Title: "TEST toast phase probe", Body: "TEST diagnostic submission", Category: "info"}, Deadline: notification.Deadline{BootID: boot, NotAfter: now + 15}, Policy: notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true}, Navigation: notification.None, Silent: true})
	scriptSHA := sha256.Sum256([]byte(script))
	record := map[string]any{"schema": 1, "purpose": "TEST Windows toast phase probe", "qualificationGranted": false, "visibleToastProved": false, "originalScriptSHA256": probeOriginalScriptSHA, "markedScriptSHA256": hex.EncodeToString(scriptSHA[:]), "projectionSHA256": probeOriginalScriptSHA, "phases": append([]map[string]any{}, stdout.phases...), "commandInvoked": called.Load(), "commandStartCallElapsedNS": startNS, "commandStartReturnElapsedNS": startReturnNS, "processStarted": processStarted, "shortcutReady": shortcutReady, "commandWaitReturnElapsedNS": waitNS, "waitReturned": waitNS > 0, "exitCode": exitCode, "commandCancelCalled": cancelled.Load(), "commandCancelSucceeded": cancelSucceeded.Load(), "stdoutBytes": stdout.bytes, "stdoutSHA256": hex.EncodeToString(stdout.hash.Sum(nil)), "stdoutUnknownLines": stdout.unknown, "stdoutPartialLine": stdout.pending != "", "stderrBytes": stderr.bytes, "stderrSHA256": hex.EncodeToString(stderr.hash.Sum(nil)), "desktopStatus": receipt.Status, "desktopReason": receipt.Reason}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal("TEST_probe_record_encoding_failed")
	}
	t.Log("AN_WINDOWS_TOAST_PHASE_PROBE " + string(raw))
}
