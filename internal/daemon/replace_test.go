//go:build linux

package daemon

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

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

// The desktop notification service may restart independently of our daemon.
// Cached IDs from its previous owner must not overwrite another session.
func TestReplacementIDsInvalidateWhenServiceOwnerChanges(t *testing.T) {
	n := &replaceNotifier{}
	s := newReplaceServer(n)
	owner := ":1.100"
	s.replaceOwnerLookup = func(context.Context) (string, bool) { return owner, true }
	a1 := sendReplaceable(t, s, "session-a", false)
	owner = ":1.101"
	n.next = 0 // restarted service reuses its counter
	b1 := sendReplaceable(t, s, "session-b", false)
	a2 := sendReplaceable(t, s, "session-a", false)
	if a1 != 1 || b1 != 1 || a2 != 2 {
		t.Fatalf("IDs after restart = %d, %d, %d; want 1, 1, 2", a1, b1, a2)
	}
	if n.sent[1].ReplacesID != 0 || n.sent[2].ReplacesID != 0 {
		t.Fatalf("stale replacement IDs after restart: b=%d a=%d", n.sent[1].ReplacesID, n.sent[2].ReplacesID)
	}
}

func TestReplacementIDsInvalidateWhenServiceOwnerUnknown(t *testing.T) {
	n := &replaceNotifier{}
	s := newReplaceServer(n)
	owner := ":1.100"
	valid := true
	s.replaceOwnerLookup = func(context.Context) (string, bool) { return owner, valid }
	sendReplaceable(t, s, "session-a", false)
	valid = false
	sendReplaceable(t, s, "session-a", false)
	sendReplaceable(t, s, "session-a", false)
	if n.sent[1].ReplacesID != 0 || n.sent[2].ReplacesID != 0 {
		t.Fatalf("unverified owner reused IDs: %d, %d", n.sent[1].ReplacesID, n.sent[2].ReplacesID)
	}
}

// The first replacement-owner check must not invalidate a strict click
// captured earlier against that same live D-Bus owner. Restarted services and
// unknown owners still invalidate obsolete or unverifiable focus targets.
func TestReplaceOwnerPreservesOnlyCurrentVerifiedStrictTargets(t *testing.T) {
	s := newReplaceServer(&replaceNotifier{})
	owner := ":1.100"
	s.replaceOwnerLookup = func(context.Context) (string, bool) { return owner, true }
	s.focusCtx[1] = FocusHints{Strict: &notification.TerminalTarget{}, StrictOwner: owner}
	s.focusCtx[2] = FocusHints{Strict: &notification.TerminalTarget{}, StrictOwner: ":1.99"}
	s.focusCtx[3] = FocusHints{TerminalName: "kitty"}

	s.refreshReplaceOwner() // Initial lookup, no previous replaceOwner.
	if _, ok := s.focusCtx[1]; !ok {
		t.Fatal("first lookup deleted strict focus authorized for current owner")
	}
	if _, ok := s.focusCtx[2]; ok {
		t.Fatal("first lookup retained strict focus from a different owner")
	}
	if _, ok := s.focusCtx[3]; ok {
		t.Fatal("first lookup retained unverifiable legacy focus target")
	}

	s.rememberReplaceID("session-a", 7)
	owner = ":1.101" // Notification server restarted and may recycle IDs.
	s.focusCtx[4] = FocusHints{Strict: &notification.TerminalTarget{}, StrictOwner: owner}
	s.refreshReplaceOwner()
	if _, ok := s.focusCtx[1]; ok {
		t.Fatal("owner change retained obsolete strict focus target")
	}
	if _, ok := s.focusCtx[4]; !ok {
		t.Fatal("owner change deleted already-verified new-owner strict focus")
	}
	if id := s.replaceID("session-a"); id != 0 {
		t.Fatalf("owner change retained stale replace ID %d", id)
	}

	s.replaceOwnerLookup = func(context.Context) (string, bool) { return "", false }
	s.refreshReplaceOwner()
	if _, ok := s.focusCtx[4]; ok {
		t.Fatal("unknown service owner retained strict focus")
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

// gatedNotifier blocks the first send until released, to overlap a second
// request of the same session with it.
type gatedNotifier struct {
	notify.Notifier
	mu      sync.Mutex
	sent    []notify.Notification
	entered chan struct{}
	release chan struct{}
}

func (g *gatedNotifier) SendNotification(n notify.Notification) (uint32, error) {
	g.mu.Lock()
	g.sent = append(g.sent, n)
	first := len(g.sent) == 1
	g.mu.Unlock()
	if first {
		close(g.entered)
		<-g.release
		return 7, nil
	}
	if n.ReplacesID != 0 {
		return n.ReplacesID, nil
	}
	return 8, nil
}

// Two concurrent first notifications of one session must not both be new:
// the second waits for the first and replaces it.
func TestReplaceKeyConcurrentSendsShareOneNotification(t *testing.T) {
	g := &gatedNotifier{entered: make(chan struct{}), release: make(chan struct{})}
	s := &Server{notifier: g, focusCtx: make(map[uint32]FocusHints)}
	ids := make(chan uint32, 2)
	send := func() {
		resp, err := s.handleNotification(&NotifyRequest{Title: "t", ReplaceKey: "session-a"}, false)
		if err != nil {
			t.Error(err)
			ids <- 0
			return
		}
		ids <- resp.NotificationID
	}
	go send()
	<-g.entered
	go send()
	// Give the second request time to reach the send if it were not held.
	time.Sleep(100 * time.Millisecond)
	g.mu.Lock()
	overlapped := len(g.sent)
	g.mu.Unlock()
	close(g.release)
	first, second := <-ids, <-ids
	if overlapped != 1 {
		t.Fatalf("second send started while the first was in flight (%d sends)", overlapped)
	}
	if first != 7 || second != 7 || g.sent[1].ReplacesID != 7 {
		t.Fatalf("ids %d, %d; second ReplacesID %d; want both 7", first, second, g.sent[1].ReplacesID)
	}
}

// The ping reply tells a hook that this daemon honours no_focus.
func TestPingAdvertisesNoFocus(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "d.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	server := &Server{startTime: time.Now()}
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		server.wg.Add(1)
		server.handleConnection(conn)
	}()
	ping, err := (&Client{socketPath: socket}).Ping()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(ping.Capabilities, CapabilityNoFocus) {
		t.Fatalf("capabilities = %v, want %q", ping.Capabilities, CapabilityNoFocus)
	}
}
