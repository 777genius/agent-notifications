package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier/nativeprotocol"
)

const pr3Correlation = "00000000-0000-4000-8000-000000000001"
const pr3Nonce = "00000000-0000-4000-8000-000000000002"
const pr3Caps = `{"schemaVersion":1,"protocolVersions":[1],"actionKinds":["none","desktop_thread_v1","future_action"],"receiptSupport":true,"backend":"macos.usernotifications","explicitFeatureEnabledByDefault":false}`

type pr3Clock struct {
	mu  sync.Mutex
	now float64
}

func (c *pr3Clock) Now() (string, float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return "test-boot", c.now, nil
}
func (c *pr3Clock) set(n float64) { c.mu.Lock(); defer c.mu.Unlock(); c.now = n }

type pr3Lease struct{ released *bool }

func (pr3Lease) BundlePath() string { return "/managed/Notifier.app" }
func (pr3Lease) ExecutablePath() string {
	return "/managed/Notifier.app/Contents/MacOS/terminal-notifier-modern"
}
func (l pr3Lease) Release() { *l.released = true }

type pr3Install struct {
	acquire func(context.Context) (NativeLease, error)
}

func (i pr3Install) Acquire(c context.Context) (NativeLease, error) { return i.acquire(c) }

type pr3Process struct {
	probe  func(context.Context, string) ([]byte, error)
	launch func(context.Context, string, string, string) (bool, error)
}

func (p pr3Process) Probe(c context.Context, e string) ([]byte, error) { return p.probe(c, e) }
func (p pr3Process) Launch(c context.Context, b, r, a string) (bool, error) {
	return p.launch(c, b, r, a)
}

type pr3Spool struct {
	prepare func(context.Context, notification.Request, func(string) ([]byte, error)) (NativeAttempt, error)
	read    func(NativeAttempt) ([]byte, error)
	expired int
}

func (s *pr3Spool) Prepare(c context.Context, r notification.Request, e func(string) ([]byte, error)) (NativeAttempt, error) {
	return s.prepare(c, r, e)
}
func (s *pr3Spool) Receipt(a NativeAttempt) ([]byte, error) { return s.read(a) }
func (s *pr3Spool) Expire(NativeAttempt) error              { s.expired++; return nil }
func pr3Request() notification.Request {
	return notification.Request{Content: notification.Content{Title: "--help", Body: "[important]\n-execute 👩‍💻", Subtitle: "-execute", Category: "info"}, CorrelationID: pr3Correlation, Deadline: notification.Deadline{BootID: "test-boot", NotAfter: 110}, Policy: notification.PolicySnapshot{Valid: true, ExplicitEnabled: true, DesktopEnabled: true, ClickToFocus: true, SoundEnabled: true}, Target: notification.DesktopTarget{ThreadID: "opaque/?#👩‍💻", ApplicationPath: "/disposable/Codex.app", TeamID: "TESTTEAM01"}}
}
func pr3Receipt(status, reason string) []byte {
	b, _ := json.Marshal(nativeprotocol.Receipt{SchemaVersion: 1, CorrelationID: pr3Correlation, Nonce: pr3Nonce, NotificationID: pr3Correlation, Status: status, Reason: reason})
	return b
}

type pr3Harness struct {
	delivery                   *StructuredDelivery
	clock                      *pr3Clock
	spool                      *pr3Spool
	probes, launches, prepares int
	released                   bool
	wire                       []byte
	reply                      []byte
}

