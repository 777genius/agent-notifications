//go:build windows

package clientsetup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify/registration"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"golang.org/x/sys/windows"
)

func TestWindowsApplyAcceptsIdentityModeWithoutUnixExecute(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Clean(strings.TrimPrefix(root, `\\?\`))
	runtimeRoot := filepath.Join(root, "runtime")
	control := filepath.Join(root, "control")
	client := filepath.Join(root, "client")
	for _, p := range []string{runtimeRoot, control, client} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	restrictPrivate(t, control)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	command := filepath.Join(runtimeRoot, "bin", "claude-notifications.bat")
	l, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, Owner: Managed, RuntimeRoot: runtimeRoot, ConsumerID: "legacy-hooks",
		Consumer: installruntime.Consumer{Registration: filepath.Join(client, "hooks.json"), Commands: []string{"legacy hook"}},
		Files:    []installruntime.File{{Path: command, Data: installruntime.WindowsLauncherScript("claude-notifications", "claude-notifications-windows-amd64.exe"), Mode: 0755}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if mode := l.Files[command].Mode; mode&0111 != 0 {
		t.Fatalf("windows identityMode must drop unix execute bits, got %o", mode)
	}
	got, err := Apply(ctx, Request{
		ControlRoot: control, RuntimeRoot: runtimeRoot, Command: command,
		ConfigPath: filepath.Join(client, "config.toml"), Provider: registration.Codex,
		Mode: Managed, ExpectedGeneration: l.Generation,
	})
	if err != nil || !got.Changed {
		t.Fatal(got, err)
	}
}

func TestWindowsSkillInspectAfterApply(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Clean(strings.TrimPrefix(root, `\\?\`))
	runtimeRoot := filepath.Join(root, "runtime")
	control := filepath.Join(root, "control")
	client := filepath.Join(root, "client")
	for _, p := range []string{runtimeRoot, control, client} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	restrictPrivate(t, control)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	command := filepath.Join(runtimeRoot, "bin", "claude-notifications.bat")
	source := filepath.Join(runtimeRoot, "skills", "agent-notify", "SKILL.md")
	destination := filepath.Join(client, "skills", "agent-notify", "SKILL.md")
	l, err := installruntime.Commit(ctx, installruntime.Request{
		ControlRoot: control, Owner: Managed, RuntimeRoot: runtimeRoot, ConsumerID: "legacy-hooks",
		Consumer: installruntime.Consumer{Registration: filepath.Join(client, "hooks.json"), Commands: []string{"legacy hook"}},
		Files: []installruntime.File{
			{Path: command, Data: installruntime.WindowsLauncherScript("claude-notifications", "claude-notifications-windows-amd64.exe"), Mode: 0755},
			{Path: source, Data: []byte("canonical test skill\n"), Mode: 0600},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := Request{
		ControlRoot: control, RuntimeRoot: runtimeRoot, Command: command,
		ConfigPath: filepath.Join(client, "config.toml"), Provider: registration.Codex,
		Mode: Managed, ExpectedGeneration: l.Generation,
		SkillProjection: &SkillProjection{SourcePath: source, DestinationPath: destination},
	}
	got, err := Apply(ctx, req)
	if err != nil || !got.Changed {
		t.Fatal(got, err)
	}
	req.ExpectedGeneration = got.Ledger.Generation
	facts, err := Inspect(ctx, req)
	if err != nil || !facts.Registered || !facts.SkillProjected {
		t.Fatalf("inspect after skill apply: %+v %v", facts, err)
	}
	again, err := Apply(ctx, req)
	if err != nil || again.Changed {
		t.Fatalf("skill no-op: %+v %v", again, err)
	}
}

func restrictPrivate(t *testing.T, path string) {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES|windows.WRITE_DAC|windows.WRITE_OWNER|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil {
		t.Fatal(err)
	}
	inherit := ""
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		inherit = "OICI"
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + sid.String() + "D:P(A;" + inherit + ";FA;;;" + sid.String() + ")(A;" + inherit + ";FA;;;SY)(A;" + inherit + ";FA;;;BA)")
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		t.Fatal(err)
	}
	flags := windows.SECURITY_INFORMATION(windows.OWNER_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if err = windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, flags, owner, nil, acl, nil); err == nil {
		return
	}
	if e := windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, owner, nil, nil, nil); e != nil {
		t.Fatal(err)
	}
	if err = windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}
