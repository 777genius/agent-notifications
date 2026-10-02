package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/777genius/agent-notifications/internal/config"
	local "github.com/777genius/agent-notifications/internal/copilotvscodeevent"
	source "github.com/777genius/agent-notifications/internal/copilotvscodesource"
	"github.com/777genius/agent-notifications/internal/notification/observation"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notifier/nativeprotocol"
)

// These tests use a real retained kernel lease, profile file and private spool.
// The process seam never launches a native application or claims physical P2.
type localBoundaryLease struct {
	profile          string
	release          func()
	released         bool
	before, complete int
}

func (l *localBoundaryLease) BundlePath() string     { return "/TEST/Notifier.app" }
func (l *localBoundaryLease) ExecutablePath() string { return "/TEST/Notifier.app/helper" }
func (l *localBoundaryLease) Release()               { l.released = true; l.release() }
func (l *localBoundaryLease) check() error {
	if l.released {
		return errors.New("authority checked after release")
	}
	raw, err := os.ReadFile(l.profile)
	if err != nil || string(raw) != "owned-profile" {
		return errors.New("physical profile drift")
	}
	return nil
}
func (l *localBoundaryLease) BeforeHandoff(context.Context) error { l.before++; return l.check() }
func (l *localBoundaryLease) Complete(context.Context) error      { l.complete++; return l.check() }

type localBoundaryInstallation struct {
	root     string
	snapshot installruntime.InstalledSnapshot
	lease    *localBoundaryLease
	acquires int
}

func (i *localBoundaryInstallation) Acquire(ctx context.Context) (NativeLease, error) {
	i.acquires++
	_, release, err := installruntime.AcquireSetupLease(ctx, i.root, i.snapshot)
	if err != nil {
		return nil, err
	}
	i.lease.release = release
	return i.lease, nil
}

type localBoundarySpool struct {
	*PrivateNativeSpool
	drift    func()
	prepared NativeAttempt
	expires  int
}

func (s *localBoundarySpool) Prepare(ctx context.Context, r notification.Request, encode func(string) ([]byte, error)) (NativeAttempt, error) {
	a, err := s.PrivateNativeSpool.Prepare(ctx, r, encode)
	s.prepared = a
	if err == nil && s.drift != nil {
		s.drift()
	}
	return a, err
}
func (s *localBoundarySpool) Expire(a NativeAttempt) error {
	s.expires++
	return s.PrivateNativeSpool.Expire(a)
}

type localBoundaryProcess struct {
	launches  int
	drift     func()
	failProbe bool
}

func (p *localBoundaryProcess) Probe(context.Context, string) ([]byte, error) {
	if p.failProbe {
		return nil, errors.New("probe unavailable")
	}
	return []byte(pr3Caps), nil
}
func (p *localBoundaryProcess) ProbePermission(_ context.Context, _ string, c, n string) ([]byte, error) {
	return json.Marshal(nativeprotocol.Permission{SchemaVersion: 1, CorrelationID: c, Nonce: n, Backend: "macos.usernotifications", Permission: "allowed"})
}
func (p *localBoundaryProcess) Launch(_ context.Context, _ string, request, receipt string) (bool, error) {
	p.launches++
	raw, err := os.ReadFile(request)
	if err != nil {
		return false, err
	}
	var wire nativeprotocol.Request
	if err := json.Unmarshal(raw, &wire); err != nil {
		return false, err
	}
	out, err := json.Marshal(nativeprotocol.Receipt{SchemaVersion: 1, CorrelationID: wire.CorrelationID, Nonce: wire.Nonce, NotificationID: wire.CorrelationID, Status: "submitted", Reason: "os_accepted"})
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(receipt, out, 0600); err != nil {
		return true, err
	}
	if p.drift != nil {
		p.drift()
	}
	return true, nil
}

