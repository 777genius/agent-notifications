package opencodeevent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/opencodeinstall"
	uap "github.com/777genius/plugin-kit-ai/sdk/opencode"
)

// Independent test authority: boot-wide nanoseconds and epoch advance together.
// It grants no native/platform qualification and reads no ambient clock/env.
type fixtureAuthority struct{ sample ClockSample }

func (c *fixtureAuthority) Snapshot(context.Context) (ClockSample, error) { return c.sample, nil }
func fixtureTimePolicy() TimePolicy {
	return TimePolicy{ProfileID: "TEST-independent-policy", RawKind: "linux-boottime"}
}
func authoritySample() ClockSample {
	s := ClockSample{BootID: "TEST-independent-boot", Domain: "TEST-nanoseconds", Kind: "continuous", TickNS: int64(10 * time.Hour), WallNS: 1700000000000000000}
	s.Fence = fixtureTimePolicy().Fence(s.BootID, s.Domain)
	return s
}
func freshProvenance(now ClockSample) Provenance {
	origin := now
	origin.TickNS -= int64(2 * time.Second)
	origin.WallNS -= int64(2 * time.Second)
	return Provenance{Clock: origin, SourceEpoch: fmt.Sprintf("TEST-source-%d", origin.TickNS), EpochStartedTickNS: origin.TickNS, NativeCreatedNS: now.WallNS - int64(time.Second), IngressTickNS: origin.TickNS, SpawnTickNS: now.TickNS - int64(time.Second), DeadlineTickNS: now.TickNS + int64(19*time.Second)}
}
func admissionFixture(t *testing.T) (context.Context, Admission, AdmissionRequest, *fixtureAuthority) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	base, err := installruntime.CanonicalPath(t.TempDir())
	if err != nil {
		t.Fatal("canonical private fixture root", err)
	}
	root := filepath.Join(base, "control")
	runtimeRoot := filepath.Join(base, "runtime")
	binary := filepath.Join(runtimeRoot, "claude-notifications-linux-amd64")
	plugin := filepath.Join(base, "plugins", "agent-notifications.js")
	bundle := []byte("inert private renderer fixture")
	sum := sha256.Sum256(bundle)
	reg := installruntime.OpenCodeRegistration{Origin: strings.Repeat("11", 32), Salt: strings.Repeat("22", 32), Namespace: strings.Repeat("33", 32), BundleSHA256: hex.EncodeToString(sum[:]), OriginBound: true}
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: root, RuntimeRoot: runtimeRoot, Owner: "existing-installer", ConsumerID: "opencode-notifications",
		Consumer: installruntime.Consumer{Registration: plugin, Commands: []string{binary, "opencode-event", "--protocol", "1"}, OpenCode: &reg},
		Files:    []installruntime.File{{Path: plugin, Data: bundle, Mode: 0600}, {Path: binary, Data: []byte("inert " + installruntime.WriterProtocolMarker + installruntime.OpenCodeWriterProtocolMarker), Mode: 0700}}})
	if err != nil {
		t.Fatal(err)
	}
	l, _, _ := installruntime.ReadOwnership(root)
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: root, RuntimeRoot: runtimeRoot, Owner: l.Owner, ConsumerID: "opencode-notifications", RefreshOnly: true, PolicyOnly: true, ExpectedGeneration: &l.Generation,
		PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"openCodeNotifications":{"desktop":true,"webhook":true}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := installruntime.ReadPolicySnapshot(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	clock := &fixtureAuthority{authoritySample()}
	r := AdmissionRequest{Fact: FactIdentity{Kind: "permission_asked", Session: "session-independent", Execution: "execution-19", Request: "request-5", Root: true}, Origin: reg.Origin,
		Provenance: freshProvenance(clock.sample), Expected: s, Executable: binary, GOOS: "linux", GOARCH: "amd64", CommandStarted: time.Now()}
	return ctx, Admission{ControlRoot: root, Clock: clock, TimePolicy: fixtureTimePolicy()}, r, clock
}
func tryAdmission(t *testing.T, ctx context.Context, a Admission, r AdmissionRequest, want AdmissionStatus) {
	t.Helper()
	h, status := a.Admit(ctx, r)
	if h != nil {
		h.Close()
	}
	if status != want {
		t.Fatalf("admission=%s, want %s", status, want)
	}
}

