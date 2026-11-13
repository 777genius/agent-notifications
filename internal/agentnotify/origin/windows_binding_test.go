package origin

import (
	"testing"

	"github.com/777genius/agent-notifications/internal/notification"
)

// Breakage: origin classifies explicit Windows binding as macOS/generic
// unsupported, or later policy mutation changes an already captured target.
func TestWindowsBindingValueSnapshot(t *testing.T) {
	o := Context{Provider: "codex", Namespace: "TEST/windows-binding", SessionID: "TEST/opaque%id", Provenance: ClientMetadata, Locality: Local, Interface: Desktop}
	b := notification.WindowsBinding{SnapshotPath: `\\?\C:\TEST\generation.wne`, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	p := RoutePolicy{Platform: "windows", LocalRouting: true, Windows: b}
	got := ResolveCodex(o, p)
	p.Windows.SHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if got.Desktop.Windows != b || got.Desktop.ThreadID != o.SessionID || got.Navigation.Capability != "available" || got.Navigation.Scope != "selected_windows_generation" {
		t.Fatal(got)
	}
	for _, id := range []string{".", ".."} {
		o.SessionID = id
		if ResolveCodex(o, p).Navigation.Capability == "available" {
			t.Fatal("normalized thread admitted", id)
		}
	}
}
