//go:build linux

package daemon

import (
	"errors"
	"testing"

	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/esiqveland/notify"
)

// replaceNotifier follows the freedesktop spec: a replacement returns the
// replaced id, and a new notification gets the next counter value.
type replaceNotifier struct {
	notify.Notifier
	next uint32
	sent []notify.Notification
	fail bool
}

func (r *replaceNotifier) SendNotification(n notify.Notification) (uint32, error) {
	r.sent = append(r.sent, n)
	if r.fail {
		return 0, errors.New("notification server unavailable")
	}
	if n.ReplacesID != 0 {
		return n.ReplacesID, nil
	}
	r.next++
	return r.next, nil
}

func newReplaceServer(n *replaceNotifier) *Server {
	return &Server{notifier: n, focusCtx: make(map[uint32]FocusHints)}
}

func sendReplaceable(t *testing.T, s *Server, key string, noFocus bool) uint32 {
	t.Helper()
	resp, err := s.handleNotification(&NotifyRequest{Title: "t", Body: "b", FocusTarget: "kitty", ReplaceKey: key, NoFocus: noFocus}, false)
	if err != nil {
		t.Fatal(err)
	}
	return resp.NotificationID
}

// A session's next notification replaces its previous one; other sessions
// and notifications without a session id stay separate.
func TestReplaceKeyReplacesOnlyTheSameSession(t *testing.T) {
	n := &replaceNotifier{}
	s := newReplaceServer(n)

	a1 := sendReplaceable(t, s, "session-a", false)
	b1 := sendReplaceable(t, s, "session-b", false)
	a2 := sendReplaceable(t, s, "session-a", false)
	u1 := sendReplaceable(t, s, "", false)
	u2 := sendReplaceable(t, s, "", false)

	if a2 != a1 {
		t.Errorf("second session-a id = %d, want replaced id %d", a2, a1)
	}
	if b1 == a1 {
		t.Errorf("session-b shares session-a's id %d", a1)
	}
	if u1 == u2 || u1 == a1 || u2 == a1 {
		t.Errorf("keyless notifications must stay unique: %d, %d (session-a %d)", u1, u2, a1)
	}
	want := []uint32{0, 0, a1, 0, 0}
	for i, sent := range n.sent {
		if sent.ReplacesID != want[i] {
			t.Errorf("notification %d ReplacesID = %d, want %d", i, sent.ReplacesID, want[i])
		}
	}
}

// A closed notification cannot be replaced: the session's next one is new.
func TestReplaceKeyForgottenWhenNotificationCloses(t *testing.T) {
	n := &replaceNotifier{}
	s := newReplaceServer(n)

	first := sendReplaceable(t, s, "session-a", false)
	other := sendReplaceable(t, s, "session-b", false)
	s.onNotificationClosed(&notify.NotificationClosedSignal{ID: first, Reason: notify.ReasonDismissedByUser})
	second := sendReplaceable(t, s, "session-a", false)
	third := sendReplaceable(t, s, "session-b", false)

	if second == first || n.sent[2].ReplacesID != 0 {
		t.Errorf("after close: id %d ReplacesID %d, want a new notification", second, n.sent[2].ReplacesID)
	}
	if third != other || n.sent[3].ReplacesID != other {
		t.Errorf("closing session-a's notification must not affect session-b: got %d replacing %d, want %d", third, n.sent[3].ReplacesID, other)
	}
}

// A restarted daemon has no ids, so the next notification is new.
func TestReplaceKeyLostOnDaemonRestart(t *testing.T) {
	n := &replaceNotifier{}
	sendReplaceable(t, newReplaceServer(n), "session-a", false)
	sendReplaceable(t, newReplaceServer(n), "session-a", false)
	if n.sent[1].ReplacesID != 0 {
		t.Errorf("ReplacesID after restart = %d, want 0", n.sent[1].ReplacesID)
	}
}

// A failed send leaves no stale id behind.
func TestReplaceKeyForgottenWhenSendFails(t *testing.T) {
	n := &replaceNotifier{}
	s := newReplaceServer(n)
	sendReplaceable(t, s, "session-a", false)
	n.fail = true
	if _, err := s.handleNotification(&NotifyRequest{Title: "t", ReplaceKey: "session-a"}, false); err == nil {
		t.Fatal("want send error")
	}
	n.fail = false
	sendReplaceable(t, s, "session-a", false)
	if got := n.sent[2].ReplacesID; got != 0 {
		t.Errorf("ReplacesID after failed send = %d, want 0", got)
	}
}

// Without focus the notification has no click action, and replacing a
// focusable one drops the old click target.
func TestNoFocusSendsNoActionAndDropsReplacedFocusContext(t *testing.T) {
	n := &replaceNotifier{}
	s := newReplaceServer(n)

	id := sendReplaceable(t, s, "session-a", false)
	if len(n.sent[0].Actions) != 1 {
		t.Fatalf("focusable notification actions = %v, want the focus action", n.sent[0].Actions)
	}
	if _, ok := s.focusCtx[id]; !ok {
		t.Fatal("focusable notification has no focus context")
	}
	if replaced := sendReplaceable(t, s, "session-a", true); replaced != id {
		t.Fatalf("replaced id = %d, want %d", replaced, id)
	}
	if len(n.sent[1].Actions) != 0 {
		t.Errorf("no-focus notification actions = %v, want none", n.sent[1].Actions)
	}
	if _, ok := s.focusCtx[id]; ok {
		t.Error("replaced notification kept its click target")
	}
}

// Strict notifications are never replaced or recorded for replacement.
func TestStrictNotificationIgnoresReplaceKey(t *testing.T) {
	n := &replaceNotifier{}
	s := newReplaceServer(n)
	sendReplaceable(t, s, "session-a", false)
	if _, err := s.handleNotification(&NotifyRequest{Title: "t", ReplaceKey: "session-a", Strict: &notification.TerminalTarget{}}, true); err != nil {
		t.Fatal(err)
	}
	if got := n.sent[1].ReplacesID; got != 0 {
		t.Errorf("strict ReplacesID = %d, want 0", got)
	}
	if got := s.replaceID("session-a"); got != 1 {
		t.Errorf("session-a live id = %d, want 1 (unchanged by strict)", got)
	}
}
