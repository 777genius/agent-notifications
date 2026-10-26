package installprogress

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSessionReplayPreservesSnapshotTruthAndTransitionIdentity(t *testing.T) {
	// Snapshot insertion and restart must not create duplicate installation facts.
	s := session{history: map[string][]event{}, last: map[string]event{}, lost: map[string]bool{}}
	for _, line := range []string{
		"snapshot\tclaude\tpending\tnot_started",
		"transition\tclaude\tregistration\tregistration_committed",
		"snapshot\tclaude\tMCP\tactivation_timeout",
		"transition\tclaude\tregistration\tregistration_committed",
		"snapshot\tclaude\tMCP\tactivation_timeout",
	} {
		kind, e, _, err := parseSession(line)
		if err != nil {
			t.Fatal(err)
		}
		s.apply(kind, e)
	}
	if len(s.history["claude"]) != 1 || s.rows.rows[0].outcome != "activation_timeout" {
		t.Fatalf("lost snapshot/action truth: %+v", s)
	}
	for i := 0; i < 40; i++ {
		outcome := "running"
		if i%2 == 0 {
			outcome = "failed"
		}
		s.apply("transition", event{"claude", "config", outcome})
	}
	if len(s.history["claude"]) != 16 || !s.lost["claude"] {
		t.Fatal("history is unbounded or lost-history disclosure missing")
	}
}

func TestSessionStartupFoldsOneFrameAndPlainReplayStaysQuiet(t *testing.T) {
	content := "snapshot\tclaude\tpending\tnot_started\ntransition\tclaude\tregistration\tregistration_committed\ntransition\tclaude\tconfig\tregistration_committed\nsnapshot\tclaude\tMCP\tactivation_timeout\nstop\t\t\t\n"
	var out bytes.Buffer
	if err := RunSession(context.Background(), eventFile(t, content), Options{Output: &out, Rich: true, Width: 80, Height: 24}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "Agent Notifications") != 1 || !strings.Contains(out.String(), "Registration saved · Configuration phase") || !strings.Contains(out.String(), "Timed out") || strings.Contains(out.String(), "Installed") {
		t.Fatalf("replayed intermediate frames or invented success: %q", out.String())
	}
	out.Reset()
	stop := eventFile(t, "stop\n")
	if err := RunSession(context.Background(), eventFile(t, strings.TrimSuffix(content, "stop\t\t\t\n")), Options{Output: &out, StopFile: stop, Transient: true}); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("plain replay duplicated actions: %q", out.String())
	}
}

func TestSessionStoppedSmallTerminalEmitsOnePlainFinalSnapshot(t *testing.T) {
	journal := "snapshot\tcodex\tcomplete\tcompleted\ntransition\tglobal\tnotification-tools\trunning\nstop\t\t\t\n"
	for _, geometry := range [][2]int{{23, 24}, {80, 2}} {
		var out bytes.Buffer
		if err := RunSession(context.Background(), eventFile(t, journal), Options{Output: &out, Rich: true, Width: geometry[0], Height: geometry[1]}); err != nil {
			t.Fatal(err)
		}
		if strings.Count(out.String(), "Codex CLI: complete - done\n") != 1 || strings.Contains(out.String(), "Installer:") {
			t.Fatalf("small-terminal final snapshot repeated or showed a stopped stage: %q", out.String())
		}
	}
}

func TestSessionLiveSmallTerminalShowsCurrentStageAndPlainReplayStaysQuiet(t *testing.T) {
	journal := "transition\tglobal\tpreparation\trunning\ntransition\tglobal\tpreparation\tdone\ntransition\tglobal\tnotification-tools\trunning\n"
	for _, fixture := range []struct {
		name          string
		width, height int
		rich          bool
	}{{"narrow", 23, 24, true}, {"short", 80, 2, true}, {"plain", 23, 24, false}} {
		t.Run(fixture.name, func(t *testing.T) {
			var out bytes.Buffer
			options := Options{Output: &out, Rich: fixture.rich, Width: fixture.width, Height: fixture.height, Transient: true, StopFile: eventFile(t, "stop\n")}
			if err := RunSession(context.Background(), eventFile(t, journal), options); err != nil {
				t.Fatal(err)
			}
			if fixture.rich {
				if strings.Count(out.String(), "Installer: Configuring agent notification tools - running\n") != 1 || strings.Contains(out.String(), "preparation") {
					t.Fatalf("current stage missing, duplicated, or historical stage replayed: %q", out.String())
				}
			} else if out.Len() != 0 {
				t.Fatalf("plain transient replay was not quiet: %q", out.String())
			}
		})
	}
}

