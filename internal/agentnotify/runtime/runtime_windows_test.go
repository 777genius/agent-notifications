//go:build windows

package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	root := t.TempDir()
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
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	entries := []windows.EXPLICIT_ACCESS{
		{AccessPermissions: windows.GENERIC_ALL, AccessMode: windows.GRANT_ACCESS, Inheritance: windows.NO_INHERITANCE, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER, TrusteeValue: windows.TrusteeValueFromSID(sid.User.Sid)}},
		{AccessPermissions: windows.GENERIC_ALL, AccessMode: windows.GRANT_ACCESS, Inheritance: windows.NO_INHERITANCE, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER, TrusteeValue: windows.TrusteeValueFromSID(system)}},
		{AccessPermissions: windows.GENERIC_ALL, AccessMode: windows.GRANT_ACCESS, Inheritance: windows.NO_INHERITANCE, Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_GROUP, TrusteeValue: windows.TrusteeValueFromSID(admins)}},
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
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
	d, ok := b.opts.DeliveryFactory(notifier.ManagedInstallation{}, o.SpoolRoot, o.BootClock).(*notifier.WindowsToastDelivery)
	if !ok || d.Clock != o.BootClock {
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
