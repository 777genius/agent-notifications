# Do Not Disturb

On macOS, the notification service plays the configured per-status sound. The
helper converts it to a short PCM resource with the configured volume already
applied, then submits an ordinary notification. macOS applies Focus and per-app
notification settings to the banner and sound together. No Focus-file access or
Full Disk Access is required.

Linux and Windows use the plugin's own audio process. On supported Linux
desktops, `respectDoNotDisturb` controls that separate audio cue as described below.

## Configuration

In the shared file selected by `config path`
(`"$NOTIFICATIONS_BIN" config path --json`; see the
[Configuration guide](CONFIGURATION.md)):

```json
{
  "notifications": {
    "respectDoNotDisturb": "silent"
  }
}
```

| Value | Behaviour while DND is active |
|---|---|
| `"off"` (default) | Current behaviour. DND state is never queried, so the option costs nothing when it is not in use. |
| `"silent"` | The banner is still delivered — it lands in the notification centre and shows when DND lifts — but the plugin's own sound is not played. |
| `"suppress"` | Nothing is delivered: no banner, no sound. |

Webhooks are unaffected in all three modes. DND is a statement about *this*
desktop's attention, not about your team's Slack channel.

DND is read at delivery time, after any `notifyDelaySeconds` wait, so switching
DND on during the grace period is honoured.

### Why the default is `"off"`

Playing audio through DND is the bug, so "respect DND" arguably belongs on by
default. Two things argue against flipping it:

- A silent behaviour change on upgrade is a surprise. `notifyOnlyWhenUnfocused`
  shipped as `false` for the same reason.
- On KDE, `org.freedesktop.Notifications.Inhibited` is **also** true when an
  *application* requests inhibition — fullscreen video, screen sharing,
  presentation mode — not only when you toggle DND by hand. That is arguably the
  right semantics, but a default-on version could swallow cues during a
  fullscreen video, and a missing cue is a much worse failure than an extra one.

### Terminal bell

On macOS the plugin does not send a terminal bell, because an audible BEL can
bypass Focus. On other platforms, in `"silent"` mode the terminal bell (`terminalBell`, on by default) still fires.
BEL is a *tab indicator* on Ghostty, tmux and Windows Terminal — visual for most
users — and whether it is audible at all is already your terminal's own setting.
In `"suppress"` mode it does not fire, because the notification is dropped before
the notifier is reached. Set `"terminalBell": false` if you want it gone entirely.

## Detection fails open

Detection returns "in DND" **only** on positive confirmation. Any D-Bus error,
unsupported desktop, missing daemon, timeout, or unparseable value is treated as
"unknown", which means the notification and its sound are delivered exactly as
before.

This bias is deliberate: a probe error or unknown state keeps the existing
delivery behavior. A confirmed positive result in `"suppress"` mode intentionally
skips desktop delivery; a false positive could therefore hide a notification.

## What is detected, per platform

### Linux

| Desktop / daemon | Source | Notes |
|---|---|---|
| KDE Plasma (and any daemon exposing the same property) | `org.freedesktop.Notifications.Inhibited` (D-Bus property) | KDE-compatible extension, not part of the freedesktop notification specification. Also true for application-requested inhibition — fullscreen video, screen sharing. |
| dunst | `paused` property on the `org.dunstproject.cmd0` interface, same bus name and object path | Matches `dunstctl set-paused`. |
| XFCE | `xfce4-notifyd` / `/do-not-disturb` via `org.xfce.Xfconf.GetProperty` | Only queried when the desktop is XFCE. |
| GNOME (and Unity) | `gsettings get org.gnome.desktop.notifications show-banners` | The key is inverted: `false` means DND. GNOME exposes no DND property on D-Bus, so this is the one probe that costs a subprocess; it only runs when the desktop is GNOME. |

Any positive signal wins; sources that cannot be read are skipped.

Not covered yet: notification daemons that expose DND only through their own
private interface and do not implement `Inhibited` — for example swaync
(`org.erikreider.swaync`) and mako. Adding one is a new reader in
`internal/notifier/dnd_linux.go` plus a case in `evaluateDND`; the same fail-open
rule applies, so an unrecognised daemon is never worse than today.

### macOS

Focus applies automatically to native notifications, including custom sounds,
regardless of `respectDoNotDisturb`. The plugin does not query Focus state, so
`"suppress"` cannot remove a notification from Notification Center on macOS.
Normal notifications do not request time-sensitive interruption.

Custom sounds are decoded to signed 16-bit PCM CAF files in `~/Library/Sounds`,
with the configured volume applied to the samples. Resources are cached by
source content and volume. The source must be a regular file of at most 32 MiB
and shorter than 30 seconds. Invalid or unsupported sounds do not fall back to a
default beep or separate Go playback. macOS controls the alert output device and
system alert volume in addition to the plugin's configured gain.

`respectDisplaySleep` continues to mute the custom sound when the display sleeps.
Older native helpers do not understand custom audio arguments and remain silent;
update both the sender and native helper together.

### Windows

DND detection is not implemented. `respectDoNotDisturb` has no effect there;
Windows still uses the plugin's own audio process.