// Neutral V1 remains decodable without a terminal-error message binding.
// Such a neutral fact alone must never authorize a private terminal claim.
func TestAdmissionPublishedNeutralShapes(t *testing.T) {
	ctx, a, r, _ := admissionFixture(t)
	frames := []string{
		`{"version":1,"kind":"turn_idle_verified","sessionID":"fixture-保","turnID":"completion","messageID":"` + strings.Repeat("a", 256) + `","rootSession":true}`,
		`{"version":1,"kind":"terminal_error","sessionID":"s","turnID":"failure","rootSession":true}`,
		`{"version":1,"kind":"question_asked","sessionID":"s","turnID":"question","requestID":"q","rootSession":true}`,
		`{"version":1,"kind":"permission_asked","sessionID":"s","turnID":"permission","requestID":"p","rootSession":true}`,
	}
	for _, frame := range frames {
		event, err := uap.Decode([]byte(frame))
		if err != nil {
			t.Fatal(err)
		}
		r.Fact = FactIdentity{Kind: string(event.Kind), Session: event.SessionID, Execution: event.TurnID, Terminal: event.MessageID, Request: event.RequestID, Root: *event.RootSession}
		if event.Kind == uap.Kind("terminal_error") {
			tryAdmission(t, ctx, a, r, InvalidFact)
		} else {
			tryAdmission(t, ctx, a, r, Admitted)
			tryAdmission(t, ctx, a, r, Duplicate)
		}
		r.Fact.Root = false
		tryAdmission(t, ctx, a, r, InvalidFact)
	}
}

// Catches canonical-key drift, plaintext identifiers in durable state, and
// per-channel claims that would let duplicate callbacks retry uncertain IO.
func TestAdmissionGoldenDurableClaimAndUpdate(t *testing.T) {
	ctx, a, r, clock := admissionFixture(t)
	h, status := a.Admit(ctx, r)
	if status != Admitted {
		t.Fatal(status)
	}
	desktop, webhook := h.Channels()
	if !desktop || !webhook {
		t.Fatal("channels do not share claim")
	}
	h.Close()
	reg := *r.Expected.Installation.Ledger.Consumers["opencode-notifications"].OpenCode
	store, err := installruntime.AcquireOpenCodeStore(ctx, a.ControlRoot, reg)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := store.Read()
	store.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Golden from independent Python hmac/struct implementation of the port contract.
	if !strings.Contains(string(payload), "6a4582ee5b27e71613efe2e2197aace1674fba5076a29c397af0dc9514ac4aa7") {
		t.Fatal("canonical digest changed")
	}
	for _, secret := range []string{r.Fact.Session, r.Fact.Execution, r.Fact.Request} {
		if strings.Contains(string(payload), secret) {
			t.Fatal("private fact persisted")
		}
	}
	tryAdmission(t, ctx, a, r, Duplicate)
	l := r.Expected.Installation.Ledger
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: a.ControlRoot, RuntimeRoot: l.RuntimeRoot, Owner: l.Owner, ConsumerID: "opencode-notifications", Consumer: l.Consumers["opencode-notifications"], ExpectedGeneration: &l.Generation})
	if err != nil {
		t.Fatal(err)
	}
	r.Expected, err = installruntime.ReadPolicySnapshot(ctx, a.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	tryAdmission(t, ctx, a, r, Duplicate)
	a.TimePolicy.ComparisonBoundNS = int64(2 * time.Second)
	clock.sample.UncertaintyNS = a.TimePolicy.ComparisonBoundNS
	clock.sample.Fence = a.TimePolicy.Fence(clock.sample.BootID, clock.sample.Domain)
	r.Provenance = freshProvenance(clock.sample)
	tryAdmission(t, ctx, a, r, Duplicate) // trusted total comparison bound remains qualified
}

