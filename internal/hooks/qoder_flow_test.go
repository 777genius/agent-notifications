package hooks

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/qodersource"
)

// Exercise real SDK decode and policy together: continuation, duplicates and
// product isolation must not notify twice or parse foreign transcript files.
func TestQoderStopFlow(t *testing.T) {
	session := fmt.Sprintf("../../qoder-%d", time.Now().UnixNano())
	handler, notify, _ := newTestHandler(t, codexTestConfig())
	handler.product = ProductQoder
	handler.source = QoderSource{}
	payload := func(id string, active bool) string {
		return fmt.Sprintf(`{"session_id":%q,"cwd":"/TEST-qoder","hook_event_name":"Stop","transcript_path":"/must-not-read","stop_hook_active":%t,"last_assistant_message":"Done"}`, id, active)
	}
	if err := handler.HandleHook("Stop", strings.NewReader(payload(session, true))); err != nil {
		t.Fatal(err)
	}
	if notify.wasCalled() {
		t.Fatal("continuation notified")
	}
	if err := handler.HandleHook("Stop", strings.NewReader(payload(session, false))); err != nil {
		t.Fatal(err)
	}
	call := notify.lastCall()
	if call == nil || call.status != analyzer.StatusTaskComplete || !strings.Contains(call.message, "Done") {
		t.Fatalf("missing completion: %+v", call)
	}
	before := len(notify.calls)
	if err := handler.HandleHook("Stop", strings.NewReader(payload(session, false))); err != nil {
		t.Fatal(err)
	}
	if len(notify.calls) != before {
		t.Fatal("duplicate notified")
	}
	ev, err := (QoderSource{}).Decode(context.Background(), "Stop", strings.NewReader(payload(session, false)))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Session.TranscriptPath != "" || strings.Contains(qoderKeys(ev).stateKey, "../") || qoderKeys(ev).stateKey == codexKeys(ev).stateKey {
		t.Fatal("foreign transcript or unsafe/cross-product identity")
	}
	if err := handler.HandleHook("Stop", strings.NewReader(payload(session+"-other", false))); err != nil {
		t.Fatal(err)
	}
	if len(notify.calls) != before+1 {
		t.Fatal("another session suppressed")
	}
}

func TestQoderDecodeRejectsUnsupportedAndOversized(t *testing.T) {
	for _, tc := range []struct{ event, input string }{
		{"PermissionRequest", `{"session_id":"s","hook_event_name":"PermissionRequest"}`},
		{"Stop", `{"session_id":"s","hook_event_name":"UserPromptSubmit"}`},
		{"Stop", `{"hook_event_name":"Stop"}`},
		{"Stop", strings.Repeat("x", qodersource.MaxPayloadBytes+1)},
	} {
		if _, err := (QoderSource{}).Decode(context.Background(), tc.event, strings.NewReader(tc.input)); err == nil {
			t.Fatalf("accepted invalid payload event %s", tc.event)
		}
	}
}