func newPR3Harness(t *testing.T) *pr3Harness {
	t.Helper()
	h := &pr3Harness{clock: &pr3Clock{now: 100}, reply: pr3Receipt("submitted", "os_accepted")}
	h.spool = &pr3Spool{prepare: func(_ context.Context, _ notification.Request, encode func(string) ([]byte, error)) (NativeAttempt, error) {
		h.prepares++
		var err error
		h.wire, err = encode(pr3Nonce)
		return NativeAttempt{Nonce: pr3Nonce, RequestPath: "/private/request", ReceiptPath: "/private/receipt"}, err
	}, read: func(NativeAttempt) ([]byte, error) { return h.reply, nil }}
	h.delivery = &StructuredDelivery{Clock: h.clock, Spool: h.spool, Installation: pr3Install{func(context.Context) (NativeLease, error) { return pr3Lease{&h.released}, nil }}, Process: pr3Process{probe: func(context.Context, string) ([]byte, error) { h.probes++; return []byte(pr3Caps), nil }, launch: func(context.Context, string, string, string) (bool, error) { h.launches++; return true, nil }}}
	return h
}
func TestPR3DeliveryLiteralReceiptAndSnapshot(t *testing.T) {
	h := newPR3Harness(t)
	r := pr3Request()
	r.Policy.SoundEnabled = false
	got := h.delivery.Deliver(context.Background(), r)
	if got.Status != "submitted" || got.Reason != "os_accepted" || got.RetrySafe || got.Navigation.Precision != "chat_id" || h.launches != 1 || !h.released || h.spool.expired != 0 {
		t.Fatalf("unexpected receipt/lifetime %+v", got)
	}
	var wire struct {
		Title, Body, Subtitle string
		Silent                bool
		Action                nativeprotocol.DesktopThreadAction
		NotAfter              float64
	}
	if err := json.Unmarshal(h.wire, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Title != r.Content.Title || wire.Body != r.Content.Body || wire.Subtitle != r.Content.Subtitle || !wire.Silent || wire.NotAfter != 110 || wire.Action.ThreadID != r.Target.ThreadID {
		t.Fatal("literal/snapshot/deadline changed")
	}
}
func TestPR3NoEffectsBeforeAdmission(t *testing.T) {
	tests := []struct {
		name           string
		change         func(*notification.Request)
		status, reason string
	}{
		{"invalid", func(r *notification.Request) { r.Content.Body = "\x1b" }, "rejected", "malformed_request"},
		{"policy", func(r *notification.Request) { r.Policy.Valid = false }, "rejected", "configuration_invalid"},
		{"explicit disabled", func(r *notification.Request) { r.Policy.ExplicitEnabled = false }, "suppressed", "disabled"},
		{"desktop disabled", func(r *notification.Request) { r.Policy.DesktopEnabled = false }, "suppressed", "disabled"},
		{"click disabled default required", func(r *notification.Request) { r.Policy.ClickToFocus = false }, "rejected", "navigation_disabled"},
		{"missing required target", func(r *notification.Request) { r.Target = notification.DesktopTarget{} }, "rejected", "navigation_unavailable"},
		{"expired", func(r *notification.Request) { r.Deadline.NotAfter = 99 }, "rejected", "expired"},
		{"fresh budget forbidden", func(r *notification.Request) { r.Deadline.NotAfter = 116 }, "rejected", "expired"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newPR3Harness(t)
			r := pr3Request()
			tt.change(&r)
			got := h.delivery.Deliver(context.Background(), r)
			if got.Status != tt.status || got.Reason != tt.reason || h.probes+h.launches+h.prepares != 0 {
				t.Fatalf("effects before admission %+v", got)
			}
		})
	}
}
func TestPR3NavigationSelectionAndSoundOptOut(t *testing.T) {
	for _, nav := range []notification.Navigation{notification.None, notification.BestEffort} {
		h := newPR3Harness(t)
		r := pr3Request()
		r.Navigation = nav
		r.Policy.ClickToFocus = false
		r.Silent = true
		got := h.delivery.Deliver(context.Background(), r)
		var wire struct {
			Action string
			Silent bool
		}
		json.Unmarshal(h.wire, &wire)
		if got.Status != "submitted" || got.Navigation.Capability != "disabled" || wire.Action != "none" || !wire.Silent {
			t.Fatalf("opt-out overridden %+v", got)
		}
	}
	h := newPR3Harness(t)
	p := h.delivery.Process.(pr3Process)
	p.probe = func(context.Context, string) ([]byte, error) {
		return []byte(strings.Replace(pr3Caps, `,"desktop_thread_v1"`, "", 1)), nil
	}
	h.delivery.Process = p
	if got := h.delivery.Deliver(context.Background(), pr3Request()); got.Reason != "unsupported_notifier" || h.launches != 0 {
		t.Fatal("required navigation downgraded")
	}
}
func TestPR3UnknownNeverRetriesOrFallsBack(t *testing.T) {
	for _, reply := range [][]byte{nil, []byte("bad"), []byte(strings.Replace(string(pr3Receipt("submitted", "os_accepted")), pr3Nonce, pr3Correlation, 1)), pr3Receipt("unknown", "timeout")} {
		h := newPR3Harness(t)
		h.reply = reply
		p := h.delivery.Process.(pr3Process)
		p.launch = func(context.Context, string, string, string) (bool, error) {
			h.launches++
			h.clock.set(111)
			return true, nil
		}
		h.delivery.Process = p
		got := h.delivery.Deliver(context.Background(), pr3Request())
		if got.Status != "unknown" || got.RetrySafe || h.launches != 1 {
			t.Fatalf("unknown became retry/success %+v", got)
		}
	}
	for _, reason := range []string{"permission_denied", "activation_required", "os_rejected"} {
		h := newPR3Harness(t)
		h.reply = pr3Receipt("rejected", reason)
		got := h.delivery.Deliver(context.Background(), pr3Request())
		if got.Status != "rejected" || got.Reason != reason || h.launches != 1 {
			t.Fatal("native rejection lost")
		}
	}
}
func TestPR3DeadlineAcrossLockProbeAndSpool(t *testing.T) {
	for _, stage := range []string{"lock", "probe", "spool"} {
		t.Run(stage, func(t *testing.T) {
			h := newPR3Harness(t)
			switch stage {
			case "lock":
				h.delivery.Installation = pr3Install{func(context.Context) (NativeLease, error) { h.clock.set(111); return pr3Lease{&h.released}, nil }}
			case "probe":
				p := h.delivery.Process.(pr3Process)
				old := p.probe
				p.probe = func(c context.Context, e string) ([]byte, error) { h.clock.set(111); return old(c, e) }
				h.delivery.Process = p
			case "spool":
				old := h.spool.prepare
				h.spool.prepare = func(c context.Context, r notification.Request, e func(string) ([]byte, error)) (NativeAttempt, error) {
					h.clock.set(111)
					return old(c, r, e)
				}
			}
			got := h.delivery.Deliver(context.Background(), pr3Request())
			if got.Status != "rejected" || got.Reason != "expired" || h.launches != 0 {
				t.Fatalf("late handoff %+v", got)
			}
		})
	}
}
func TestPR3CancelAfterHandoffRetainsAttempt(t *testing.T) {
	h := newPR3Harness(t)
	h.reply = nil
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := h.delivery.Process.(pr3Process)
	p.launch = func(context.Context, string, string, string) (bool, error) {
		h.launches++
		cancel()
		return true, errors.New("lost launcher")
	}
	h.delivery.Process = p
	got := h.delivery.Deliver(ctx, pr3Request())
	if got.Status != "unknown" || h.spool.expired != 0 || h.launches != 1 {
		t.Fatal("cancel incorrectly safe-retried/removed")
	}
}
func TestPR3OldUnknownInstallationNeverExecuted(t *testing.T) {
	h := newPR3Harness(t)
	h.delivery.Installation = pr3Install{func(context.Context) (NativeLease, error) { return nil, errors.New("unknown offline fingerprint") }}
	got := h.delivery.Deliver(context.Background(), pr3Request())
	if got.Reason != "unsupported_notifier" || h.probes+h.launches+h.prepares != 0 {
		t.Fatal("unknown helper executed")
	}
}
func TestPR3SuspendCancelsBlockedProbe(t *testing.T) {
	h := newPR3Harness(t)
	p := h.delivery.Process.(pr3Process)
	p.probe = func(c context.Context, _ string) ([]byte, error) {
		h.clock.set(111)
		select {
		case <-c.Done():
			return nil, c.Err()
		case <-time.After(time.Second):
			t.Error("continuous timeout not propagated")
			return nil, nil
		}
	}
	h.delivery.Process = p
	got := h.delivery.Deliver(context.Background(), pr3Request())
	if got.Reason != "expired" || h.launches != 0 {
		t.Fatal("suspend allowed late handoff")
	}
}
func TestPR3LegacyPresentationCharacterization(t *testing.T) {
	got := legacyPresentation(analyzer.StatusPermissionRequest, "[session|main folder] body", "Permission", true)
	if got.Title != "Permission [session]" || got.Subtitle != "main · folder" || got.Body != "body" || !got.TimeSensitive {
		t.Fatalf("legacy presentation changed %+v", got)
	}
	got = legacyPresentation(analyzer.StatusTaskComplete, "[important] body", "Done", false)
	// Characterize the intentional legacy bracket extraction. The explicit
	// request test above independently proves it does not use this parser.
	_, branch, body := extractSessionInfo("[important] body")
	if got.Title != "Done" || got.Body != body || got.Subtitle != branch || got.TimeSensitive {
		t.Fatalf("legacy wrapper changed %+v", got)
	}
}