// Catches trusting sender kind/uncertainty, stale native facts, and extending
// original ingress/spawn/deadline facts after lease or store contention.
func TestAdmissionIndependentTimeBounds(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*AdmissionRequest, *fixtureAuthority)
		want   AdmissionStatus
	}{
		{"native60", func(r *AdmissionRequest, c *fixtureAuthority) {
			r.Provenance.NativeCreatedNS = c.sample.WallNS - int64(60*time.Second)
		}, Admitted},
		{"native61", func(r *AdmissionRequest, c *fixtureAuthority) {
			r.Provenance.NativeCreatedNS = c.sample.WallNS - int64(60*time.Second) - 1
		}, Expired},
		{"future2", func(r *AdmissionRequest, c *fixtureAuthority) {
			r.Provenance.NativeCreatedNS = c.sample.WallNS + int64(2*time.Second)
		}, Admitted},
		{"futureMore", func(r *AdmissionRequest, c *fixtureAuthority) {
			r.Provenance.NativeCreatedNS = c.sample.WallNS + int64(2*time.Second) + 1
		}, Expired},
		{"queue30", func(r *AdmissionRequest, c *fixtureAuthority) {
			r.Provenance.Clock.TickNS = c.sample.TickNS - int64(31*time.Second)
			r.Provenance.Clock.WallNS = c.sample.WallNS - int64(31*time.Second)
			r.Provenance.IngressTickNS = r.Provenance.Clock.TickNS
			r.Provenance.EpochStartedTickNS = r.Provenance.Clock.TickNS
		}, Admitted},
		{"queueOver30", func(r *AdmissionRequest, c *fixtureAuthority) {
			r.Provenance.Clock.TickNS = c.sample.TickNS - int64(31*time.Second) - 1
			r.Provenance.Clock.WallNS = c.sample.WallNS - int64(31*time.Second) - 1
			r.Provenance.IngressTickNS = r.Provenance.Clock.TickNS
			r.Provenance.EpochStartedTickNS = r.Provenance.Clock.TickNS
		}, Expired},
		{"deadline", func(r *AdmissionRequest, c *fixtureAuthority) { r.Provenance.DeadlineTickNS = c.sample.TickNS }, Expired},
		{"extension", func(r *AdmissionRequest, c *fixtureAuthority) { r.Provenance.DeadlineTickNS++ }, TimeUnverified},
		{"oldCommand", func(r *AdmissionRequest, c *fixtureAuthority) { r.CommandStarted = time.Now().Add(-21 * time.Second) }, Expired},
		{"boot", func(r *AdmissionRequest, c *fixtureAuthority) { r.Provenance.Clock.BootID = "previous-boot" }, TimeUnverified},
		{"domain", func(r *AdmissionRequest, c *fixtureAuthority) { r.Provenance.Clock.Domain = "process-relative" }, TimeUnverified},
		{"kind", func(r *AdmissionRequest, c *fixtureAuthority) {
			c.sample.Kind = "unqualified"
			r.Provenance.Clock.Kind = "continuous"
		}, TimeUnverified},
		{"fence", func(r *AdmissionRequest, c *fixtureAuthority) { r.Provenance.Clock.Fence = "previous-calibration" }, TimeUnverified},
		{"uncertainty", func(r *AdmissionRequest, c *fixtureAuthority) {
			c.sample.UncertaintyNS = int64(2*time.Second) + 1
			r.Provenance.Clock.UncertaintyNS = int64(time.Second)
		}, TimeUnverified},
		{"senderAllowance", func(r *AdmissionRequest, c *fixtureAuthority) {
			c.sample.WallNS += int64(time.Second)
			r.Provenance.Clock.UncertaintyNS = int64(time.Second)
		}, TimeUnverified},
		{"wallJump", func(r *AdmissionRequest, c *fixtureAuthority) { c.sample.WallNS += int64(time.Minute) }, TimeUnverified},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, a, r, c := admissionFixture(t)
			tc.mutate(&r, c)
			tryAdmission(t, ctx, a, r, tc.want)
		})
	}
}