func TestSessionStopControlAndJournalFailuresKeepPrivateInputOut(t *testing.T) {
	for _, content := range []string{"transition\tclaude\t\x1b[2JSECRET\trunning\n", strings.Repeat("s", 257), strings.Repeat("s", journalLimit+1)} {
		var out bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := RunSession(ctx, eventFile(t, content), Options{Output: &out, Rich: true, Width: 80, Height: 24})
		cancel()
		if err == nil || errors.Is(err, context.DeadlineExceeded) || strings.Contains(out.String(), "SECRET") || strings.Contains(out.String(), "\x1b[2J") {
			t.Fatalf("unsafe journal processing: %v %q", err, out.String())
		}
	}
	path := eventFile(t, "snapshot\tcodex\tpending\tnot_started\n")
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if runtime.GOOS != "windows" {
		if err := RunSession(context.Background(), path, Options{Output: &out}); err == nil {
			t.Fatal("accepted nonprivate journal")
		}
	}
}

func TestSessionResizeAbandonsOldGeometryAndShorterFramesEraseTail(t *testing.T) {
	// Cursor-up against the old reflowed surface would erase foreign scrollback.
	s := &session{history: map[string][]event{}, last: map[string]event{}, lost: map[string]bool{}}
	s.apply("snapshot", event{"claude", "MCP", "activation_timeout"})
	width, height := 80, 24
	var out bytes.Buffer
	v := sessionView{s: s, options: Options{Output: &out, Rich: true, Width: width, Height: height, TerminalSize: func() (int, int) { return width, height }}}
	if err := v.paint(); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	width, height = 50, 12
	if err := v.paint(); err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(`\x1b\[[0-9]+A`).MatchString(out.String()) {
		t.Fatalf("resize used old cursor-up: %q", out.String())
	}
	out.Reset()
	width, height = 32, 10
	v.clear()
	if out.Len() != 0 {
		t.Fatalf("deferred clear used stale geometry: %q", out.String())
	}
	// Without geometry change, a shorter final board must remove its old tail.
	width, height = 80, 24
	s.apply("snapshot", event{"claude", "MCP", "activation_timeout"})
	if err := v.paint(); err != nil {
		t.Fatal(err)
	}
	old := v.painted
	s.apply("snapshot", event{"claude", "complete", "completed"})
	out.Reset()
	if err := v.paint(); err != nil {
		t.Fatal(err)
	}
	if v.painted >= old || !strings.Contains(out.String(), "\r\x1b[2K\n") {
		t.Fatalf("shortened frame retained old footer: %q", out.String())
	}
}

func TestSessionFailedOrShortOutputRestoresCursor(t *testing.T) {
	out := &failingOutput{}
	err := RunSession(context.Background(), eventFile(t, "snapshot\tcodex\tcomplete\tcompleted\nstop\t\t\t\n"), Options{Output: out, Rich: true, Width: 80, Height: 24})
	if err == nil || out.last != "\x1b[0m\x1b[?25h" {
		t.Fatalf("failed output restore: %v %q", err, out.last)
	}
	err = RunSession(context.Background(), eventFile(t, "stop\t\t\t\n"), Options{Output: shortOutput{}, Rich: true, Width: 80, Height: 24})
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short output accepted: %v", err)
	}
}

func TestSessionBoundedHistoryDisclosesLossInCompactAndExpanded(t *testing.T) {
	s := &session{history: map[string][]event{}, last: map[string]event{}, lost: map[string]bool{}}
	for i := 0; i < 40; i++ {
		outcome := "running"
		if i%2 == 0 {
			outcome = "failed"
		}
		s.apply("transition", event{"codex", "config", outcome})
	}
	var out bytes.Buffer
	v := sessionView{s: s, options: Options{Output: &out, Rich: true, Width: 80, Height: 24, Activity: "compact"}}
	if err := v.paint(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Earlier checkpoints omitted") {
		t.Fatal("compact lost-history marker absent")
	}
	out.Reset()
	v.options.Activity = "expanded"
	if err := v.fullHistory(); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "configuration:") != 16 || !strings.Contains(out.String(), "earlier checkpoints omitted") {
		t.Fatalf("expanded history unbounded/undisclosed: %q", out.String())
	}
}

