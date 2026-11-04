//go:build linux

package notifier

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestAgentNotifyEmbeddedIconIsBoundedAndImmutable(t *testing.T) {
	icon, ok := agentNotifyImageData()
	if !ok || icon.Width != 64 || icon.Height != 64 || icon.Rowstride != 256 ||
		!icon.HasAlpha || icon.BitsPerSample != 8 || icon.Channels != 4 || len(icon.Data) != 64*64*4 {
		t.Fatalf("invalid freedesktop image: ok=%t w=%d h=%d stride=%d channels=%d bytes=%d", ok, icon.Width, icon.Height, icon.Rowstride, icon.Channels, len(icon.Data))
	}
	if got := dbus.MakeVariant(icon).Signature().String(); got != "(iiibiiay)" {
		t.Fatalf("invalid outgoing image-data D-Bus signature: %s", got)
	}
	visible, uniform := 0, true
	for i := 0; i+3 < len(icon.Data); i += 4 {
		if icon.Data[i+3] != 0 {
			visible++
		}
		if icon.Data[i] != icon.Data[0] || icon.Data[i+1] != icon.Data[1] ||
			icon.Data[i+2] != icon.Data[2] || icon.Data[i+3] != icon.Data[3] {
			uniform = false
		}
	}
	if visible == 0 || uniform {
		t.Fatalf("embedded icon lacks visible pixel variation: visible=%d uniform=%t", visible, uniform)
	}
}

// An actual private D-Bus Notify service observes presentation bytes, policy
// opt-outs and exactly one external effect per request. Other freedesktop
// clients keep their legacy presentation; no app-icon file is written/read.
func TestFreedesktopAgentNotifyPresentationOnPrivateSessionBus(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", startTestSessionBus(t))
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	server := &testNotifications{capabilities: []string{"body", "sound"}}
	if _, err := conn.RequestName("org.freedesktop.Notifications", dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	if err := conn.Export(server, "/org/freedesktop/Notifications", "org.freedesktop.Notifications"); err != nil {
		t.Fatal(err)
	}
	clock := &pr3Clock{now: 100}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i, tc := range []struct {
		name              string
		mcp, sound        bool
		silent            bool
		serverSupportsCue bool
	}{
		{"mcp enabled", true, true, false, true},
		{"mcp sound opt-out", true, false, true, true},
		{"mcp no system sound", true, true, false, false},
		{"non-mcp unchanged", false, true, false, true},
		{"mcp policy off, silent unset", true, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server.capabilitiesMu.Lock()
			if tc.serverSupportsCue {
				server.capabilities = []string{"body", "sound"}
			} else {
				server.capabilities = []string{"body"}
			}
			server.capabilitiesMu.Unlock()
			port := NewFreedesktopDelivery(clock)
			port.AgentNotifyPresentation = tc.mcp
			r := linuxNoneRequest(clock)
			r.Policy.SoundEnabled = tc.sound
			r.Silent = tc.silent
			if ready := port.CheckReadiness(ctx, r); ready.Status != "ready" || server.last.Load() != uint32(i) {
				t.Fatalf("readiness had side effects: %+v, notify=%d", ready, server.last.Load())
			}
			receipt := port.Deliver(ctx, r)
			if receipt.Status != "submitted" || receipt.Reason != "session_notification" || server.last.Load() != uint32(i+1) {
				t.Fatalf("D-Bus delivery: %+v, count=%d", receipt, server.last.Load())
			}
			hints := server.hints.Load().(map[string]dbus.Variant)
			icon := server.appIcon.Load().(string)
			image, hasImage := hints["image-data"]
			if tc.mcp {
				if icon != "" || !hasImage {
					t.Fatalf("missing immutable MCP icon: app=%q present=%t", icon, hasImage)
				}
				// godbus Export converts an incoming D-Bus struct to []interface{},
				// then Store reconstructs it as []Variant (signature "av").
				// Validate the actual received fields and bytes instead of that
				// reconstructed signature; the outgoing wire signature is checked above.
				fields, ok := image.Value().([]dbus.Variant)
				if !ok || len(fields) != 7 {
					t.Fatalf("malformed MCP image fields: type=%T", image.Value())
				}
				want, ok := agentNotifyImageData()
				if !ok {
					t.Fatal("missing embedded MCP image")
				}
				if fields[0].Value() != want.Width || fields[1].Value() != want.Height ||
					fields[2].Value() != want.Rowstride || fields[3].Value() != want.HasAlpha ||
					fields[4].Value() != want.BitsPerSample || fields[5].Value() != want.Channels {
					t.Fatal("MCP image metadata changed in D-Bus delivery")
				}
				pixels, ok := fields[6].Value().([]byte)
				if !ok || !bytes.Equal(pixels, want.Data) {
					t.Fatalf("MCP image bytes changed in D-Bus delivery: type=%T size=%d", fields[6].Value(), len(pixels))
				}
			} else if icon != "" || hasImage {
				t.Fatalf("changed another provider's icon: app=%q image=%t", icon, hasImage)
			}
			cue, hasCue := hints["sound-name"]
			if want := tc.mcp && tc.sound && tc.serverSupportsCue; hasCue != want || hasCue && cue.Value() != "message-new-instant" {
				t.Fatalf("unsound cue consent/capabilities: present=%t value=%v want=%t", hasCue, cue.Value(), want)
			}
			_, suppresses := hints["suppress-sound"]
			if suppresses != tc.silent {
				t.Fatalf("sound opt-out not preserved: suppressed=%t want=%t", suppresses, tc.silent)
			}
		})
	}
}