func TestPR3PrivateSpoolPhysicalPathsAndCleanup(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("private spool enabled on qualified unix hosts only")
	}
	root := t.TempDir()
	os.Chmod(root, 0700)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	clock := &pr3Clock{now: 100}
	spool := &PrivateNativeSpool{Root: alias, Clock: clock}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	encode := func(n string) ([]byte, error) { return []byte(`{"nonce":"` + n + `"}`), nil }
	attempt, err := spool.Prepare(ctx, pr3Request(), encode)
	if err != nil {
		t.Fatal(err)
	}
	physical, _ := filepath.EvalSymlinks(root)
	if filepath.Dir(attempt.Directory) != physical || !isUUID(attempt.Nonce) || attempt.Nonce == pr3Nonce {
		t.Fatal("nonphysical/nonrandom attempt")
	}
	for p, mode := range map[string]os.FileMode{attempt.Directory: 0700, attempt.RequestPath: 0600} {
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatal("private mode lost")
		}
	}
	if _, err = spool.Prepare(ctx, pr3Request(), encode); err == nil {
		t.Fatal("double launch attempt admitted")
	}
	if err = spool.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(attempt.RequestPath); err != nil {
		t.Fatal("live attempt removed")
	}
	// Simulate native consumption and a terminal receipt: request body disappears,
	// while the live receipt/claim is retained until original deadline.
	if err = os.Remove(attempt.RequestPath); err != nil {
		t.Fatal(err)
	}
	if err = writePrivate(attempt.ReceiptPath, pr3Receipt("submitted", "os_accepted")); err != nil {
		t.Fatal(err)
	}
	if _, err = spool.Receipt(attempt); err != nil {
		t.Fatal(err)
	}
	clock.set(111)
	if err = spool.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(attempt.Directory); !os.IsNotExist(err) {
		t.Fatal("expired orphan remains")
	}
	if _, err = os.Open(attempt.RequestPath); !os.IsNotExist(err) {
		t.Fatal("late native reader sees request")
	}
}
func TestPR3SpoolReclaimsInterruptedPublication(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("private spool enabled on qualified unix hosts only")
	}
	root := t.TempDir()
	os.Chmod(root, 0700)
	s := &PrivateNativeSpool{Root: root, Clock: &pr3Clock{now: 100}}
	for _, prefix := range []string{".prepare.", ".expired."} {
		for _, partial := range []bool{false, true} {
			stage := filepath.Join(root, prefix+pr3Correlation+"."+pr3Nonce)
			if err := os.Mkdir(stage, 0700); err != nil {
				t.Fatal(err)
			}
			if partial {
				if err := writePrivate(filepath.Join(stage, "owner.json"), []byte("{")); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			err := s.Cleanup(ctx)
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(stage); !os.IsNotExist(err) {
				t.Fatal("interrupted publication poisons spool")
			}
		}
	}
}
func TestPR3SpoolRejectsUnsafeReceiptAndFullRoot(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("private spool enabled on qualified unix hosts only")
	}
	root := t.TempDir()
	os.Chmod(root, 0700)
	s := &PrivateNativeSpool{Root: root, Clock: &pr3Clock{now: 100}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	a, err := s.Prepare(ctx, pr3Request(), func(string) ([]byte, error) { return []byte("{}"), nil })
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(t.TempDir(), "receipt")
	os.WriteFile(foreign, pr3Receipt("submitted", "os_accepted"), 0600)
	if err = os.Symlink(foreign, a.ReceiptPath); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Receipt(a); err == nil {
		t.Fatal("symlink receipt accepted")
	}
	os.Remove(a.ReceiptPath)
	os.WriteFile(a.ReceiptPath, make([]byte, nativeprotocol.MaxEnvelopeBytes+1), 0600)
	if _, err = s.Receipt(a); err == nil {
		t.Fatal("oversized receipt accepted")
	}
}

func TestPR3OwnedCommandsAndDeletedWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	request := filepath.Join(dir, "request")
	receipt := filepath.Join(dir, "receipt")
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	cmd := nativeLaunchCommand(context.Background(), "/managed/ClaudeNotifier.app", request, receipt)
	expected := []string{"/usr/bin/open", "-n", "-a", "/managed/ClaudeNotifier.app", "--args", "--send-json", "--request-file", request, "--receipt-file", receipt, "-launchedViaLaunchServices"}
	if !reflect.DeepEqual(cmd.Args, expected) || cmd.Dir != "/" || cmd.Stdout != nil || cmd.Stderr != nil {
		t.Fatal("launcher depends on cwd/output or changed native grammar")
	}
	probe := nativeProbeCommand(context.Background(), "/managed/ClaudeNotifier.app/Contents/MacOS/terminal-notifier-modern")
	if len(probe.Args) != 2 || probe.Args[1] != "--capabilities-json" || probe.Dir != "/" {
		t.Fatal("probe uses legacy discovery")
	}
	var output nativeOutput
	if _, err := output.Write(make([]byte, nativeprotocol.MaxEnvelopeBytes)); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte{1}); err == nil || len(output.data) != nativeprotocol.MaxEnvelopeBytes {
		t.Fatal("probe output unbounded")
	}
}