func TestSessionInstallerHistoryLossDoesNotImplyClientHistoryLoss(t *testing.T) {
	s := &session{history: map[string][]event{}, last: map[string]event{}, lost: map[string]bool{}}
	s.apply("transition", event{"codex", "complete", "completed"})
	for i := 0; i < 40; i++ {
		outcome := "running"
		if i%2 == 0 {
			outcome = "failed"
		}
		s.apply("transition", event{"global", "preparation", outcome})
	}
	if len(s.history["global"]) != 16 || !s.lost["global"] || s.lost["codex"] {
		t.Fatal("installer history bound or loss scope incorrect")
	}
	var out bytes.Buffer
	v := sessionView{s: s, options: Options{Output: &out, Rich: true, Width: 80, Height: 24, Activity: "compact"}}
	if err := v.paint(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Recent activity") || !strings.Contains(out.String(), "Earlier installer checkpoints omitted") || strings.Contains(out.String(), "* Codex CLI") || strings.Contains(out.String(), "Earlier checkpoints omitted") {
		t.Fatalf("installer loss attributed to client history: %q", out.String())
	}
	out.Reset()
	v.options.Activity = "expanded"
	if err := v.fullHistory(); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "Installer: ") != 16 || !strings.Contains(out.String(), "Earlier installer checkpoints omitted") || strings.Contains(out.String(), "Codex CLI: earlier") {
		t.Fatalf("expanded installer history lost or misattributed: %q", out.String())
	}
}

