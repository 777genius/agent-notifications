package pievent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/analyzer"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/notification/observation"
	"github.com/777genius/agent-notifications/internal/pisource"
	"github.com/777genius/agent-notifications/internal/webhook"
	"github.com/google/uuid"
)

type fixedClock struct{}

func (fixedClock) Now() (string, float64, error) { return "TEST-boot", 100, nil }

type testGate struct {
	channels            Channels
	revoke              bool
	changeAfterChannels bool
	acquired, released  int
	revision            pisource.Revision
}

func (g *testGate) Channels(_ context.Context, _ Binding, r pisource.Revision) Channels {
	if r != g.revision {
		return Channels{}
	}
	out := g.channels
	if g.changeAfterChannels {
		g.revoke = true
	}
	return out
}
func (g *testGate) Acquire(ctx context.Context, _ Binding, r pisource.Revision, _ Channel) (func(), error) {
	if ctx.Err() != nil || g.revoke || r != g.revision {
		return nil, errors.New("revoked")
	}
	g.acquired++
	return func() { g.released++ }, nil
}
func fixture(t *testing.T) (Consumer, pisource.Facts, *testGate) {
	t.Helper()
	root := t.TempDir()
	cache := filepath.Join(root, "observations")
	if err := os.Mkdir(cache, 0700); err != nil {
		t.Fatal(err)
	}
	r := pisource.Revision{ProjectionDigest: "sha256:" + strings.Repeat("1", 64), CanonicalDigest: "sha256:" + strings.Repeat("2", 64), BootstrapVersion: 1, BindingID: "binding", RuntimeRoot: root, PrimarySHA256: strings.Repeat("3", 64)}
	f := pisource.Facts{Kind: pisource.Completed, ObserverID: uuid.NewString(), ObservationID: uuid.NewString(), SessionID: "native-session", CWD: root, Mode: "tui", PiPID: 123, Revision: r}
	g := &testGate{channels: Channels{Desktop: true, Webhook: true}, revision: r}
	cfg := config.DefaultConfig()
	cfg.Notifications.Webhook.Enabled = true
	cfg.Notifications.Webhook.URL = "https://example.test/PRIVATE-SENTINEL"
	c := Consumer{Binding: Binding{InstallationID: "install", BindingID: "binding", Generation: 1}, Gate: g, Config: cfg, LocalTerminal: true, Clock: fixedClock{}, Cache: &observation.RecentCache{Root: cache, Clock: fixedClock{}}}
	return c, f, g
}
func deadline() notification.Deadline {
	return notification.Deadline{BootID: "TEST-boot", NotAfter: 115}
}
func TestPiModesAndNativeEventMeaning(t *testing.T) {
	for _, mode := range []string{"tui", "rpc", "json", "print"} {
		for _, kind := range []pisource.Kind{pisource.Completed, pisource.Failed, pisource.Question, pisource.Aborted, pisource.Unclassified} {
			t.Run(mode+"/"+string(kind), func(t *testing.T) {
				c, f, g := fixture(t)
				f.Mode, f.Kind = mode, kind
				desktops, webhooks := 0, 0
				c.Desktop = func(_ context.Context, r DesktopRequest) error {
					desktops++
					if r.CWD != f.CWD || r.SessionID != f.SessionID {
						t.Fatal("native context lost")
					}
					if kind == pisource.Question && r.Status != analyzer.StatusQuestion {
						t.Fatal("UI prompt became permission")
					}
					return nil
				}
				c.SendWebhook = func(_ context.Context, cfg *config.Config, m webhook.SendContext) error {
					webhooks++
					if cfg.Notifications.Webhook.Retry.Enabled || cfg.Notifications.Webhook.Headers != nil || cfg.Notifications.Webhook.PayloadFields != nil {
						t.Fatal("private webhook transport widened")
					}
					if m.AgentSource != "pi" || m.SessionID != "" || m.CWD != "" || strings.Contains(m.Message, "PRIVATE") {
						t.Fatalf("private message: %+v", m)
					}
					return nil
				}
				r := c.Consume(context.Background(), f, deadline())
				supported := kind != pisource.Aborted && kind != pisource.Unclassified
				wantD, wantW := 0, 0
				if supported {
					wantW = 1
					if mode == "tui" {
						wantD = 1
					}
				}
				if desktops != wantD || webhooks != wantW || g.acquired != g.released {
					t.Fatalf("%+v effects=%d/%d leases=%d/%d", r, desktops, webhooks, g.acquired, g.released)
				}
				if supported && r.Status != "submitted" {
					t.Fatalf("%+v", r)
				}
			})
		}
	}
}
func TestPiClaimsEachChannelOnceAndRechecksRevision(t *testing.T) {
	c, f, g := fixture(t)
	d, w := 0, 0
	c.Desktop = func(context.Context, DesktopRequest) error { d++; return nil }
	c.SendWebhook = func(context.Context, *config.Config, webhook.SendContext) error { w++; return nil }
	if r := c.Consume(context.Background(), f, deadline()); r.Status != "submitted" {
		t.Fatal(r)
	}
	if r := c.Consume(context.Background(), f, deadline()); r.Desktop != "duplicate" || r.Webhook != "duplicate" {
		t.Fatal(r)
	}
	if d != 1 || w != 1 || g.acquired != 2 || g.released != 2 {
		t.Fatalf("duplicate effects=%d/%d", d, w)
	}
	f.ObservationID = uuid.NewString()
	g.changeAfterChannels = true
	if r := c.Consume(context.Background(), f, deadline()); r.Desktop != "revoked" || r.Webhook != "revoked" {
		t.Fatal(r)
	}
	if d != 1 || w != 1 {
		t.Fatal("revoked binding had effects")
	}
}
func TestPiRemoteDesktopAndExpiredBudgetHaveNoEffect(t *testing.T) {
	c, f, g := fixture(t)
	c.LocalTerminal = false
	c.Desktop = func(context.Context, DesktopRequest) error { t.Fatal("remote desktop"); return nil }
	c.SendWebhook = func(context.Context, *config.Config, webhook.SendContext) error { return nil }
	if r := c.Consume(context.Background(), f, deadline()); r.Desktop != "" || r.Webhook != "submitted" {
		t.Fatal(r)
	}
	f.ObservationID = uuid.NewString()
	if r := c.Consume(context.Background(), f, notification.Deadline{BootID: "TEST-boot", NotAfter: 99}); r.Reason != "expired" {
		t.Fatal(r)
	}
	if g.acquired != 1 {
		t.Fatal("expired invocation acquired effect lease")
	}
}
