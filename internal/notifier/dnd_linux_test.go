//go:build linux

package notifier

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// staticReader returns a reader that always answers (value, ok) and records that
// it ran. CI has no session bus, so every test drives evaluateDND through these
// injected readers rather than through real D-Bus.
func staticReader(value, ok bool, called *bool) dndReader {
	return func(context.Context) (bool, bool) {
		if called != nil {
			*called = true
		}
		return value, ok
	}
}

func TestEvaluateDND(t *testing.T) {
	tests := []struct {
		name    string
		desktop string
		readers dndReaders
		want    bool
	}{
		{
			name:    "inhibited wins",
			desktop: "kde",
			readers: dndReaders{inhibited: staticReader(true, true, nil)},
			want:    true,
		},
		{
			name:    "inhibited false and nothing else readable",
			desktop: "kde",
			readers: dndReaders{inhibited: staticReader(false, true, nil)},
			want:    false,
		},
		{
			name:    "unreadable inhibited does not mask dunst",
			desktop: "sway",
			readers: dndReaders{
				inhibited:   staticReader(true, false, nil), // value must be ignored when ok is false
				dunstPaused: staticReader(true, true, nil),
			},
			want: true,
		},
		{
			name:    "dunst not paused",
			desktop: "sway",
			readers: dndReaders{dunstPaused: staticReader(false, true, nil)},
			want:    false,
		},
		{
			name:    "xfce do-not-disturb on",
			desktop: "xfce",
			readers: dndReaders{xfceDND: staticReader(true, true, nil)},
			want:    true,
		},
		{
			name:    "xfce do-not-disturb off",
			desktop: "xfce",
			readers: dndReaders{xfceDND: staticReader(false, true, nil)},
			want:    false,
		},
		{
			name:    "gnome banners hidden means DND",
			desktop: "ubuntu:gnome",
			readers: dndReaders{gnomeBanners: staticReader(false, true, nil)},
			want:    true,
		},
		{
			name:    "gnome banners shown means no DND",
			desktop: "gnome",
			readers: dndReaders{gnomeBanners: staticReader(true, true, nil)},
			want:    false,
		},
		{
			name:    "gnome banners unreadable means no DND",
			desktop: "gnome",
			readers: dndReaders{gnomeBanners: staticReader(false, false, nil)},
			want:    false,
		},
		{
			name:    "no readers at all",
			desktop: "kde",
			readers: dndReaders{},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evaluateDND(context.Background(), tt.desktop, tt.readers); got != tt.want {
				t.Errorf("evaluateDND(%q) = %v, want %v", tt.desktop, got, tt.want)
			}
		})
	}
}

// TestEvaluateDND_DesktopGatesSlowProbes is the latency guarantee from the
// design: the GNOME probe shells out to gsettings and the XFCE probe talks to a
// second daemon, so neither may run on a desktop that cannot answer.
func TestEvaluateDND_DesktopGatesSlowProbes(t *testing.T) {
	tests := []struct {
		name      string
		desktop   string
		wantGnome bool
		wantXFCE  bool
	}{
		{name: "kde runs neither", desktop: "kde", wantGnome: false, wantXFCE: false},
		{name: "unknown desktop runs neither", desktop: "", wantGnome: false, wantXFCE: false},
		{name: "gnome runs only gsettings", desktop: "ubuntu:gnome", wantGnome: true, wantXFCE: false},
		{name: "unity runs only gsettings", desktop: "unity", wantGnome: true, wantXFCE: false},
		{name: "xfce runs only xfconf", desktop: "xfce", wantGnome: false, wantXFCE: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gnomeCalled, xfceCalled bool
			readers := dndReaders{
				gnomeBanners: staticReader(true, true, &gnomeCalled),
				xfceDND:      staticReader(false, true, &xfceCalled),
			}

			evaluateDND(context.Background(), tt.desktop, readers)

			if gnomeCalled != tt.wantGnome {
				t.Errorf("gsettings probe called = %v, want %v on desktop %q", gnomeCalled, tt.wantGnome, tt.desktop)
			}
			if xfceCalled != tt.wantXFCE {
				t.Errorf("xfconf probe called = %v, want %v on desktop %q", xfceCalled, tt.wantXFCE, tt.desktop)
			}
		})
	}
}

func TestEvaluateDND_StopsAtFirstPositive(t *testing.T) {
	var dunstCalled, gnomeCalled bool
	readers := dndReaders{
		inhibited:    staticReader(true, true, nil),
		dunstPaused:  staticReader(true, true, &dunstCalled),
		gnomeBanners: staticReader(false, true, &gnomeCalled),
	}

	if !evaluateDND(context.Background(), "gnome", readers) {
		t.Fatal("evaluateDND = false, want true when Inhibited is set")
	}
	if dunstCalled || gnomeCalled {
		t.Errorf("later probes ran after a positive signal: dunst=%v gnome=%v", dunstCalled, gnomeCalled)
	}
}

func TestEvaluateDND_ExpiredContextSkipsEveryReader(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var inhibitedCalled, dunstCalled, xfceCalled, gnomeCalled bool
	readers := dndReaders{
		inhibited:    staticReader(true, true, &inhibitedCalled),
		dunstPaused:  staticReader(true, true, &dunstCalled),
		xfceDND:      staticReader(true, true, &xfceCalled),
		gnomeBanners: staticReader(false, true, &gnomeCalled),
	}

	if evaluateDND(ctx, "xfce:gnome", readers) {
		t.Error("evaluateDND = true, want false once the probe deadline has passed")
	}
	if inhibitedCalled || dunstCalled || xfceCalled || gnomeCalled {
		t.Errorf("readers ran on an expired context: inhibited=%v dunst=%v xfce=%v gnome=%v",
			inhibitedCalled, dunstCalled, xfceCalled, gnomeCalled)
	}
}