// Catches destructive uncertainty eviction, forward wall expiry, and failure to
// persist a conservative new-boot/domain/fence baseline at full capacity.
func TestAdmissionCapacityAndVerifiedRetention(t *testing.T) {
	for _, transition := range []string{"sameBoot", "newBoot", "newDomain", "newFence"} {
		t.Run(transition, func(t *testing.T) {
			ctx, a, r, c := admissionFixture(t)
			// Each admission below models a separate command, with its own
			// unchanged deadline; filesystem setup is not part of that command.
			admit := func(want AdmissionStatus) {
				commandCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				request := r
				request.CommandStarted = time.Now()
				tryAdmission(t, commandCtx, a, request, want)
			}
			reg := *r.Expected.Installation.Ledger.Consumers["opencode-notifications"].OpenCode
			// Independent external store fixture: no product claim-key generator.
			rows := make([]map[string]any, 4096)
			for i := range rows {
				key := sha256.Sum256([]byte(fmt.Sprintf("external-uncertain-record-%d", i)))
				rows[i] = map[string]any{"Key": hex.EncodeToString(key[:]), "TickNS": c.sample.TickNS, "UncertaintyNS": 0, "NativeReadBoundNS": 0, "State": "claimed_unknown", "Generation": 1}
				if transition == "sameBoot" {
					rows[i]["NativeReadBoundNS"] = 1
				}
			}
			payload, _ := json.Marshal(map[string]any{"Version": 2, "Checkpoint": c.sample, "NativeReadBoundNS": 0, "Records": rows})
			store, err := installruntime.AcquireOpenCodeStore(ctx, a.ControlRoot, reg)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.Write(payload); err != nil {
				t.Fatal(err)
			}
			store.Close()
			admit(Capacity)
			advance := func(d time.Duration) {
				c.sample.TickNS += int64(d)
				c.sample.WallNS += int64(d)
				r.Provenance = freshProvenance(c.sample)
			}
			switch transition {
			case "newBoot":
				c.sample.BootID = "next-boot"
				c.sample.TickNS = int64(time.Hour)
				r.Provenance = freshProvenance(c.sample)
			case "newDomain":
				c.sample.Domain = "another-qualified-domain"
				c.sample.TickNS = int64(time.Hour)
				r.Provenance = freshProvenance(c.sample)
			case "newFence":
				a.TimePolicy.ProfileID = "TEST-next-qualified-policy"
				c.sample.WallNS += int64(48 * time.Hour)
				r.Provenance = freshProvenance(c.sample)

			}
			c.sample.Fence = a.TimePolicy.Fence(c.sample.BootID, c.sample.Domain)
			r.Provenance = freshProvenance(c.sample)
			admit(Capacity)
			advance(24*time.Hour - time.Nanosecond)
			admit(Capacity)
			advance(time.Nanosecond)
			if transition == "sameBoot" {
				admit(Capacity)
				advance(time.Nanosecond)
			}
			admit(Admitted)
			admit(Duplicate)
		})
	}
}