// RED on the old delivery: drift after Prepare still launches; completion after
// a successful receipt is never checked. The same original nonce/deadline and
// handed-off spool must survive uncertainty, with exactly one acquired lease.
func TestLocalAuthorityAtRetainedNativeBoundary(t *testing.T) {
	for _, phase := range []string{"before", "after", "probe-return"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			base := t.TempDir()
			root := filepath.Join(base, "control")
			runtime := filepath.Join(base, "runtime")
			if err := os.Mkdir(runtime, 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := installruntime.Commit(ctx, installruntime.Request{ControlRoot: root, RuntimeRoot: runtime, Owner: "TEST", ConsumerID: "TEST", Consumer: installruntime.Consumer{Registration: "TEST"}}); err != nil {
				t.Fatal(err)
			}
			snapshot, err := installruntime.ReadInstalledSnapshot(root)
			if err != nil {
				t.Fatal(err)
			}
			profile := filepath.Join(base, "profile")
			if err := os.WriteFile(profile, []byte("owned-profile"), 0600); err != nil {
				t.Fatal(err)
			}
			lease := &localBoundaryLease{profile: profile}
			installation := &localBoundaryInstallation{root: root, snapshot: snapshot, lease: lease}
			clock := &pr3Clock{now: 100}
			spoolRoot := filepath.Join(base, "spool")
			if err := os.Mkdir(spoolRoot, 0700); err != nil {
				t.Fatal(err)
			}
			spool := &localBoundarySpool{PrivateNativeSpool: &PrivateNativeSpool{Root: spoolRoot, Clock: clock}}
			drift := func() {
				if err := os.WriteFile(profile, []byte("foreign-profile"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			process := &localBoundaryProcess{}
			if phase == "before" {
				spool.drift = drift
			}
			if phase == "after" {
				process.drift = drift
			}
			if phase == "probe-return" {
				process.failProbe = true
			}
			delivery := &StructuredDelivery{Installation: installation, Clock: clock, Process: process, Spool: spool}
			request := pr3Request()
			request.Navigation = notification.None
			request.Target = notification.DesktopTarget{}
			request.Deadline.NotAfter = 104 // Original four-second Local admission.
			var got notification.Receipt
			if phase == "after" {
				document, err := config.ParseDocument([]byte(`{"schemaVersion":2,"notifications":{"desktop":{"enabled":true}}}`), "/TEST/config", false)
				if err != nil {
					t.Fatal(err)
				}
				cfg, err := document.Effective(config.AssetContext{Agent: config.AgentCopilotVSCode, PluginRoot: "/TEST"})
				if err != nil {
					t.Fatal(err)
				}
				facts, err := source.Decode(ctx, source.Stop, []byte(`{"hook_event_name":"Stop","timestamp":"2026-10-02T06:45:01Z","stop_hook_active":false,"session_id":"TEST-retained-claim"}`))
				if err != nil {
					t.Fatal(err)
				}
				cacheRoot := filepath.Join(base, "claims")
				if err := os.Mkdir(cacheRoot, 0700); err != nil {
					t.Fatal(err)
				}
				consumer := local.Consumer{Binding: local.Binding{InstallationID: "TEST-install", BindingID: "TEST-binding", ProfileIdentity: "TEST-profile", Product: "copilot-vscode", Generation: 1}, Gate: localBoundaryGate{profile: profile}, Config: cfg, Desktop: delivery, Clock: clock, Cache: &observation.RecentCache{Root: cacheRoot, Clock: clock}}
				receipt := consumer.Consume(ctx, facts, request.Deadline)
				if receipt.Status != "unknown" {
					t.Fatalf("completion drift was not uncertain at real consumer: %+v", receipt)
				}
				got = notification.Receipt{Status: receipt.Status}
				entries, err := os.ReadDir(cacheRoot)
				if err != nil {
					t.Fatal(err)
				}
				retained := map[string][]byte{}
				for _, entry := range entries {
					raw, err := os.ReadFile(filepath.Join(cacheRoot, entry.Name()))
					if err != nil {
						t.Fatal(err)
					}
					retained[entry.Name()] = raw
				}
				if err := os.WriteFile(profile, []byte("owned-profile"), 0600); err != nil {
					t.Fatal(err)
				}
				again := consumer.Consume(ctx, facts, request.Deadline)
				if again.Status != "suppressed" || installation.acquires != 1 || process.launches != 1 {
					t.Fatalf("restored profile resent the retained attempt: %+v acquires=%d launches=%d", again, installation.acquires, process.launches)
				}
				for name, want := range retained {
					raw, err := os.ReadFile(filepath.Join(cacheRoot, name))
					if err != nil || !bytes.Equal(raw, want) {
						t.Fatal("uncertain claim/TTL changed across repeat")
					}
				}
			} else {
				got = delivery.Deliver(ctx, request)
			}
			if installation.acquires != 1 || !lease.released || lease.complete != 1 {
				t.Fatalf("lease finalization/acquisition: acquired=%d released=%v complete=%d", installation.acquires, lease.released, lease.complete)
			}
			if phase == "before" {
				if process.launches != 0 || lease.before != 1 || got.Status == "submitted" || spool.expires != 1 {
					t.Fatalf("pre-drift caused effect or retained unlaunched attempt: %+v launches=%d expires=%d", got, process.launches, spool.expires)
				}
			} else if phase == "after" {
				if process.launches != 1 || lease.before != 1 || got.Status != "unknown" || got.RetrySafe || spool.expires != 0 {
					t.Fatalf("post-drift lost uncertainty/ownership: %+v launches=%d expires=%d", got, process.launches, spool.expires)
				}
				raw, err := os.ReadFile(spool.prepared.RequestPath)
				if err != nil {
					t.Fatal(err)
				}
				var wire nativeprotocol.Request
				if err := json.Unmarshal(raw, &wire); err != nil {
					t.Fatal(err)
				}
				if wire.Nonce != spool.prepared.Nonce || wire.NotAfter != request.Deadline.NotAfter {
					t.Fatal("nonce/deadline renewed")
				}
				// Expire is the caller-owned cleanup primitive, not a callback timer.
				// Delivery must retain ownership until the original deadline.
				if _, err := os.Stat(spool.prepared.Directory); err != nil {
					t.Fatal("handed-off attempt was removed early")
				}
				clock.set(111)
				if err := spool.PrivateNativeSpool.Expire(spool.prepared); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(spool.prepared.Directory); !os.IsNotExist(err) {
					t.Fatal("owned attempt survived expiry")
				}
			} else if process.launches != 0 || lease.before != 0 {
				t.Fatal("early return reached handoff")
			}
			// A released span must not leave the real component lock pinned.
			if _, release, err := installruntime.AcquireSetupLease(ctx, root, snapshot); err != nil {
				t.Fatal(err)
			} else {
				release()
			}
		})
	}
}

// Consumer admission checks a real profile file. It does not mint N2 physical
// proof: this fixture exercises only the accepted N1/lease/spool/claim boundary.
type localBoundaryGate struct{ profile string }

func (g localBoundaryGate) Channels(context.Context, local.Binding) local.Channels {
	raw, err := os.ReadFile(g.profile)
	return local.Channels{Desktop: err == nil && string(raw) == "owned-profile"}
}
func (g localBoundaryGate) Recheck(ctx context.Context, b local.Binding, _ local.Channel) bool {
	return g.Channels(ctx, b).Desktop
}
