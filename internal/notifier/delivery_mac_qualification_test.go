//go:build darwin && cgo && pr3_native_integration

package notifier

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/notification"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// This opt-in sender integration test intentionally produces one notification.
// It is excluded from ordinary tests, including ordinary Darwin tests. The Mac
// coordinator must first install the signed PR2+PR3 candidate into a NEW owned
// qualification root, create its private spool, and supply a NEW test chat.
// Never set these variables to a user's production runtime or production chat.
func TestPR3ManagedMacQualification(t *testing.T) {
	if os.Getenv("AGENT_NOTIFY_PR3_RUN_NATIVE") != "1" {
		t.Skip("requires explicit coordinator opt-in in a new PR2 test installation")
	}
	root := os.Getenv("AGENT_NOTIFY_PR3_TEST_ROOT")
	if !filepath.IsAbs(root) || !strings.HasPrefix(filepath.Base(root), "pr3-qualification-") {
		t.Fatal("requires a new named qualification root")
	}
	physical, err := filepath.EvalSymlinks(root)
	if err != nil || physical != root {
		t.Fatal("qualification root must be physical")
	}
	marker, err := readPrivate(filepath.Join(root, "PR3-QUALIFICATION-ONLY"), 128)
	if err != nil || string(marker) != "new-owned-pr3-test-installation\n" {
		t.Fatal("missing private qualification-only installation marker")
	}
	target := notification.DesktopTarget{ThreadID: os.Getenv("AGENT_NOTIFY_PR3_TEST_THREAD"), ApplicationPath: os.Getenv("AGENT_NOTIFY_PR3_CODEX_APP"), TeamID: os.Getenv("AGENT_NOTIFY_PR3_CODEX_TEAM")}
	if target.ThreadID == "" || target.ApplicationPath == "" || target.TeamID == "" {
		t.Fatal("coordinator must supply a new test chat and trusted setup identity")
	}

	clock := SystemBootClock{}
	boot, now, err := clock.Now()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	snapshot, err := installruntime.ReadPolicySnapshot(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	install := ManagedInstallation{ControlRoot: root, Expected: snapshot.Installation}
	correlation, err := uuidString()
	if err != nil {
		t.Fatal(err)
	}
	request := notification.Request{Content: notification.Content{Title: "--help", Body: "[important]\n-execute 👩‍💻 PR3 qualification", Subtitle: "-execute", Category: "info"}, CorrelationID: correlation, Deadline: notification.Deadline{BootID: boot, NotAfter: now + 15}, Policy: notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true, ClickToFocus: true, SoundEnabled: false}, Navigation: notification.Required, Target: target, Silent: true}
	receipt := NewStructuredDelivery(install, filepath.Join(root, "spool")).Deliver(ctx, request)
	data, _ := json.Marshal(receipt)
	t.Logf("native receipt (OS acceptance only, no banner/click claim): %s", data)
	if receipt.Status != "submitted" || receipt.Reason != "os_accepted" || receipt.RetrySafe || receipt.Navigation.Precision != "chat_id" {
		t.Fatalf("native outcome %s/%s; do not automatically retry unknown", receipt.Status, receipt.Reason)
	}
	// Do not remove the managed installation: callback and later coordinator
	// click/removed-cwd/upgrade tests must remain independent of this test binary.
}