// Catches an old loaded observer borrowing a new incarnation at identical paths,
// even when another consumer retains the same ledger ID across removal.
func TestAdmissionOldOriginAfterSharedReinstall(t *testing.T) {
	ctx, a, r, _ := admissionFixture(t)
	tryAdmission(t, ctx, a, r, Admitted)
	l := r.Expected.Installation.Ledger
	consumer := l.Consumers["opencode-notifications"]
	if _, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: a.ControlRoot, RuntimeRoot: l.RuntimeRoot, Owner: l.Owner, ConsumerID: "shared-fixture", Consumer: installruntime.Consumer{Registration: "retained-fixture"}, ExpectedGeneration: &l.Generation}); err != nil {
		t.Fatal(err)
	}
	if err := opencodeinstall.RevokeChannels(ctx, a.ControlRoot, l.RuntimeRoot); err != nil {
		t.Fatal(err)
	}
	l, _, _ = installruntime.ReadOwnership(a.ControlRoot)
	if _, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: a.ControlRoot, RuntimeRoot: l.RuntimeRoot, Owner: l.Owner, ConsumerID: "opencode-notifications", RemoveConsumer: true, ExpectedGeneration: &l.Generation, Files: []installruntime.File{{Path: consumer.Registration, Before: l.Files[consumer.Registration], Remove: true}}}); err != nil {
		t.Fatal(err)
	}
	l, _, _ = installruntime.ReadOwnership(a.ControlRoot)
	reg := *consumer.OpenCode
	reg.Origin = strings.Repeat("44", 32)
	reg.Salt = strings.Repeat("55", 32)
	reg.Namespace = strings.Repeat("66", 32)
	consumer.OpenCode = &reg
	bundle := []byte("inert new-incarnation bundle")
	sum := sha256.Sum256(bundle)
	reg.BundleSHA256 = hex.EncodeToString(sum[:])
	l, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: a.ControlRoot, RuntimeRoot: l.RuntimeRoot, Owner: l.Owner, ConsumerID: "opencode-notifications", Consumer: consumer, ExpectedGeneration: &l.Generation, Files: []installruntime.File{{Path: consumer.Registration, Data: bundle, Mode: 0600}}})
	if err != nil {
		t.Fatal(err)
	}
	if l.ID != r.Expected.Installation.Ledger.ID {
		t.Fatal("shared ledger changed")
	}
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: a.ControlRoot, RuntimeRoot: l.RuntimeRoot, Owner: l.Owner, ConsumerID: "opencode-notifications", RefreshOnly: true, PolicyOnly: true, ExpectedGeneration: &l.Generation, PolicyFields: map[string]json.RawMessage{"route": json.RawMessage(`{"openCodeNotifications":{"desktop":true,"webhook":true}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	r.Expected, err = installruntime.ReadPolicySnapshot(ctx, a.ControlRoot)
	if err != nil {
		t.Fatal(err)
	}
	tryAdmission(t, ctx, a, r, SnapshotChanged)
	r.Origin = reg.Origin
	tryAdmission(t, ctx, a, r, Admitted)
}

// Catches refreshing a sender budget after store contention, or leaking outer
// leases on cancellation. No claim is written before the original deadline.
func TestAdmissionStoreContentionExpiresOriginalBudget(t *testing.T) {
	ctx, a, r, c := admissionFixture(t)
	r.Provenance.DeadlineTickNS = c.sample.TickNS + int64(40*time.Millisecond)
	release, err := installruntime.LockExisting(ctx, filepath.Join(a.ControlRoot, installruntime.OpenCodeStoreLock))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	tryAdmission(t, ctx, a, r, Expired)
	release()
	r.Provenance = freshProvenance(c.sample)
	tryAdmission(t, ctx, a, r, Admitted)
}

// Catches event-time state repair/adoption and claims against revoked, mismatched
// config, recovery, expired contexts, or origin from a previous incarnation.
func TestAdmissionSuppressionLeavesClaimsUntouched(t *testing.T) {
	for _, mode := range []string{"originless", "oldOrigin", "missing", "corrupt", "symlink", "revoked", "configChanged", "configSnapshotMutation", "recovery", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, a, r, _ := admissionFixture(t)
			l := r.Expected.Installation.Ledger
			reg := *l.Consumers["opencode-notifications"].OpenCode
			path := filepath.Join(a.ControlRoot, "opencode-admission", reg.Namespace, "claims.json")
			want := StoreUnavailable
			switch mode {
			case "originless":
				r.Origin = ""
				want = NotRegistered
			case "oldOrigin":
				r.Origin = strings.Repeat("44", 32)
				want = SnapshotChanged
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(path, []byte(`{"broken":true}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				foreign := filepath.Join(t.TempDir(), "foreign")
				if err := os.WriteFile(foreign, []byte("sentinel"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(foreign, path); err != nil {
					t.Skip("symlinks unavailable", err)
				}
			case "revoked":
				if err := opencodeinstall.RevokeChannels(ctx, a.ControlRoot, l.RuntimeRoot); err != nil {
					t.Fatal(err)
				}
				want = SnapshotChanged
			case "configChanged":
				if err := os.WriteFile(filepath.Join(a.ControlRoot, "agent-notifications.json"), []byte(`{"schemaVersion":1,"enabled":false}`), 0600); err != nil {
					t.Fatal(err)
				}
				want = SnapshotChanged
			case "configSnapshotMutation":
				r.Expected.Fields["route"] = json.RawMessage(`{"openCodeNotifications":{"desktop":true,"webhook":false}}`)
				want = SnapshotChanged
			case "recovery":
				_, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: a.ControlRoot, RuntimeRoot: l.RuntimeRoot, Owner: l.Owner, ConsumerID: "opencode-notifications", RefreshOnly: true, ExpectedGeneration: &l.Generation, Fault: func(string) error { return fmt.Errorf("interrupted install") }})
				if err == nil {
					t.Fatal("fault missed")
				}
				want = SnapshotChanged
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
				want = Expired
			}
			before, _ := os.ReadFile(path)
			tryAdmission(t, ctx, a, r, want)
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("suppressed admission mutated private state")
			}
		})
	}
}

