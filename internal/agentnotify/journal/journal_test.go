//go:build linux || darwin

package journal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type testClock struct{ x Sample }

func (c *testClock) Sample() Sample { return c.x }
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func fixture(t *testing.T, l Limits) (*Store, *testClock) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	c := &testClock{Sample{"boot-A", 100, true}}
	s, e := Initialize(testContext(t), Options{root, c, l})
	if e != nil {
		t.Fatal(e)
	}
	return s, c
}
func admission(id string) Admission {
	return Admission{Key: Key{"codex/local", "session-A", Explicit, id}, Digest: sha256.Sum256([]byte("normalized payload")), TrackingID: "tracking-" + id, Decision: Snapshot{Target: Target{"chat_id", "chat-A", "/test/Original.app", "team-A"}, Policy: "local-enabled", Navigation: Navigation{"available", "chat_id", "local_current_profile", "configured"}}}
}
func mustAdmit(t *testing.T, s *Store, a Admission) Result {
	t.Helper()
	r, e := s.Admit(testContext(t), a)
	if e != nil || !r.Fresh {
		t.Fatalf("admit fresh=%v: %v", r.Fresh, e)
	}
	return r
}
func reopen(t *testing.T, s *Store, c Clock) *Store {
	t.Helper()
	r, e := Open(testContext(t), Options{s.root, c, s.limits})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestReplayIdentityAndCAS(t *testing.T) {
	s, c := fixture(t, Limits{})
	a := admission("R")
	if r, e := s.Lookup(testContext(t), a.Key, a.Digest); e != nil || r.Found {
		t.Fatal(r, e)
	}
	first := mustAdmit(t, s, a)
	if first.ScopedKey != a.Key.scoped(s.namespace) {
		t.Fatal("missing native identity input")
	}
	a.Decision.Target.Application = "/new/Changed.app"
	a.TrackingID = "replacement"
	s = reopen(t, s, c)
	r, e := s.Admit(testContext(t), a)
	if e != nil || r.Fresh {
		t.Fatal(r, e)
	}
	old, _ := json.Marshal(first.Record)
	got, _ := json.Marshal(r.Record)
	if string(old) != string(got) {
		t.Fatal("original decision changed")
	}
	if r.Record.Receipt.Status != "unknown" || r.Record.Receipt.Reason != "pending_submission" {
		t.Fatal(r)
	}
	if e = s.Finalize(testContext(t), a.Key, strings.Repeat("a", 64), "submitted", "os_accepted", "test"); !errors.Is(e, ErrCAS) {
		t.Fatal(e)
	}
	if e = s.Finalize(testContext(t), a.Key, first.Record.Attempt, "rejected", "permission_denied", "test"); e != nil {
		t.Fatal(e)
	}
	r, e = s.Lookup(testContext(t), a.Key, a.Digest)
	if e != nil || r.Record.Receipt.Status != "rejected" || r.Record.Receipt.Decision.Target.Application != "/test/Original.app" {
		t.Fatal(r, e)
	}
	if e = s.Finalize(testContext(t), a.Key, first.Record.Attempt, "submitted", "late", "test"); !errors.Is(e, ErrCAS) {
		t.Fatal(e)
	}
	a.Digest = sha256.Sum256([]byte("different"))
	if _, e = s.Admit(testContext(t), a); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(s.root, "journal.json"))
	if e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{"normalized payload", "different", "session-A", "codex/local"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("stored raw input %s", secret)
		}
	}
}
func TestKeyFramingAndKinds(t *testing.T) {
	if hash("ab", "c") == hash("a", "bc") {
		t.Fatal("ambiguous framing")
	}
	s, _ := fixture(t, Limits{})
	a := admission("same")
	mustAdmit(t, s, a)
	a.Key.Kind = ClientCall
	r := mustAdmit(t, s, a)
	if r.Record.Receipt.RequestID != nil {
		t.Fatal("tracking is not explicit")
	}
	a.Key.Kind = Generated
	mustAdmit(t, s, a)
	if a.Key.scoped(s.namespace) == a.Key.scoped(strings.Repeat("a", 64)) {
		t.Fatal("namespace ignored")
	}
}
func TestSessionIsolation(t *testing.T) {
	s, _ := fixture(t, Limits{})
	a := admission("same-call")
	a.Key.Kind = ClientCall
	one := mustAdmit(t, s, a)
	a.Key.Session = "session-B"
	two := mustAdmit(t, s, a)
	if one.Record.Attempt == two.Record.Attempt {
		t.Fatal("same attempt")
	}
}
func TestClockRetentionAndLateCAS(t *testing.T) {
	s, c := fixture(t, Limits{Records: 1})
	a := admission("R")
	old := mustAdmit(t, s, a)
	// A process restart preserves the same boot high-water mark. No wall time is used.
	s = reopen(t, s, c)
	c.x.Seconds += 61
	if e := s.Collect(testContext(t)); e != nil {
		t.Fatal(e)
	}
	c.x.Seconds = 1
	if e := s.Collect(testContext(t)); e != nil {
		t.Fatal(e)
	} // regression
	c.x = Sample{"boot-B", 900000, true}
	if e := s.Collect(testContext(t)); e != nil {
		t.Fatal(e)
	} // reboot adds zero
	c.x.Available = false
	if e := s.Collect(testContext(t)); e != nil {
		t.Fatal(e)
	}
	c.x = Sample{"boot-B", 1900000, true}
	if e := s.Collect(testContext(t)); e != nil {
		t.Fatal(e)
	} // unavailable gap adds zero
	if _, e := s.Admit(testContext(t), admission("other")); !errors.Is(e, ErrFull) {
		t.Fatal(e)
	}
	c.x.Seconds += MinRetention - 60
	if e := s.Collect(testContext(t)); e != nil {
		t.Fatal(e)
	}
	r, e := s.Lookup(testContext(t), a.Key, a.Digest)
	if e != nil || !r.Found {
		t.Fatal("early eviction", r, e)
	}
	c.x.Seconds += 2
	if e := s.Collect(testContext(t)); e != nil {
		t.Fatal(e)
	}
	fresh := mustAdmit(t, s, a)
	if fresh.Record.Attempt == old.Record.Attempt {
		t.Fatal("reused token")
	}
	if e := s.Finalize(testContext(t), a.Key, old.Record.Attempt, "submitted", "late", "test"); !errors.Is(e, ErrCAS) {
		t.Fatal(e)
	}
}
func TestRateWindowsReplayRestartReboot(t *testing.T) {
	s, c := fixture(t, Limits{})
	for i := 0; i < 3; i++ {
		mustAdmit(t, s, admission(strconv.Itoa(i)))
	}
	for i := 0; i < 20; i++ {
		r, e := s.Admit(testContext(t), admission("0"))
		if e != nil || r.Fresh {
			t.Fatal(r, e)
		}
	}
	if _, e := s.Admit(testContext(t), admission("burst")); !errors.Is(e, ErrRate) {
		t.Fatal(e)
	}
	c.x.Seconds += 11
	for i := 3; i < 6; i++ {
		mustAdmit(t, s, admission(strconv.Itoa(i)))
	}
	c.x.Seconds += 11
	if _, e := s.Admit(testContext(t), admission("session-full")); !errors.Is(e, ErrRate) {
		t.Fatal(e)
	}
	s = reopen(t, s, c)
	c.x = Sample{"boot-B", 9000000, true}
	if e := s.Collect(testContext(t)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Admit(testContext(t), admission("no-reset")); !errors.Is(e, ErrRate) {
		t.Fatal(e)
	}
	c.x.Available = false
	for i := 0; i < 2; i++ {
		if _, e := s.Admit(testContext(t), admission("clock-missing")); !errors.Is(e, ErrRate) {
			t.Fatal(e)
		}
	}
	c.x.Available = true
	c.x.Seconds += 61
	mustAdmit(t, s, admission("after-window"))
}

func TestRateLimitPersistsClockAcrossRebootWithoutCollect(t *testing.T) {
	s, c := fixture(t, Limits{})
	for i := 0; i < 3; i++ {
		mustAdmit(t, s, admission("burst-"+strconv.Itoa(i)))
	}
	if _, e := s.Admit(testContext(t), admission("full")); !errors.Is(e, ErrRate) {
		t.Fatal(e)
	}
	c.x = Sample{"boot-B", 9_000_000, true}
	if _, e := s.Admit(testContext(t), admission("reboot")); !errors.Is(e, ErrRate) {
		t.Fatal(e)
	}
	s = reopen(t, s, c)
	c.x.Seconds += 61
	mustAdmit(t, s, admission("recovered"))
}
func TestRuntimeRate(t *testing.T) {
	s, c := fixture(t, Limits{})
	for group := 0; group < 10; group++ {
		for j := 0; j < 3; j++ {
			a := admission(fmt.Sprintf("%d-%d", group, j))
			a.Key.Session = fmt.Sprintf("session-%d", group)
			mustAdmit(t, s, a)
		}
		c.x.Seconds += 3
	}
	a := admission("runtime-full")
	a.Key.Session = "new-session"
	if _, e := s.Admit(testContext(t), a); !errors.Is(e, ErrRate) {
		t.Fatal(e)
	}
	s = reopen(t, s, c)
	if _, e := s.Admit(testContext(t), a); !errors.Is(e, ErrRate) {
		t.Fatal(e)
	}
	c.x.Seconds += 40
	mustAdmit(t, s, a)
}
func TestCorruptionAndLostState(t *testing.T) {
	cases := map[string]func([]byte) []byte{"partial": func(b []byte) []byte { return b[:len(b)/2] }, "version": func(b []byte) []byte { return []byte(strings.Replace(string(b), `"version":1`, `"version":999`, 1)) }, "unknown-field": func(b []byte) []byte { return append([]byte(`{"unexpected":1,`), b[1:]...) }, "duplicate": func(b []byte) []byte { return append([]byte(`{"version":1,`), b[1:]...) }, "null-records": func(b []byte) []byte { return []byte(strings.Replace(string(b), `"records":{}`, `"records":null`, 1)) }, "trailing": func(b []byte) []byte { return append(b, []byte(` {}`)...) }}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			s, _ := fixture(t, Limits{})
			p := filepath.Join(s.root, "journal.json")
			b, _ := os.ReadFile(p)
			broken := damage(b)
			if e := os.WriteFile(p, broken, 0600); e != nil {
				t.Fatal(e)
			}
			if _, e := s.Admit(testContext(t), admission("R")); !errors.Is(e, ErrRepair) {
				t.Fatal(e)
			}
			after, _ := os.ReadFile(p)
			if string(after) != string(broken) {
				t.Fatal("silently repaired")
			}
		})
	}
	for _, name := range []string{"journal.json", "namespace", "lock"} {
		t.Run("lost-"+name, func(t *testing.T) {
			s, c := fixture(t, Limits{})
			mustAdmit(t, s, admission("R"))
			if e := os.Remove(filepath.Join(s.root, name)); e != nil {
				t.Fatal(e)
			}
			if _, e := Open(testContext(t), Options{s.root, c, s.limits}); !errors.Is(e, ErrRepair) {
				t.Fatal(e)
			}
			if _, e := Initialize(testContext(t), Options{s.root, c, s.limits}); !errors.Is(e, ErrRepair) {
				t.Fatal("bootstrap reset", e)
			}
			if _, e := os.Lstat(filepath.Join(s.root, name)); !os.IsNotExist(e) {
				t.Fatal("bootstrap recreated lost expected file", name, e)
			}
		})
	}
}
func TestBoundsAndCounterOverflow(t *testing.T) {
	s, _ := fixture(t, Limits{Bytes: 1024})
	a := admission("large")
	a.Decision.Target.Application = strings.Repeat("x", 1024)
	if _, e := s.Admit(testContext(t), a); !errors.Is(e, ErrFull) {
		t.Fatal(e)
	}
	r, e := s.Lookup(testContext(t), a.Key, a.Digest)
	if e != nil || r.Found {
		t.Fatal(r, e)
	}
	s, c := fixture(t, Limits{})
	if e = s.transaction(testContext(t), func(d *disk) (bool, error) { d.Clock.Logical = math.MaxUint64; return true, nil }); e != nil {
		t.Fatal(e)
	}
	c.x.Seconds += 2
	if _, e = s.Admit(testContext(t), admission("overflow")); !errors.Is(e, ErrFull) {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(s.root, "journal.json"), []byte(strings.Repeat(" ", 8<<20+1)), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Lookup(testContext(t), admission("R").Key, Digest{}); !errors.Is(e, ErrRepair) {
		t.Fatal(e)
	}
}
func TestFaultsPreserveAdmission(t *testing.T) {
	for _, stage := range []string{"before_temp", "partial_write", "before_file_sync", "after_file_sync", "before_rename", "after_rename", "after_directory_sync"} {
		t.Run(stage, func(t *testing.T) {
			s, c := fixture(t, Limits{})
			a := admission("R")
			r := mustAdmit(t, s, a)
			fault := errors.New("injected disk failure")
			s.fault = func(at string) error {
				if at == stage {
					return fault
				}
				return nil
			}
			if e := s.Finalize(testContext(t), a.Key, r.Record.Attempt, "submitted", "os_accepted", "test"); !errors.Is(e, fault) {
				t.Fatal(e)
			}
			s = reopen(t, s, c)
			got, e := s.Admit(testContext(t), a)
			if e != nil || got.Fresh {
				t.Fatal(got, e)
			}
			if stage != "after_rename" && stage != "after_directory_sync" && got.Record.State != "dispatching" {
				t.Fatal("pending lost")
			}
		})
	}
}
func TestUnsafePathsAndLockCancellation(t *testing.T) {
	s, _ := fixture(t, Limits{})
	dir, e := openRoot(s.root)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { requireNoError(t, dir.Close()) }()
	l, e := lock(testContext(t), dir, false)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, e = s.Admit(ctx, admission("R")); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	requireNoError(t, l.Close())
	if _, e = s.Lookup(context.Background(), admission("R").Key, Digest{}); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	canceled, stop := context.WithCancel(testContext(t))
	stop()
	if _, e = s.Admit(canceled, admission("R")); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	for _, kind := range []string{"symlink", "hardlink", "mode", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := fixture(t, Limits{})
			p := filepath.Join(s.root, "journal.json")
			switch kind {
			case "symlink":
				requireNoError(t, os.Rename(p, p+".old"))
				requireNoError(t, os.Symlink(p+".old", p))
			case "hardlink":
				requireNoError(t, os.Link(p, p+".link"))
			case "mode":
				requireNoError(t, os.Chmod(p, 0644))
			case "fifo":
				requireNoError(t, os.Remove(p))
				requireNoError(t, unix.Mkfifo(p, 0600))
			}
			if _, e := s.Admit(testContext(t), admission("R")); !errors.Is(e, ErrRepair) {
				t.Fatal(e)
			}
		})
	}
	root := t.TempDir()
	link := filepath.Join(root, "link")
	requireNoError(t, os.Symlink(s.root, link))
	if _, e = Open(testContext(t), Options{Root: link}); !errors.Is(e, ErrRepair) {
		t.Fatal(e)
	}
	requireNoError(t, os.Chmod(s.root, 0777))
	if _, e = s.Admit(testContext(t), admission("R")); !errors.Is(e, ErrRepair) {
		t.Fatal(e)
	}
}

