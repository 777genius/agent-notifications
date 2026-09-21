//go:build windows

package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier"
	"golang.org/x/sys/windows"
)

type windowsBoot struct{}

func (windowsBoot) Now() (string, float64, error) { return "boot", 100, nil }

type windowsFakeDelivery struct {
	send func(notification.Request) string
}

func (f windowsFakeDelivery) CheckReadiness(_ context.Context, r notification.Request) notification.Readiness {
	return notification.Readiness{CorrelationID: r.CorrelationID, Status: "ready", Reason: "permission_authorized", Navigation: notification.NavigationResult{Capability: "disabled", Precision: "none"}}
}
func (f windowsFakeDelivery) Deliver(_ context.Context, r notification.Request) notification.Receipt {
	s := "submitted"
	if f.send != nil {
		s = f.send(r)
	}
	return notification.Receipt{CorrelationID: r.CorrelationID, Status: s, Reason: "test_outcome", Navigation: notification.NavigationResult{Capability: "disabled", Precision: "none"}}
}

func windowsOptions(t *testing.T) Options {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Clean(strings.TrimPrefix(root, `\\?\`))
	return Options{
		ControlRoot:  filepath.Join(root, "control"),
		JournalRoot:  filepath.Join(root, "journal"),
		GlobalConfig: filepath.Join(root, "global.json"),
		SpoolRoot:    filepath.Join(root, "spool"),
		BootClock:    windowsBoot{},
		JournalClock: journal.ClockFunc(func() journal.Sample { return journal.Sample{Boot: "boot", Seconds: 100, Available: true} }),
		ReadSnapshot: func(context.Context, string) (installruntime.PolicySnapshot, error) {
			return installruntime.PolicySnapshot{Installation: installruntime.InstalledSnapshot{Enabled: true, Ledger: installruntime.Ledger{ID: "/none"}}, Fields: map[string]json.RawMessage{"schemaVersion": json.RawMessage(`1`), "enabled": json.RawMessage(`true`), "route": json.RawMessage(`{"localRouting":false}`)}}, nil
		},
		ReadGlobal: func(string) ([]byte, error) {
			return []byte(`{"foreign":{"keep":true},"notifications":{"desktop":{"enabled":true,"sound":true,"clickToFocus":true}}}`), nil
		},
		DeliveryFactory: func(notifier.ManagedInstallation, string, notifier.BootClock) Delivery { return windowsFakeDelivery{} },
	}
}

func windowsRestrict(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES|windows.WRITE_DAC|windows.WRITE_OWNER|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	sid, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil {
		t.Fatal(err)
	}
	inherit := ""
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		inherit = "OICI"
	}
	user := sid.User.Sid
	sd, err := windows.SecurityDescriptorFromString("O:" + user.String() + "D:P(A;" + inherit + ";FA;;;" + user.String() + ")(A;" + inherit + ";FA;;;SY)(A;" + inherit + ";FA;;;BA)")
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

func TestWindowsProductionClocksShareBootIdentity(t *testing.T) {
	o := windowsOptions(t)
	o.BootClock = nil
	o.JournalClock = nil
	b, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	now := b.Clock().Now()
	if now.BootID == "" {
		t.Fatal("production windows boot clock unavailable")
	}
	sample := b.opts.JournalClock.Sample()
	if !sample.Available || sample.Boot != now.BootID {
		t.Fatalf("clocks diverged journal=%+v boot=%+v", sample, now)
	}
}

func TestWindowsProductionFactoryUsesToast(t *testing.T) {
	o := windowsOptions(t)
	o.DeliveryFactory = nil
	b, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := b.opts.DeliveryFactory(notifier.ManagedInstallation{}, o.SpoolRoot, o.BootClock).(*sessionDelivery)
	if !ok {
		t.Fatal("windows production delivery is not fenced")
	}
	toast, toastOK := d.Delivery.(*notifier.WindowsToastDelivery)
	if !toastOK || toast.Clock != o.BootClock {
		t.Fatal("windows factory still uses macos native delivery")
	}
}

func TestWindowsSessionEligibleWithoutNative(t *testing.T) {
	o := windowsOptions(t)
	o.BootClock = nil
	b, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	s := b.Status(context.Background())
	if s.OfflineCapability != "eligible" || s.Configuration != "configured" {
		t.Fatal(s)
	}
}

func TestWindowsProductionNotifyNoneWithDefaultClocks(t *testing.T) {
	o := windowsOptions(t)
	o.JournalClock = journal.PlatformClock{}
	o.BootClock = nil
	windowsRestrict(t, o.JournalRoot)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := journal.Initialize(ctx, journal.Options{Root: o.JournalRoot, Clock: o.JournalClock}); err != nil {
		t.Fatal(err)
	}
	var sent int
	o.DeliveryFactory = func(notifier.ManagedInstallation, string, notifier.BootClock) Delivery {
		return windowsFakeDelivery{send: func(notification.Request) string { sent++; return "submitted" }}
	}
	b, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	now := b.Clock().Now()
	if now.BootID == "" {
		t.Fatal("production clock unavailable")
	}
	id := "windows-none"
	got := b.Notify(context.Background(), agentnotify.Payload{Title: "test", Body: "literal", Category: "info", RequestID: &id, Navigation: notification.None}, origin.Context{Provider: "codex", Namespace: "tests", SessionID: id, Provenance: origin.ClientMetadata, Locality: origin.Local, Interface: origin.Desktop}, notification.Deadline{BootID: now.BootID, NotAfter: now.NotAfter + 10})
	if got.Status != "submitted" || sent != 1 {
		t.Fatal(got, sent)
	}
}
