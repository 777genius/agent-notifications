package copilotvscodeinstall

import (
	"context"
	"testing"

	"github.com/777genius/agent-notifications/internal/notification"
)

type testTerminalSink struct{ count int }

func (s *testTerminalSink) Deliver(_ context.Context, r notification.Request) notification.Receipt {
	s.count++
	return notification.Receipt{CorrelationID: r.CorrelationID, Status: "submitted"}
}

func TestCursorTerminalRouteCannotEscapeLocalDesktopGuard(t *testing.T) {
	sink := &testTerminalSink{}
	ctx := context.Background()
	base := notification.InformationalDesktopRequest(notification.Content{Title: "Cursor CLI", Body: "Stopping", Category: "info"}, "test", notification.Deadline{}, false)
	origin := &notification.TerminalTarget{Agent: "cursor", WindowID: "321"}
	r := notification.WithTerminalClick(base, origin, true)
	if got := (localDesktop{backend: sink}).Deliver(ctx, r); got.Status != "rejected" {
		t.Fatal("Copilot must never inherit Cursor terminal navigation")
	}
	if sink.count != 0 {
		t.Fatal("unqualified request reached OS effect port")
	}
	p := localDesktop{backend: sink, allowTerminal: true}
	if got := p.Deliver(ctx, r); got.Status != "submitted" || sink.count != 1 {
		t.Fatal("qualified Cursor route not accepted", got)
	}
	r.Terminal.Agent = "gemini"
	if got := p.Deliver(ctx, r); got.Status != "rejected" || sink.count != 1 {
		t.Fatal("foreign agent was able to route through Cursor", got)
	}
	r.Terminal.Agent = "cursor"
	r.Target.ThreadID = "untrusted-thread"
	if got := p.Deliver(ctx, r); got.Status != "rejected" || sink.count != 1 {
		t.Fatal("Codex Desktop thread ID reached Cursor port", got)
	}
	if got := p.Deliver(ctx, base); got.Status != "submitted" || sink.count != 2 {
		t.Fatal("informational Cursor behavior regressed", got)
	}
}
