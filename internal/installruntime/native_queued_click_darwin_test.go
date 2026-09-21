//go:build darwin

package installruntime

import (
	"context"
	"os"
	"testing"
	"time"
)

// Opt-in queued-click harness after a published generation update.
// This test does not send a notification unless AGENT_NOTIFY_QUEUED_CLICK=1
// and AGENT_NOTIFY_QUEUED_CLICK_ATTEMPT is a unique UUID. Default CI skips.
// Do not point at a real user project or the production bundle ID.
func TestNativeQueuedClickAfterGenerationUpdateHarness(t *testing.T) {
	if os.Getenv("AGENT_NOTIFY_QUEUED_CLICK") != "1" {
		t.Skip("queued Notification Center click is a manual gate; installer identity is TestNativeExactHeadGenerationsPreserveCallbackIdentity")
	}
	attempt := os.Getenv("AGENT_NOTIFY_QUEUED_CLICK_ATTEMPT")
	if len(attempt) != 36 {
		t.Fatal("set AGENT_NOTIFY_QUEUED_CLICK_ATTEMPT to a unique UUID before sending")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	_, r := request(t)
	first := exactHeadNativeApp(t, "generation-A")
	change, err := StageNative(ctx, r.ControlRoot, first)
	if err != nil {
		t.Fatal(err)
	}
	r.Native = change
	if _, err := Commit(ctx, r); err != nil {
		t.Fatal(err)
	}
	pathA := change.After.Path
	inodeA, err := nativeDirectoryID(pathA)
	if err != nil {
		t.Fatal(err)
	}
	second := exactHeadNativeApp(t, "generation-B")
	change, err = StageNative(ctx, r.ControlRoot, second)
	if err != nil {
		t.Fatal(err)
	}
	r.Native = change
	ledger, err := Commit(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if ledger.Native == nil || ledger.Native.Path == pathA {
		t.Fatal("active path did not move to generation B")
	}
	id, err := nativeDirectoryID(pathA)
	if err != nil || id != inodeA {
		t.Fatal("generation A identity moved during update")
	}
	t.Logf("attempt=%s generationA=%s generationB=%s", attempt, pathA, ledger.Native.Path)
	t.Log("send one notification from generation A before this update in a dedicated test profile, then click it; this harness only preserves A")
}
