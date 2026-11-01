//go:build windows

package setup

import (
    "testing"

    "github.com/777genius/agent-notifications/internal/notification"
)

// Consent never consumes an extra immutable callback generation, but
// a different or missing generation identity cannot borrow an old binding.
func TestWindowsCallbackGenerationReuseOnlyForConsent(t *testing.T) {
    previous := Route{
        LocalRouting: true,
        Windows: notification.WindowsBinding{
            SnapshotPath: `\\?\C:\TEST\windows-callback\aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\generation.wne`,
            SHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
        },
    }
    current := previous
    current.AllowUnknownCaller = true
    current.AllowCallerAsserted = true
    if !sameWindowsCallbackGeneration(current, previous) {
        t.Fatal("consent-only change incorrectly requires another Windows generation")
    }
    altered := []struct {
        name string
        edit func(*Route)
    }{
        {"disabled",func(r *Route) {r.LocalRouting=false}},
        {"foreign-application",func(r *Route) {r.ApplicationPath="other"}},
        {"foreign-team",func(r *Route) {r.TeamID="other"}},
        {"unbound",func(r *Route) {r.Windows=notification.WindowsBinding{}}},
        {"different-snapshot",func(r *Route) {r.Windows.SnapshotPath+=".other"}},
        {"different-digest",func(r *Route) {r.Windows.SHA256="dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}},
    }
    for _, tt := range altered {
        t.Run(tt.name,func(t *testing.T){
            changed:=current
            tt.edit(&changed)
            if sameWindowsCallbackGeneration(changed,previous) {
                t.Fatal("changed generation or route identity was reused")
            }
        })
    }
    legacy:=previous
    legacy.ApplicationPath="foreign"
    if sameWindowsCallbackGeneration(current,legacy) {
        t.Fatal("foreign previous route identity admitted")
    }
}