func TestReadDNDSource_NilReader(t *testing.T) {
	if value, ok := readDNDSource(context.Background(), nil); value || ok {
		t.Errorf("readDNDSource(nil) = (%v, %v), want (false, false)", value, ok)
	}
}

func TestDesktopIsDoNotDisturb_NoSessionBusStillReadsGSettings(t *testing.T) {
	// A GNOME session whose bus is unreachable must still consult GSettings, and
	// must not crash on the nil connection.
	withDNDStubs(t, func(context.Context) (*dbus.Conn, func(), error) { return nil, nil, errors.New("no session bus") },
		func(context.Context) (bool, bool) { return false, true }, // banners hidden => DND
		func() string { return "gnome" })

	if !desktopIsDoNotDisturb() {
		t.Error("desktopIsDoNotDisturb() = false, want true when GNOME reports show-banners=false")
	}
}

func TestDesktopIsDoNotDisturb_FailsOpenWithoutAnySource(t *testing.T) {
	withDNDStubs(t, func(context.Context) (*dbus.Conn, func(), error) { return nil, nil, errors.New("no session bus") },
		func(context.Context) (bool, bool) { return false, false },
		func() string { return "kde" })

	if desktopIsDoNotDisturb() {
		t.Error("desktopIsDoNotDisturb() = true, want false when no source can be read")
	}
}

func TestDesktopIsDoNotDisturb_CompletesWithinProbeBudget(t *testing.T) {
	// Runs against whatever the machine really has (usually nothing in CI). The
	// value is not asserted - only that the hook path can never be blocked for
	// longer than the documented budget.
	start := time.Now()
	_ = desktopIsDoNotDisturb()
	if elapsed := time.Since(start); elapsed > dndProbeTimeout+time.Second {
		t.Errorf("desktopIsDoNotDisturb() took %v, want at most %v", elapsed, dndProbeTimeout+time.Second)
	}
}

// withDNDStubs replaces the three package-level seams for the duration of a test.
func withDNDStubs(t *testing.T, bus func(context.Context) (*dbus.Conn, func(), error), banners dndReader, desktop func() string) {
	t.Helper()
	origBus, origBanners, origDesktop := dndSessionBus, gnomeShowBanners, currentDesktop
	dndSessionBus, gnomeShowBanners, currentDesktop = bus, banners, desktop
	t.Cleanup(func() {
		dndSessionBus, gnomeShowBanners, currentDesktop = origBus, origBanners, origDesktop
	})
}

func TestParseGSettingsBool(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		want   bool
		wantOK bool
	}{
		{"true with newline", "true\n", true, true},
		{"false with newline", "false\n", false, true},
		{"uppercase and padded", " FALSE ", false, true},
		{"empty", "", false, false},
		{"unrecognised", "uh oh", false, false},
		{"gvariant boolean literal", "b'true'", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseGSettingsBool(tt.raw)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("parseGSettingsBool(%q) = (%v, %v), want (%v, %v)", tt.raw, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestDefaultCurrentDesktop(t *testing.T) {
	t.Run("prefers XDG_CURRENT_DESKTOP and lowercases it", func(t *testing.T) {
		t.Setenv("XDG_CURRENT_DESKTOP", "ubuntu:GNOME")
		t.Setenv("XDG_SESSION_DESKTOP", "plasma")
		if got := defaultCurrentDesktop(); got != "ubuntu:gnome" {
			t.Errorf("defaultCurrentDesktop() = %q, want %q", got, "ubuntu:gnome")
		}
	})

	t.Run("falls back to XDG_SESSION_DESKTOP", func(t *testing.T) {
		t.Setenv("XDG_CURRENT_DESKTOP", "")
		t.Setenv("XDG_SESSION_DESKTOP", "XFCE")
		if got := defaultCurrentDesktop(); got != "xfce" {
			t.Errorf("defaultCurrentDesktop() = %q, want %q", got, "xfce")
		}
	})

	t.Run("empty when neither is set", func(t *testing.T) {
		t.Setenv("XDG_CURRENT_DESKTOP", "")
		t.Setenv("XDG_SESSION_DESKTOP", "")
		if got := defaultCurrentDesktop(); got != "" {
			t.Errorf("defaultCurrentDesktop() = %q, want empty", got)
		}
	})
}

func TestDesktopMatchers(t *testing.T) {
	tests := []struct {
		desktop   string
		wantGnome bool
		wantXFCE  bool
	}{
		{"gnome", true, false},
		{"ubuntu:gnome", true, false},
		{"pop:gnome", true, false},
		{"unity", true, false},
		{"xfce", false, true},
		{"xubuntu:xfce", false, true},
		{"kde", false, false},
		{"sway", false, false},
		{"", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.desktop, func(t *testing.T) {
			if got := isGNOMEDesktop(tt.desktop); got != tt.wantGnome {
				t.Errorf("isGNOMEDesktop(%q) = %v, want %v", tt.desktop, got, tt.wantGnome)
			}
			if got := isXFCEDesktop(tt.desktop); got != tt.wantXFCE {
				t.Errorf("isXFCEDesktop(%q) = %v, want %v", tt.desktop, got, tt.wantXFCE)
			}
		})
	}
}

func TestDefaultGnomeShowBanners_MissingBinaryIsUnknown(t *testing.T) {
	// Force the gsettings lookup to fail by emptying PATH: the contract is that a
	// missing or failing subprocess reports "unknown", never "in DND".
	t.Setenv("PATH", "")
	value, ok := defaultGnomeShowBanners(context.Background())
	if ok {
		t.Errorf("defaultGnomeShowBanners() ok = true (value %v), want unknown when gsettings cannot run", value)
	}
}
