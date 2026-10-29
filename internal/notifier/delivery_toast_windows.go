//go:build windows

package notifier

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/777genius/agent-notifications/internal/commandrun"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/opencodeinstall"
	"github.com/777genius/agent-notifications/internal/windowscallback"
)

type windowsPowerShellToastSession struct {
	appID, controlRoot, executable string

	// Inert retained callback composition. Checkpoint B owns installed readiness;
	// capture release bytes only, without inspecting helper/snapshot/native state.
	callback                windowscallback.Port
	requireOpenCodeShortcut bool
	trustedReady            func(context.Context) error
}

// TEMPORARY fixed checkpoints; never merge this script into the final candidate.
const windowsToastPowerShell = `$ErrorActionPreference='Stop'
$TESTDeadline=[long]0
$TESTTraceEnabled=[long]::TryParse($env:TEST_PI_TRACE_DEADLINE,[ref]$TESTDeadline) -and $TESTDeadline -ge 0 -and $TESTDeadline -le 4294967295L
function TESTTrace([string]$phase) {
  try {
    if (!$TESTTraceEnabled) { return }
    $now=([long][Environment]::TickCount -band 4294967295L)
    $delta=$TESTDeadline-$now
    if ($delta -gt 2147483647L) { $delta-=4294967296L }
    if ($delta -lt -2147483648L) { $delta+=4294967296L }
    $remaining=[Math]::Max(0L,[Math]::Min(15000L,$delta))
    $elapsed=[Math]::Max(0L,[Math]::Min(60000L,15000L-$delta))
    $state='active'; if ($delta -le 0) { $state='deadline_exceeded' }
    [Console]::Error.WriteLine('{"type":"TEST_pi_native_trace","phase":"'+$phase+'","channel":"desktop","elapsed_ms":'+$elapsed+',"remaining_ms":'+$remaining+',"context":"'+$state+'"}')
  } catch { }
}
try {
  TESTTrace 'powershell_entered'
  [Windows.UI.Notifications.ToastNotificationManager,Windows.UI.Notifications,ContentType=WindowsRuntime] | Out-Null
  TESTTrace 'winrt_ready'
  $xmlBytes=[Convert]::FromBase64String($env:AGENT_NOTIFICATIONS_TOAST_XML)
  $xmlText=[Text.Encoding]::UTF8.GetString($xmlBytes)
  $doc=[Windows.Data.Xml.Dom.XmlDocument,Windows.Data.Xml.Dom.XmlDocument,ContentType=WindowsRuntime]::New()
  $doc.LoadXml($xmlText)
  TESTTrace 'xml_loaded'
  $toast=[Windows.UI.Notifications.ToastNotification,Windows.UI.Notifications,ContentType=WindowsRuntime]::New($doc)
  TESTTrace 'toast_created'
  $notifier=[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($env:AGENT_NOTIFICATIONS_TOAST_APP_ID)
  TESTTrace 'notifier_created'
  $notifier.Show($toast)
  TESTTrace 'toast_shown'
} catch { TESTTrace 'toast_failed'; exit 1 }`

var submitWindowsToast = runWindowsToast
var resolveWindowsPowerShell = systemWindowsPowerShell

func systemWindowsPowerShell() (string, error) {
	systemDir, err := windows.GetSystemDirectory()
	if err != nil {
		return "", err
	}
	path := filepath.Join(systemDir, "WindowsPowerShell", "v1.0", "powershell.exe")
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("system PowerShell is not a regular file")
	}
	return path, nil
}

func runWindowsToast(ctx context.Context, p windowsToastPayload) error {
	data, err := encodeWindowsToast(p)
	if err != nil {
		return err
	}
	powershell, err := resolveWindowsPowerShell()
	if err != nil {
		return err
	}
	cmd := windowsToastCommand(ctx, powershell, data, p.AppID)
	trace := &testPowerShellTrace{}
	cmd.Stderr = trace
	err = commandrun.Run(ctx, cmd)
	trace.forward(os.Stderr)
	if ctx.Err() != nil {
		return errors.Join(ctx.Err(), err)
	}
	return err
}

// Construction shares the native child privacy boundary without launching it.
func windowsToastCommand(ctx context.Context, powershell string, data []byte, appID string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", windowsToastPowerShell)
	cmd.Env = append(nativeNotificationEnvironment(),
		"AGENT_NOTIFICATIONS_TOAST_XML="+base64.StdEncoding.EncodeToString(data),
		"AGENT_NOTIFICATIONS_TOAST_APP_ID="+appID,
	)
	if deadline := testToastDeadline(ctx); deadline != "" {
		cmd.Env = append(cmd.Env, "TEST_PI_TRACE_DEADLINE="+deadline)
	}
	// The terminal bell frees the hook's hidden console before delivery, so a
	// console child would otherwise get a new, visible console window.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd
}

func openWindowsToast(ctx context.Context) (windowsToastSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := resolveWindowsPowerShell(); err != nil {
		return nil, err
	}
	return windowsPowerShellToastSession{callback: windowscallback.NewPort()}, nil
}

func NewOpenCodeWindowsToastDelivery(clock BootClock) *WindowsToastDelivery {
	return &WindowsToastDelivery{Clock: clock, Open: openOpenCodeWindowsToast}
}

// NewTrustedWindowsToastDelivery receives identity and shortcut verification
// only from trusted host composition. Native event/config text must never supply
// appID. A missing identity or verifier denies delivery; no legacy fallback.
func NewTrustedWindowsToastDelivery(clock BootClock, appID string, ready func(context.Context) error) *WindowsToastDelivery {
	return &WindowsToastDelivery{Clock: clock, Open: func(ctx context.Context) (windowsToastSession, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if appID == "" || ready == nil {
			return nil, errors.New("trusted toast identity unavailable")
		}
		if _, err := resolveWindowsPowerShell(); err != nil {
			return nil, err
		}
		return windowsPowerShellToastSession{appID: appID, trustedReady: ready, callback: windowscallback.NewPort()}, nil
	}}
}

func openOpenCodeWindowsToast(ctx context.Context) (windowsToastSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := resolveWindowsPowerShell(); err != nil {
		return nil, err
	}
	root := os.Getenv("AGENT_NOTIFICATIONS_CONTROL_ROOT")
	if root == "" {
		var err error
		root, err = installruntime.ControlRoot()
		if err != nil {
			return nil, err
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return windowsPowerShellToastSession{appID: opencodeinstall.OpenCodeToastAppID, controlRoot: root,
		executable: executable, requireOpenCodeShortcut: true, callback: windowscallback.NewPort()}, nil
}

func (s windowsPowerShellToastSession) Ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.trustedReady != nil {
		return s.trustedReady(ctx)
	}
	if s.requireOpenCodeShortcut {
		return opencodeinstall.WindowsShortcutReady(s.controlRoot, s.executable)
	}
	return nil
}

func (s windowsPowerShellToastSession) Submit(ctx context.Context, r notification.Request) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	appID := s.appID
	if appID == "" {
		appID = windowsToastAppID
	}
	return submitWindowsToast(ctx, windowsToastPayload{
		AppID:  appID,
		Title:  r.Content.Title,
		Body:   desktopBodyWithSubtitle(r.Content),
		Silent: r.Silent || !r.Policy.SoundEnabled,
	})
}

func (windowsPowerShellToastSession) Close() error { return nil }
