//go:build linux

package notifier

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/777genius/agent-notifications/internal/logging"
)

const (
	// dndProbeTimeout bounds the entire DND probe. The probe runs on the hook
	// path, so it must never become a source of latency: on expiry the probe
	// reports "not in DND" and the notification is delivered as usual.
	dndProbeTimeout = 1500 * time.Millisecond

	// dndCallTimeout bounds a single D-Bus round trip.
	dndCallTimeout = 400 * time.Millisecond

	// gsettingsTimeout bounds the GNOME subprocess query, which is the only
	// non-D-Bus probe and therefore the slowest one.
	gsettingsTimeout = time.Second
)

// D-Bus addresses of the Do Not Disturb sources this package understands.
const (
	notificationsBusName           = "org.freedesktop.Notifications"
	notificationsObjectPath        = dbus.ObjectPath("/org/freedesktop/Notifications")
	notificationsInhibitedProperty = "Inhibited"

	dunstInterface      = "org.dunstproject.cmd0"
	dunstPausedProperty = "paused"

	xfconfBusName            = "org.xfce.Xfconf"
	xfconfObjectPath         = dbus.ObjectPath("/org/xfce/Xfconf")
	xfceNotifydChannel       = "xfce4-notifyd"
	xfceDoNotDisturbProperty = "/do-not-disturb"

	gnomeNotificationsSchema = "org.gnome.desktop.notifications"
	gnomeShowBannersKey      = "show-banners"
)

// Test seams: the session bus, the GNOME subprocess, and the desktop name are
// the three things a unit test cannot provide for real.
var (
	dndSessionBus    = defaultDNDSessionBus
	gnomeShowBanners = defaultGnomeShowBanners
	currentDesktop   = defaultCurrentDesktop
)

// dndReader reads one Do Not Disturb signal, bounded by ctx. It returns
// (value, ok); ok is false when the source could not be read at all, which is
// always treated as "no information" rather than as "not in DND".
type dndReader func(ctx context.Context) (value bool, ok bool)

// dndReaders holds one probe per supported notification daemon. A nil reader is
// a source that is not present in this session and is skipped.
type dndReaders struct {
	// inhibited reads org.freedesktop.Notifications.Inhibited. KDE Plasma and
	// any daemon exposing the same extension can provide it. Note
	// it is also true when an application requests inhibition (fullscreen video,
	// screen sharing), not only when the user toggles DND by hand.
	inhibited dndReader
	// dunstPaused reads dunst's org.dunstproject.cmd0 "paused" property.
	dunstPaused dndReader
	// xfceDND reads xfce4-notifyd's /do-not-disturb Xfconf property.
	xfceDND dndReader
	// gnomeBanners reports whether GNOME is SHOWING banners (inverted meaning).
	gnomeBanners dndReader
}

// desktopIsDoNotDisturb wires the real readers and evaluates them. Every failure
// mode - no session bus, no notification daemon, a daemon without DND state -
// leaves the corresponding reader nil or unreadable and therefore reports "not
// in DND", so the notification is delivered.
func desktopIsDoNotDisturb() bool {
	ctx, cancel := context.WithTimeout(context.Background(), dndProbeTimeout)
	defer cancel()

	desktop := currentDesktop()
	readers := dndReaders{gnomeBanners: gnomeShowBanners}

	conn, closeConn, err := dndSessionBus(ctx)
	if err != nil {
		logging.Debug("DND: no session bus (%v); D-Bus probes skipped", err)
	} else {
		defer closeConn()

		// One owner check covers both notification-daemon probes: it keeps an
		// absent daemon from costing two full call timeouts, and stops the probe
		// from D-Bus-activating a notification daemon just to ask it a question.
		if busNameHasOwner(ctx, conn, notificationsBusName) {
			readers.inhibited = func(c context.Context) (bool, bool) {
				return dbusBoolProperty(c, conn, notificationsBusName, notificationsObjectPath,
					notificationsBusName, notificationsInhibitedProperty)
			}
			readers.dunstPaused = func(c context.Context) (bool, bool) {
				return dbusBoolProperty(c, conn, notificationsBusName, notificationsObjectPath,
					dunstInterface, dunstPausedProperty)
			}
		} else {
			logging.Debug("DND: %s has no owner; notification-daemon probes skipped", notificationsBusName)
		}

		readers.xfceDND = func(c context.Context) (bool, bool) {
			if !busNameHasOwner(c, conn, xfconfBusName) {
				return false, false
			}
			return xfconfBoolProperty(c, conn, xfceNotifydChannel, xfceDoNotDisturbProperty)
		}
	}

	return evaluateDND(ctx, desktop, readers)
}

// evaluateDND is the pure decision layer: any positive signal wins, and any
// unreadable source is skipped. Desktop-specific probes are gated on the desktop
// name so a subprocess is never spawned on a desktop that cannot answer.
func evaluateDND(ctx context.Context, desktop string, r dndReaders) bool {
	if v, ok := readDNDSource(ctx, r.inhibited); ok && v {
		logging.Debug("DND: active (%s.%s)", notificationsBusName, notificationsInhibitedProperty)
		return true
	}
	if v, ok := readDNDSource(ctx, r.dunstPaused); ok && v {
		logging.Debug("DND: active (dunst paused)")
		return true
	}
	if isXFCEDesktop(desktop) {
		if v, ok := readDNDSource(ctx, r.xfceDND); ok && v {
			logging.Debug("DND: active (xfce4-notifyd do-not-disturb)")
			return true
		}
	}
	if isGNOMEDesktop(desktop) {
		// GNOME Shell exposes no DND property on org.freedesktop.Notifications;
		// the state lives in GSettings, and the key is inverted.
		if shown, ok := readDNDSource(ctx, r.gnomeBanners); ok && !shown {
			logging.Debug("DND: active (GNOME %s=false)", gnomeShowBannersKey)
			return true
		}
	}
	return false
}

