//go:build windows

package notifier

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"

	"github.com/777genius/agent-notifications/internal/notification"
)

type windowsPowerShellToastSession struct{}

const windowsToastPowerShell = `$ErrorActionPreference='Stop'; Add-Type -AssemblyName System.Runtime.WindowsRuntime; $xmlBytes=[Convert]::FromBase64String($env:AGENT_NOTIFICATIONS_TOAST_XML); $xmlText=[Text.Encoding]::UTF8.GetString($xmlBytes); $doc=[Windows.Data.Xml.Dom.XmlDocument,Windows.Data.Xml.Dom.XmlDocument,ContentType=WindowsRuntime]::New(); $doc.LoadXml($xmlText); $toast=[Windows.UI.Notifications.ToastNotification,Windows.UI.Notifications,ContentType=WindowsRuntime]::New($doc); [Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($env:AGENT_NOTIFICATIONS_TOAST_APP_ID).Show($toast)`

var submitWindowsToast = runWindowsToast
var lookWindowsPowerShell = exec.LookPath

func runWindowsToast(ctx context.Context, p windowsToastPayload) error {
	data, err := encodeWindowsToast(p)
	if err != nil {
		return err
	}
	powershell, err := lookWindowsPowerShell("powershell.exe")
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", windowsToastPowerShell)
	cmd.Env = append(os.Environ(),
		"AGENT_NOTIFICATIONS_TOAST_XML="+base64.StdEncoding.EncodeToString(data),
		"AGENT_NOTIFICATIONS_TOAST_APP_ID="+p.AppID,
	)
	err = cmd.Run()
	if ctx.Err() != nil {
		return errors.Join(ctx.Err(), err)
	}
	return err
}

func openWindowsToast(ctx context.Context) (windowsToastSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := lookWindowsPowerShell("powershell.exe"); err != nil {
		return nil, err
	}
	return windowsPowerShellToastSession{}, nil
}

func (windowsPowerShellToastSession) Ready(ctx context.Context) error { return ctx.Err() }

func (windowsPowerShellToastSession) Submit(ctx context.Context, r notification.Request) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return submitWindowsToast(ctx, windowsToastPayload{
		AppID:  windowsToastAppID,
		Title:  r.Content.Title,
		Body:   r.Content.Body,
		Silent: r.Silent || !r.Policy.SoundEnabled,
	})
}

func (windowsPowerShellToastSession) Close() error { return nil }