// Every helper runs this same test binary in a separate OS process and accesses
// only injected test roots. The effect counter is a test file, never delivery.
func TestJournalProcess(t *testing.T) {
	if os.Getenv("JOURNAL_CHILD") != "1" {
		return
	}
	root := os.Getenv("JOURNAL_ROOT")
	if os.Getenv("JOURNAL_PHASE") == "bootstrap" {
		s, e := newStore(Options{Root: root, Clock: ClockFunc(func() Sample { return Sample{"boot-A", 100, true} })})
		if e != nil {
			t.Fatal(e)
		}
		s.fault = func(stage string) error {
			if stage == os.Getenv("JOURNAL_CRASH") {
				os.Exit(71)
			}
			return nil
		}
		if e = s.bootstrap(testContext(t)); e != nil {
			t.Fatal(e)
		}
		t.Fatal("crash hook missed")
	}
	s, e := Open(testContext(t), Options{Root: root, Clock: ClockFunc(func() Sample { return Sample{"boot-A", 100, true} })})
	if e != nil {
		t.Fatal(e)
	}
	a := admission(os.Getenv("JOURNAL_KEY"))
	if raw := os.Getenv("JOURNAL_RATES"); raw != "" {
		requireNoError(t, json.Unmarshal([]byte(raw), &a.Rates))
	}
	if os.Getenv("JOURNAL_DIGEST") == "other" {
		a.Digest = sha256.Sum256([]byte("conflict"))
	}
	if gate := os.Getenv("JOURNAL_GATE"); gate != "" {
		r, e := s.Lookup(testContext(t), a.Key, a.Digest)
		if e != nil || r.Found {
			t.Fatal("preflight lookup", r, e)
		}
		if e = os.WriteFile(filepath.Join(root, "ready-"+strconv.Itoa(os.Getpid())), []byte("ready"), 0600); e != nil {
			t.Fatal(e)
		}
		ctx := testContext(t)
		for {
			if _, e = os.Stat(filepath.Join(root, gate)); e == nil {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(time.Millisecond):
			}
		}
	}
	phase := os.Getenv("JOURNAL_PHASE")
	stage := os.Getenv("JOURNAL_CRASH")
	if phase == "admit" {
		s.fault = func(at string) error {
			if at == stage {
				os.Exit(71)
			}
			return nil
		}
	}
	r, e := s.Admit(testContext(t), a)
	if errors.Is(e, ErrConflict) {
		fmt.Println("CONFLICT")
		return
	}
	if errors.Is(e, ErrRate) {
		fmt.Println("RATE")
		return
	}
	if e != nil {
		t.Fatal(e)
	}
	if !r.Fresh {
		fmt.Println("REPLAY")
		return
	}
	fmt.Println("FRESH")
	if phase == "before_effect" {
		os.Exit(71)
	}
	f, e := os.OpenFile(filepath.Join(root, "effects"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.WriteString("effect\n"); e != nil {
		t.Fatal(e)
	}
	if e = f.Sync(); e != nil {
		t.Fatal(e)
	}
	requireNoError(t, f.Close())
	if phase == "after_effect" {
		os.Exit(71)
	}
	if phase == "finalize" {
		s.fault = func(at string) error {
			if at == stage {
				os.Exit(71)
			}
			return nil
		}
	}
	if e = s.Finalize(testContext(t), a.Key, r.Record.Attempt, "submitted", "os_accepted", "counter"); e != nil {
		t.Fatal(e)
	}
}
func child(s *Store, key, phase, stage, digest string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestJournalProcess$", "-test.timeout=10s")
	cmd.Env = append(os.Environ(), "JOURNAL_CHILD=1", "JOURNAL_ROOT="+s.root, "JOURNAL_KEY="+key, "JOURNAL_PHASE="+phase, "JOURNAL_CRASH="+stage, "JOURNAL_DIGEST="+digest)
	return cmd
}
func effects(t *testing.T, s *Store) int {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(s.root, "effects"))
	if os.IsNotExist(e) {
		return 0
	}
	if e != nil {
		t.Fatal(e)
	}
	return strings.Count(string(b), "effect\n")
}
func TestActualProcessesSameKeyAndConflict(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(strconv.FormatBool(conflict), func(t *testing.T) {
			s, _ := fixture(t, Limits{})
			cmds := []*exec.Cmd{child(s, "R", "", "", ""), child(s, "R", "", "", "")}
			if conflict {
				cmds[1] = child(s, "R", "", "", "other")
			}
			var wg sync.WaitGroup
			out := make([][]byte, 2)
			errs := make([]error, 2)
			for i := range cmds {
				wg.Add(1)
				go func(i int) { defer wg.Done(); out[i], errs[i] = cmds[i].CombinedOutput() }(i)
			}
			wg.Wait()
			for i, e := range errs {
				if e != nil {
					t.Fatalf("%v: %s", e, out[i])
				}
			}
			joined := string(out[0]) + string(out[1])
			if strings.Count(joined, "FRESH") != 1 || effects(t, s) != 1 {
				t.Fatal(joined)
			}
			if conflict && !strings.Contains(joined, "CONFLICT") {
				t.Fatal(joined)
			}
		})
	}
}
func TestActualProcessesDifferentKeysRate(t *testing.T) {
	s, _ := fixture(t, Limits{})
	const n = 8
	var wg sync.WaitGroup
	outputs := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b, e := child(s, strconv.Itoa(i), "", "", "").CombinedOutput()
			outputs[i] = string(b)
			if e != nil {
				outputs[i] += " ERROR " + e.Error()
			}
		}(i)
	}
	wg.Wait()
	all := strings.Join(outputs, "\n")
	if strings.Contains(all, "ERROR") || strings.Count(all, "FRESH") != 3 || strings.Count(all, "RATE") != 5 || effects(t, s) != 3 {
		t.Fatal(all)
	}
}
func TestActualCrashBoundaries(t *testing.T) {
	stages := []string{"before_temp", "partial_write", "before_file_sync", "after_file_sync", "before_rename", "after_rename", "after_directory_sync"}
	for _, phase := range []string{"admit", "finalize", "before_effect", "after_effect"} {
		for _, stage := range stages {
			if (phase == "before_effect" || phase == "after_effect") && stage != "before_temp" {
				continue
			}
			t.Run(phase+"/"+stage, func(t *testing.T) {
				s, c := fixture(t, Limits{})
				b, e := child(s, "R", phase, stage, "").CombinedOutput()
				var exit *exec.ExitError
				if !errors.As(e, &exit) || exit.ExitCode() != 71 {
					t.Fatalf("crash missing: %v %s", e, b)
				}
				s = reopen(t, s, c)
				b, e = child(s, "R", "", "", "").CombinedOutput()
				if e != nil {
					t.Fatalf("restart %v: %s", e, b)
				}
				want := 1
				if phase == "before_effect" || (phase == "admit" && (stage == "after_rename" || stage == "after_directory_sync")) {
					want = 0
				}
				if effects(t, s) != want {
					t.Fatalf("effects=%d want=%d output=%s", effects(t, s), want, b)
				}
				dir, e := openRoot(s.root)
				requireNoError(t, e)
				l, e := lock(testContext(t), dir, false)
				if e != nil {
					t.Fatal("dead process retained lock", e)
				}
				requireNoError(t, l.Close())
				requireNoError(t, dir.Close())
			})
		}
	}
}
func TestStrictJSON(t *testing.T) {
	for _, b := range []string{`{"x":1,"x":2}`, `{"x":"\ud800"}`, `{"x":"\udc00"}`, `{}[]`, strings.Repeat("[", 18) + strings.Repeat("]", 18)} {
		if strictJSON([]byte(b)) == nil {
			t.Fatal(b)
		}
	}
	for _, b := range []string{`{"emoji":"\ud83d\ude00"}`, `{"x":"\\ud800"}`, "{\"x\":\"a\u200db\"}"} {
		if e := strictJSON([]byte(b)); e != nil {
			t.Fatal(b, e)
		}
	}
}

