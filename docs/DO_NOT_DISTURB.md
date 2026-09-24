# Do Not Disturb

The plugin plays its own audio cue, in its own process, rather than asking the
notification server to play one. That is what makes per-status sounds and volume
control possible — and it is also why, before this option existed, the MP3 played
at full volume while the desktop was in Do Not Disturb: the banner was correctly
queued and silenced by the desktop, but nothing had any say over a separate
process's audio.

`respectDoNotDisturb` closes that gap.

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

### The terminal bell is not muted

In `"silent"` mode the terminal bell (`terminalBell`, on by default) still fires.
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

### macOS and Windows — not implemented

Both report "not in DND", so `respectDoNotDisturb` currently has no effect there:
notifications and their sounds are delivered exactly as before, in every mode.
The same underlying bug exists on both platforms — the plugin plays its own
sound, and the macOS Swift notifier is already invoked with `-nosound` — so this
is worth doing, but not worth guessing at:

- **macOS.** `defaults -currentHost read com.apple.notificationcenterui doNotDisturb`
  stopped being authoritative in Ventura, which replaced Do Not Disturb with
  Focus modes. The live state lives in `~/Library/DoNotDisturb/DB/Assertions.json`
  and `~/Library/Preferences/com.apple.ncprefs.plist`. Both are undocumented and
  version-fragile, and a detector that is wrong in the "in DND" direction
  silently swallows notifications — the one failure mode this feature must not
  have. It needs its own testing across OS versions, in its own change.
- **Windows.** Focus Assist / quiet hours, via
  `WNF_SHEL_QUIET_MOMENT_SHELL_MODE_CHANGED` or the registry under
  `HKCU\SOFTWARE\Microsoft\Windows\CurrentVersion\Notifications\Settings`.
  `git.sr.ht/~jackmordaunt/go-toast` is already a dependency but does not expose
  it.

Both slot into the existing shape: add `internal/notifier/dnd_darwin.go` or
`dnd_windows.go` with a `desktopIsDoNotDisturb()` and narrow the build tag on
`dnd_other.go` accordingly. No change to the config, hook, or notifier API is
needed.

## Latency

The probe runs on the hook path, which is latency-critical, so it is bounded:

- D-Bus property reads are sub-millisecond and always run.
- One `NameHasOwner` check gates the notification-daemon probes, so an absent
  daemon costs one fast round trip instead of two full timeouts — and the probe
  never D-Bus-activates a daemon as a side effect of asking it a question.
- The `gsettings` subprocess (~50–150ms) runs **only** on GNOME.
- Hard caps: 400ms per D-Bus call, 1s for the `gsettings` call, 1.5s for the
  whole probe. On expiry the probe reports "not in DND" and delivery proceeds.
- Nothing is queried at all while `respectDoNotDisturb` is `"off"`, and nothing
  is queried when the notification was already suppressed by
  `notifyOnlyWhenUnfocused`.

The session bus connection is private and closed when the probe returns, and it
is opened with the no-autostart variant so a session without a bus cannot cause
`dbus-launch` to spawn a stray daemon.

## Checking it yourself

Read the KDE / freedesktop property directly:

```bash
gdbus call --session \
  --dest org.freedesktop.Notifications \
  --object-path /org/freedesktop/Notifications \
  --method org.freedesktop.DBus.Properties.Get \
  org.freedesktop.Notifications Inhibited
```

Toggle DND from your notification applet and re-run: it should flip
`<<true>>`/`<<false>>`. Start a fullscreen video and re-run to see the
application-inhibition case described above.

The equivalents for the other sources:

```bash
dunstctl is-paused
xfconf-query -c xfce4-notifyd -p /do-not-disturb
gsettings get org.gnome.desktop.notifications show-banners   # false means DND
```

With debug logging enabled, the plugin logs which source fired:

```
DND: active (org.freedesktop.Notifications.Inhibited)
DND: active (dunst paused)
DND: active (xfce4-notifyd do-not-disturb)
DND: active (GNOME show-banners=false)
```

If the outcome looks right but the source is not the one you expect, the
detection is wrong even though the result happens to match.