// readDNDSource runs one reader, skipping absent sources and honoring the
// overall probe deadline so a slow early source cannot make a later one hang.
func readDNDSource(ctx context.Context, read dndReader) (bool, bool) {
	if read == nil || ctx.Err() != nil {
		return false, false
	}
	return read(ctx)
}

// dbusBoolProperty reads a boolean D-Bus property via
// org.freedesktop.DBus.Properties.Get.
func dbusBoolProperty(ctx context.Context, conn *dbus.Conn, dest string, path dbus.ObjectPath, iface, prop string) (bool, bool) {
	callCtx, cancel := context.WithTimeout(ctx, dndCallTimeout)
	defer cancel()

	var variant dbus.Variant
	err := conn.Object(dest, path).
		CallWithContext(callCtx, "org.freedesktop.DBus.Properties.Get", 0, iface, prop).
		Store(&variant)
	if err != nil {
		// Expected on daemons that do not implement this property.
		logging.Debug("DND: %s.%s unavailable: %v", iface, prop, err)
		return false, false
	}

	value, ok := variant.Value().(bool)
	if !ok {
		logging.Debug("DND: %s.%s is not a boolean (%s)", iface, prop, variant.Signature())
		return false, false
	}
	return value, true
}

// xfconfBoolProperty reads a boolean from the Xfconf settings daemon.
func xfconfBoolProperty(ctx context.Context, conn *dbus.Conn, channel, property string) (bool, bool) {
	callCtx, cancel := context.WithTimeout(ctx, dndCallTimeout)
	defer cancel()

	var variant dbus.Variant
	err := conn.Object(xfconfBusName, xfconfObjectPath).
		CallWithContext(callCtx, "org.xfce.Xfconf.GetProperty", 0, channel, property).
		Store(&variant)
	if err != nil {
		logging.Debug("DND: xfconf %s%s unavailable: %v", channel, property, err)
		return false, false
	}

	value, ok := variant.Value().(bool)
	if !ok {
		logging.Debug("DND: xfconf %s%s is not a boolean (%s)", channel, property, variant.Signature())
		return false, false
	}
	return value, true
}

// busNameHasOwner reports whether a bus name is currently owned. Probing this
// first avoids both a slow timeout against a dead service and D-Bus activating
// a daemon as a side effect of querying it.
func busNameHasOwner(ctx context.Context, conn *dbus.Conn, name string) bool {
	callCtx, cancel := context.WithTimeout(ctx, dndCallTimeout)
	defer cancel()

	var owned bool
	if err := conn.BusObject().
		CallWithContext(callCtx, "org.freedesktop.DBus.NameHasOwner", 0, name).
		Store(&owned); err != nil {
		logging.Debug("DND: NameHasOwner(%s) failed: %v", name, err)
		return false
	}
	return owned
}

// defaultDNDSessionBus opens a PRIVATE session bus connection bounded by ctx.
// The hook process is short-lived, so a private connection can be closed
// deterministically instead of leaking the shared connection's goroutines.
//
// The no-autostartup variant is deliberate: the autostarting one shells out to
// dbus-launch when DBUS_SESSION_BUS_ADDRESS is unset, which would both spawn a
// stray bus daemon and add subprocess latency to the hook path just to discover
// that this session has no desktop to ask.
func defaultDNDSessionBus(ctx context.Context) (*dbus.Conn, func(), error) {
	conn, err := dbus.SessionBusPrivateNoAutoStartup(dbus.WithContext(ctx))
	if err != nil {
		return nil, nil, err
	}
	if err := conn.Auth(nil); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	if err := conn.Hello(); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return conn, func() { _ = conn.Close() }, nil
}

// defaultGnomeShowBanners reports GNOME's show-banners setting. It returns
// (shown, ok); ok is false when gsettings is missing, errors, times out, or
// prints something unrecognised.
func defaultGnomeShowBanners(ctx context.Context) (bool, bool) {
	callCtx, cancel := context.WithTimeout(ctx, gsettingsTimeout)
	defer cancel()

	out, err := exec.CommandContext(callCtx, "gsettings", "get", gnomeNotificationsSchema, gnomeShowBannersKey).Output()
	if err != nil {
		logging.Debug("DND: gsettings %s unavailable: %v", gnomeShowBannersKey, err)
		return false, false
	}
	return parseGSettingsBool(string(out))
}

// parseGSettingsBool parses the "true"/"false" gsettings prints for a boolean.
func parseGSettingsBool(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

// defaultCurrentDesktop returns the lowercased desktop name. XDG_CURRENT_DESKTOP
// may be a colon-separated list (for example "ubuntu:GNOME"), which is why
// callers match on substrings rather than on equality.
func defaultCurrentDesktop() string {
	desktop := os.Getenv("XDG_CURRENT_DESKTOP")
	if desktop == "" {
		desktop = os.Getenv("XDG_SESSION_DESKTOP")
	}
	return strings.ToLower(desktop)
}

func isGNOMEDesktop(desktop string) bool {
	return strings.Contains(desktop, "gnome") || strings.Contains(desktop, "unity")
}

func isXFCEDesktop(desktop string) bool {
	return strings.Contains(desktop, "xfce")
}