func TestActualLookupAdmitRace(t *testing.T) {
	s, _ := fixture(t, Limits{})
	cmds := []*exec.Cmd{child(s, "R", "", "", ""), child(s, "R", "", "", "")}
	var outputs [2]bytes.Buffer
	for i, cmd := range cmds {
		cmd.Env = append(cmd.Env, "JOURNAL_GATE=go")
		cmd.Stdout = &outputs[i]
		cmd.Stderr = &outputs[i]
		if e := cmd.Start(); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
	}
	ctx := testContext(t)
	for {
		ready, e := filepath.Glob(filepath.Join(s.root, "ready-*"))
		if e != nil {
			t.Fatal(e)
		}
		if len(ready) == 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("preflight barrier timeout")
		case <-time.After(time.Millisecond):
		}
	}
	if e := os.WriteFile(filepath.Join(s.root, "go"), []byte("go"), 0600); e != nil {
		t.Fatal(e)
	}
	for i, cmd := range cmds {
		if e := cmd.Wait(); e != nil {
			t.Fatalf("%v %s", e, outputs[i].String())
		}
	}
	all := outputs[0].String() + outputs[1].String()
	if strings.Count(all, "FRESH") != 1 || strings.Count(all, "REPLAY") != 1 || effects(t, s) != 1 {
		t.Fatal(all)
	}
}
func TestRateCounterCorruption(t *testing.T) {
	s, _ := fixture(t, Limits{})
	mustAdmit(t, s, admission("R"))
	p := filepath.Join(s.root, "journal.json")
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	var d disk
	if e = json.Unmarshal(b, &d); e != nil {
		t.Fatal(e)
	}
	d.Events = []event{}
	b, e = json.Marshal(d)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Admit(testContext(t), admission("new")); !errors.Is(e, ErrRepair) {
		t.Fatal(e)
	}
}
func TestPermanentLockAndRealWriteDenial(t *testing.T) {
	s, c := fixture(t, Limits{})
	a := admission("R")
	r := mustAdmit(t, s, a)
	before, e := os.Stat(filepath.Join(s.root, "lock"))
	if e != nil {
		t.Fatal(e)
	}
	s.fault = func(stage string) error {
		if stage == "before_temp" {
			return os.Chmod(s.root, 0500)
		}
		return nil
	}
	e = s.Finalize(testContext(t), a.Key, r.Record.Attempt, "submitted", "os_accepted", "test")
	if restore := os.Chmod(s.root, 0700); restore != nil {
		t.Fatal(restore)
	}
	if !errors.Is(e, os.ErrPermission) {
		t.Fatalf("expected actual EACCES, got %v", e)
	}
	s.fault = nil
	got, e := s.Lookup(testContext(t), a.Key, a.Digest)
	if e != nil || got.Record.State != "dispatching" {
		t.Fatal(got, e)
	}
	c.x.Seconds += MinRetention + 1
	if e = s.Collect(testContext(t)); e != nil {
		t.Fatal(e)
	}
	after, e := os.Stat(filepath.Join(s.root, "lock"))
	if e != nil || !os.SameFile(before, after) {
		t.Fatal("lock inode changed", e)
	}
}
func TestFailedAdmissionDoesNotConsumeKeyOrRate(t *testing.T) {
	for _, stage := range []string{"before_temp", "partial_write", "before_file_sync", "after_file_sync", "before_rename"} {
		t.Run(stage, func(t *testing.T) {
			s, _ := fixture(t, Limits{})
			s.fault = func(at string) error {
				if at == stage {
					return unix.ENOSPC
				}
				return nil
			}
			if _, e := s.Admit(testContext(t), admission("R")); !errors.Is(e, unix.ENOSPC) {
				t.Fatal(e)
			}
			s.fault = nil
			for _, id := range []string{"R", "R2", "R3"} {
				mustAdmit(t, s, admission(id))
			}
		})
	}
}