// Actual subprocesses use the same public Admission port and real permanent
// kernel locks. A crash after claim cannot retry; simultaneous callbacks share
// one outward handoff. The hold mode models bounded IO with filesystem barriers.
func TestAdmissionProcess(t *testing.T) {
	if root := os.Getenv("E1_ADMISSION_ROOT"); root != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s, err := installruntime.ReadPolicySnapshot(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		c := s.Installation.Ledger.Consumers["opencode-notifications"]
		clock := &fixtureAuthority{authoritySample()}
		a := Admission{ControlRoot: root, Clock: clock, TimePolicy: fixtureTimePolicy()}
		r := AdmissionRequest{Fact: FactIdentity{Kind: "permission_asked", Session: "session-independent", Execution: "execution-19", Request: "request-5", Root: true}, Origin: c.OpenCode.Origin, Provenance: freshProvenance(clock.sample), Expected: s, Executable: c.Commands[0], GOOS: "linux", GOARCH: "amd64", CommandStarted: time.Now()}
		h, status := a.Admit(ctx, r)
		if h != nil {
			defer h.Close()
			switch os.Getenv("E1_ADMISSION_MODE") {
			case "crash":
				os.Exit(73)
			case "hold":
				if err := os.WriteFile(filepath.Join(root, "ready"), nil, 0600); err != nil {
					t.Fatal(err)
				}
				awaitFile(t, ctx, filepath.Join(root, "release"))
			default:
				f, err := os.OpenFile(filepath.Join(root, "effect"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					t.Fatal("duplicate handoff", err)
				}
				_ = f.Close()
			}
		}
		fmt.Println("E1-status=" + string(status))
		return
	}
	for _, mode := range []string{"concurrent", "crash", "hold"} {
		t.Run(mode, func(t *testing.T) {
			ctx, a, r, _ := admissionFixture(t)
			child := func(mode string) *exec.Cmd {
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAdmissionProcess$")
				cmd.Dir = a.ControlRoot
				cmd.Env = []string{"E1_ADMISSION_ROOT=" + a.ControlRoot, "E1_ADMISSION_MODE=" + mode}
				return cmd
			}
			switch mode {
			case "concurrent":
				commands := make([]*exec.Cmd, 6)
				outputs := make([]strings.Builder, 6)
				for i := range commands {
					commands[i] = child("once")
					commands[i].Stdout = &outputs[i]
					commands[i].Stderr = &outputs[i]
					if err := commands[i].Start(); err != nil {
						t.Fatal(err)
					}
				}
				admitted := 0
				for i, cmd := range commands {
					if err := cmd.Wait(); err != nil {
						t.Fatalf("child: %v %s", err, outputs[i].String())
					}
					if strings.Contains(outputs[i].String(), "E1-status=admitted") {
						admitted++
					}
				}
				if admitted != 1 {
					t.Fatalf("outward handoffs=%d", admitted)
				}
				tryAdmission(t, ctx, a, r, Duplicate)
			case "crash":
				out, err := child("crash").CombinedOutput()
				exit, ok := err.(*exec.ExitError)
				if !ok || exit.ExitCode() != 73 {
					t.Fatalf("crash: %v %s", err, out)
				}
				tryAdmission(t, ctx, a, r, Duplicate)
			case "hold":
				cmd := child("hold")
				var out strings.Builder
				cmd.Stdout = &out
				cmd.Stderr = &out
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				awaitFile(t, ctx, filepath.Join(a.ControlRoot, "ready"))
				short, cancel := context.WithTimeout(ctx, 60*time.Millisecond)
				reg := *r.Expected.Installation.Ledger.Consumers["opencode-notifications"].OpenCode
				store, err := installruntime.AcquireOpenCodeStore(short, a.ControlRoot, reg)
				if err != nil {
					t.Fatal("IO holds store lock", err)
				}
				store.Close()
				if err = opencodeinstall.RevokeChannels(short, a.ControlRoot, r.Expected.Installation.Ledger.RuntimeRoot); err == nil {
					t.Fatal("revocation crossed active IO fence")
				}
				cancel()
				short, cancel = context.WithTimeout(ctx, 60*time.Millisecond)
				tryAdmission(t, short, a, r, Expired)
				cancel()
				if err = os.WriteFile(filepath.Join(a.ControlRoot, "release"), nil, 0600); err != nil {
					t.Fatal(err)
				}
				if err = cmd.Wait(); err != nil {
					t.Fatalf("held child: %v %s", err, out.String())
				}
				if err = opencodeinstall.RevokeChannels(ctx, a.ControlRoot, r.Expected.Installation.Ledger.RuntimeRoot); err != nil {
					t.Fatal(err)
				}
				r.Fact.Request = "fresh-after-revoke"
				tryAdmission(t, ctx, a, r, SnapshotChanged)
				l, _, _ := installruntime.ReadOwnership(a.ControlRoot)
				if _, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: a.ControlRoot, RuntimeRoot: l.RuntimeRoot, Owner: l.Owner, ConsumerID: "opencode-notifications", RemoveConsumer: true, ExpectedGeneration: &l.Generation}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
func awaitFile(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("process barrier deadline")
		case <-ticker.C:
		}
	}
}