func TestSessionMCPStageAndNarrowCardPreserveStatus(t *testing.T) {
	s := &session{history: map[string][]event{}, last: map[string]event{}, lost: map[string]bool{}}
	s.apply("snapshot", event{"cursor", "MCP", "registration_committed"})
	s.apply("transition", event{"global", "notification-tools", "running"})
	if !strings.Contains(s.counters(), "active 1") {
		t.Fatal(s.counters())
	}
	var out bytes.Buffer
	v := sessionView{s: s, options: Options{Output: &out, Rich: true, Width: 50, Height: 12}}
	if err := v.paint(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Configuring notifications") {
		t.Fatal(out.String())
	}
	// Strip semantic styling, then check physical columns including border.
	for _, line := range strings.Split(regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`).ReplaceAllString(out.String(), ""), "\n") {
		if len([]rune(strings.TrimPrefix(line, "\r"))) >= 50 {
			t.Fatalf("card wraps: %q", line)
		}
	}
}

func TestSessionCanonicalOrderAndDistinctActivationFailures(t *testing.T) {
	var out bytes.Buffer
	journal := "snapshot\tgemini\tMCP\tactivation_not_attempted\nsnapshot\topencode\tMCP\tactivation_deadline\nsnapshot\tcodex\tMCP\tactivation_canceled\nsnapshot\tclaude\tcomplete\tcompleted\nstop\t\t\t\n"
	if err := RunSession(context.Background(), eventFile(t, journal), Options{Output: &out, Activity: "off"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	ordered := strings.Index(got, "Claude Code") < strings.Index(got, "Codex CLI") &&
		strings.Index(got, "Codex CLI") < strings.Index(got, "OpenCode") &&
		strings.Index(got, "OpenCode") < strings.Index(got, "Gemini CLI")
	if !ordered || !strings.Contains(got, "activation canceled") || !strings.Contains(got, "deadline reached") || !strings.Contains(got, "not started") {
		t.Fatal(got)
	}
}

func TestSessionFragmentThenTruncationStopsWithoutInventingRow(t *testing.T) {
	path := eventFile(t, "snapshot\tclaude\tpending\tnot_started\n")
	out := &observedOutput{frames: make(chan string, 32)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- RunSession(ctx, path, Options{Output: out, Rich: true, Width: 80, Height: 24, Interval: time.Millisecond})
	}()
	waitFrame(t, out, "Claude Code")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("snapshot\tcodex\tconfig\trun"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	waitFrame(t, out, "Claude Code")
	if strings.Contains(out.String(), "Codex CLI") {
		t.Fatal("fragment rendered before newline")
	}
	if err = os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	err = <-done
	if err == nil || errors.Is(err, context.DeadlineExceeded) || !strings.HasSuffix(out.String(), "\x1b[0m\x1b[?25h") {
		t.Fatalf("truncation did not stop/restore: %v %q", err, out.String())
	}
}

func TestSessionRejectsTrailingFragmentAcrossStartupReadBoundary(t *testing.T) {
	// Put the terminal record exactly at the 4096-byte startup read boundary.
	prefix := strings.Repeat("snapshot\tclaude\tpending\tnot_started\n", 28) + strings.Repeat("snapshot\tcodex\tpending\tnot_started\n", 88) + "stop\t\t\t\n"
	if len(prefix) != 4096 {
		t.Fatal("fixture boundary drift")
	}
	var out bytes.Buffer
	err := RunSession(context.Background(), eventFile(t, prefix+"PRIVATE_FRAGMENT"), Options{Output: &out})
	if err == nil || strings.Contains(out.String(), "PRIVATE_FRAGMENT") {
		t.Fatalf("terminal journal accepted trailing fragment: %v %q", err, out.String())
	}
}

// A singular installer stage used to hide concurrent clients' own phases and
// counters could report failed clients as active. Exercise the shared model and
// the visible rich frame with a deterministic observation clock.
func TestSessionConcurrentClientPhasesAndElapsedRemainIndependent(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	s := &session{
		rows:    board{now: func() time.Time { return now }},
		history: map[string][]event{}, last: map[string]event{}, lost: map[string]bool{},
	}
	apply := func(line string) {
		t.Helper()
		kind, e, _, err := parseSession(line)
		if err != nil {
			t.Fatal(err)
		}
		s.apply(kind, e)
	}
	apply("transition\tclaude\tmarketplace\trunning")
	now = now.Add(5 * time.Second)
	apply("transition\tcodex\tdownload\trunning")
	apply("transition\tglobal\tinstalling-codex\trunning")
	now = now.Add(7 * time.Second)
	apply("snapshot\tclaude\tmarketplace\trunning")
	var out bytes.Buffer
	v := sessionView{s: s, options: Options{Output: &out, Rich: true, Width: 100, Height: 24, Activity: "off"}}
	if err := v.paint(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Claude Code: Checking marketplace (12s)",
		"Codex CLI: Downloading files (7s)",
		"Completed 0; active 2; queued 0",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing independent progress %q: %q", want, out.String())
		}
	}
	apply("transition\tclaude\truntime\trunning")
	now = now.Add(3 * time.Second)
	lines := strings.Join(s.currentLines(100, 0), "\n")
	if !strings.Contains(lines, "Installing runtime (3s)") || !strings.Contains(lines, "Downloading files (10s)") {
		t.Fatalf("phase change reset another client's elapsed time: %q", lines)
	}
	apply("transition\tcodex\tdownload\tfailed")
	if s.counters() != "Completed 0; active 1; queued 0" {
		t.Fatalf("failure counted as active or complete: %s", s.counters())
	}
	lines = strings.Join(s.currentLines(100, 0), "\n")
	if !strings.Contains(lines, "! Codex CLI: failed") || strings.Contains(lines, "Codex CLI: Downloading files") || strings.Contains(lines, "Installed;") {
		t.Fatalf("failure projected as active or complete: %q", lines)
	}
	apply("transition\tclaude\tverification\trunning")
	apply("snapshot\tgemini\twaiting\trunning")
	lines = strings.Join(s.currentLines(100, 0), "\n")
	if !strings.Contains(lines, "Verifying registration") || !strings.Contains(lines, "Waiting for shared settings") {
		t.Fatal(lines)
	}
	apply("transition\tclaude\tverification\tdone")
	lines = strings.Join(s.currentLines(100, 0), "\n")
	if !strings.Contains(lines, "Registration verified") || strings.Contains(lines, "Installed;") || s.counters() != "Completed 0; active 1; queued 0" {
		t.Fatalf("probe completion was presented as completed setup: %s / %q", s.counters(), lines)
	}
	s.stopped, s.rows.stopped = true, true
	lines = strings.Join(s.currentLines(100, 0), "\n")
	if s.counters() != "Completed 0; active 0; queued 0" || strings.Contains(lines, "⠋") || !strings.Contains(lines, "last observed running") {
		t.Fatalf("observer stop claimed active or completed work: %s / %q", s.counters(), lines)
	}
}

func TestSessionUncertainSnapshotNeedsAuthoritativeActivity(t *testing.T) {
	s := &session{history: map[string][]event{}, last: map[string]event{}, lost: map[string]bool{}}
	s.apply("snapshot", event{"claude", "registration", "state_requires_inspection"})
	s.apply("snapshot", event{"codex", "MCP", "registration_committed"})
	lines := strings.Join(s.currentLines(100, 0), "\n")
	if s.counters() != "Completed 0; active 0; queued 0" || strings.Contains(lines, "⠋") || strings.Contains(lines, "observed") {
		t.Fatalf("partial state invented live work: %s / %q", s.counters(), lines)
	}
	s.apply("transition", event{"global", "installing-claude", "running"})
	s.apply("snapshot", event{"claude", "MCP", "activation_timeout"})
	lines = strings.Join(s.currentLines(100, 0), "\n")
	if s.counters() != "Completed 0; active 0; queued 0" || !strings.Contains(lines, "Timed out") || !strings.Contains(lines, "Registration saved") || strings.Contains(lines, "⠋") {
		t.Fatalf("legacy stage hid the saved-registration failure: %s / %q", s.counters(), lines)
	}
}