func TestClockHighWaterAndFrozenRate(t *testing.T) {
	s, c := fixture(t, Limits{})
	mustAdmit(t, s, admission("R"))
	for _, step := range []struct{ seconds, logical uint64 }{{110, 9}, {1, 9}, {105, 9}, {112, 10}} {
		c.x.Seconds = step.seconds
		if e := s.Collect(testContext(t)); e != nil {
			t.Fatal(e)
		}
		if e := s.transaction(testContext(t), func(d *disk) (bool, error) {
			if d.Clock.Logical != step.logical {
				t.Fatalf("logical=%d want=%d", d.Clock.Logical, step.logical)
			}
			return false, nil
		}); e != nil {
			t.Fatal(e)
		}
	}
	// With no qualified clock, fresh capacity is finite and never replenishes.
	s = reopen(t, s, nil)
	for _, id := range []string{"a", "b", "c"} {
		mustAdmit(t, s, admission(id))
	}
	if _, e := s.Admit(testContext(t), admission("frozen")); !errors.Is(e, ErrRate) {
		t.Fatal(e)
	}
	if e := s.Collect(testContext(t)); e != nil {
		t.Fatal(e)
	}
	s = reopen(t, s, nil)
	if _, e := s.Admit(testContext(t), admission("frozen")); !errors.Is(e, ErrRate) {
		t.Fatal(e)
	}
}
func TestMissingFieldsAndStateInvariants(t *testing.T) {
	for _, change := range []string{"missing-clock", "null-events", "future-record", "mismatched-status", "duplicate-attempt", "unknown-kind"} {
		t.Run(change, func(t *testing.T) {
			s, _ := fixture(t, Limits{})
			mustAdmit(t, s, admission("R"))
			mustAdmit(t, s, admission("S"))
			p := filepath.Join(s.root, "journal.json")
			b, e := os.ReadFile(p)
			if e != nil {
				t.Fatal(e)
			}
			var d disk
			if e = json.Unmarshal(b, &d); e != nil {
				t.Fatal(e)
			}
			id := admission("R").Key.scoped(s.namespace)
			r := d.Records[id]
			switch change {
			case "null-events":
				d.Events = nil
			case "future-record":
				r.Created = d.Clock.Logical + 1
				d.Records[id] = r
			case "mismatched-status":
				r.Receipt.Status = "submitted"
				d.Records[id] = r
			case "duplicate-attempt":
				other := d.Records[admission("S").Key.scoped(s.namespace)]
				r.Attempt = other.Attempt
				d.Records[id] = r
			case "unknown-kind":
				r.Receipt.KeyKind = "tracking"
				d.Records[id] = r
			}
			b, e = json.Marshal(d)
			if e != nil {
				t.Fatal(e)
			}
			if change == "missing-clock" {
				var fields map[string]json.RawMessage
				requireNoError(t, json.Unmarshal(b, &fields))
				delete(fields, "clock")
				b, e = json.Marshal(fields)
				if e != nil {
					t.Fatal(e)
				}
			}
			if e = os.WriteFile(p, b, 0600); e != nil {
				t.Fatal(e)
			}
			if _, e = s.Admit(testContext(t), admission("new")); !errors.Is(e, ErrRepair) {
				t.Fatal(e)
			}
		})
	}
}
func TestCancellationAtWriteBoundary(t *testing.T) {
	s, _ := fixture(t, Limits{})
	ctx, cancel := context.WithCancel(testContext(t))
	s.fault = func(stage string) error {
		if stage == "after_file_sync" {
			cancel()
		}
		return nil
	}
	if r, e := s.Admit(ctx, admission("R")); !errors.Is(e, context.Canceled) || r.Fresh {
		t.Fatal(r, e)
	}
	s.fault = nil
	mustAdmit(t, s, admission("R"))
	// Once durable admission exists, cancellation of finalization preserves pending.
	canceled, stop := context.WithCancel(testContext(t))
	stop()
	a := admission("R")
	r, e := s.Lookup(testContext(t), a.Key, a.Digest)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Finalize(canceled, a.Key, r.Record.Attempt, "submitted", "os_accepted", "test"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	r, e = s.Lookup(testContext(t), a.Key, a.Digest)
	if e != nil || r.Record.State != "dispatching" {
		t.Fatal(r, e)
	}
}

func TestActualBootstrapCrashBoundaries(t *testing.T) {
	for _, stage := range []string{"after_namespace_sync", "partial_write", "after_file_sync", "before_rename", "after_rename", "after_directory_sync"} {
		t.Run(stage, func(t *testing.T) {
			root, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			if e = os.Chmod(root, 0700); e != nil {
				t.Fatal(e)
			}
			s, e := newStore(Options{Root: root})
			requireNoError(t, e)
			b, e := child(s, "R", "bootstrap", stage, "").CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(e, &exit) || exit.ExitCode() != 71 {
				t.Fatalf("%v %s", e, b)
			}
			_, e = Open(testContext(t), Options{Root: root})
			if stage == "after_rename" || stage == "after_directory_sync" {
				if e != nil {
					t.Fatal(e)
				}
			} else if !errors.Is(e, ErrRepair) {
				t.Fatal(e)
			}
			if _, e = Initialize(testContext(t), Options{Root: root}); !errors.Is(e, ErrRepair) {
				t.Fatal("silent bootstrap recovery", e)
			}
		})
	}
}
func TestLimitsAndReadOnlyLookup(t *testing.T) {
	for _, l := range []Limits{{Records: 10001}, {Records: -1}, {Bytes: (8 << 20) + 1}, {Bytes: 1023}, {Retention: MinRetention - 1}, {Retention: 366 * 86400}} {
		if _, e := newStore(Options{Limits: l}); !errors.Is(e, ErrInvalid) {
			t.Fatal(l, e)
		}
	}
	s, c := fixture(t, Limits{})
	a := admission("R")
	mustAdmit(t, s, a)
	paths := []string{"namespace", "journal.json", "lock"}
	before := map[string][]byte{}
	for _, p := range paths {
		b, e := os.ReadFile(filepath.Join(s.root, p))
		if e != nil {
			t.Fatal(e)
		}
		before[p] = b
	}
	s = reopen(t, s, c)
	if _, e := s.Lookup(testContext(t), a.Key, a.Digest); e != nil {
		t.Fatal(e)
	}
	for _, p := range paths {
		b, e := os.ReadFile(filepath.Join(s.root, p))
		if e != nil || !bytes.Equal(b, before[p]) {
			t.Fatal("lookup wrote state", p, e)
		}
	}
}

// Fail on fixture setup errors so a safety test cannot pass without its intended mutation.
func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