func TestPR3ConstructionDoesNotCreateStatusState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	delivery := NewStructuredDelivery(ManagedInstallation{}, root)
	if delivery == nil {
		t.Fatal("missing adapter")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("construction mutated read-only state")
	}
}

func TestPR3LegacyStatusAndSessionLabelMatrix(t *testing.T) {
	for _, tc := range []struct {
		status    analyzer.Status
		sensitive bool
	}{
		{analyzer.StatusTaskComplete, false}, {analyzer.StatusReviewComplete, false},
		{analyzer.StatusQuestion, false}, {analyzer.StatusPlanReady, false},
		{analyzer.StatusSessionLimitReached, true}, {analyzer.StatusAPIError, true},
		{analyzer.StatusAPIErrorOverloaded, true}, {analyzer.StatusPermissionRequest, true},
	} {
		for _, label := range []bool{false, true} {
			got := legacyPresentation(tc.status, "[session|branch folder] exact body", "Custom status title", label)
			title := "Custom status title"
			if label {
				title += " [session]"
			}
			if got.Title != title || got.Subtitle != "branch · folder" || got.Body != "exact body" || got.TimeSensitive != tc.sensitive {
				t.Fatalf("legacy status presentation changed: %s %+v", tc.status, got)
			}
		}
	}
}
