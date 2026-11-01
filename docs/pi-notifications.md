# Pi notifications

Pi receives completion, question and final error alerts through its native
extension events. The common Agent Notifications installer installs and maintains
the extension through Universal Agent Plugins. In Pi's local terminal UI, desktop
alerts use configured sounds and return to the originating terminal when clicked.
Explicitly enabled webhooks also work in print, JSON, RPC and remote sessions.

## Qualification

Release acceptance covers real Pi hooks and installed update/repair/remove/reload
checks on all three OSes, with native desktop cases on Linux. The owner also
manually confirmed a completion banner and click back to the Pi Terminal window
on macOS. Full macOS/Windows desktop sound and negative-case qualification is
pending; see the [test scope and retained limitations](testing/pi-native-e2e.md#owner-approved-hooklifecycle-release-profile-2026-11-01).

## Install or update

Install the [official standalone Pi distribution](https://github.com/earendil-works/pi#getting-started)
first and make `pi` available on `PATH`. The native integration is tested against
Pi v1.4.0. On Windows, use its real `pi.exe`; an npm `pi.cmd` shim is refused with
standalone installation guidance. Run the common installer from Bash, or native
Git Bash on Windows:

```bash
(set -o pipefail; curl -fsSL https://agent-notifications.com/install.sh | bash -s -- --product pi --desktop)
```

Pi can share one setup command with other selected agents:

```bash
(set -o pipefail; curl -fsSL https://agent-notifications.com/install.sh | bash -s -- --products claude,codex,pi --desktop)
```

Choose `--webhook` for webhooks only or `--desktop --webhook` for both channels.
Configure the webhook destination separately. Channel consent belongs to Pi;
adding it does not enable another agent's channels. An update without channel
flags preserves Pi's saved choice. Pi setup installs its notification extension;
it does not add an MCP server or skill.

Restart Pi or run `/reload` after installation, update or repair. A still-loaded
old observer cannot adopt a changed package/runtime revision; reload loads the
new installed observer. Keep the installer's printed inspect, repair, remove and
recovery commands for this installation.

## Events and delivery modes

| Native event | Alert | Meaning |
| --- | --- | --- |
| Final `agent_settled`, completed outcome | Task complete | Automatic retries and queued continuations have finished successfully; intermediate turns do not notify |
| `ui_prompt_start` | Question | An extension is waiting for blocking user input; this does not approve a tool or permission |
| Final settlement with an error outcome | API error | A final provider/run error; raw provider error text is excluded |
| Aborted settlement | No completion | Cancelling a run does not announce success |

Alerts use fixed Pi wording. Prompt titles, answers, transcripts, tool arguments
and provider error strings are not included in the event carrier or webhook.
A completed activity describes Pi's final run outcome; it is not an independent
assessment of the correctness of the answer.

| Pi mode/context | Desktop, sound and click | Enabled webhook |
| --- | --- | --- |
| Local terminal UI with a known originating terminal | Yes | Yes |
| JSON or print | No | Yes |
| RPC | No | Yes, including blocking UI requests |
| SSH/remote or unknown terminal locality | No | Yes |

RPC's functional extension UI is not a local terminal. There is no override that
turns a headless or remote session into a desktop target. Normal one-shot exit
drains an already admitted terminal webhook within its original bounded delivery
deadline; cancellation and endpoint failures do not cause automatic resends.

## Sound and channel settings

Use the installed executable and configuration path shown by setup. The common
launcher can locate the shared file:

```bash
agent-notifications config path
agent-notifications config inspect --json
```

Pi inherits shared settings. To override Pi alone, first follow the
[schema 2 upgrade instructions](AGENT_CONFIGURATION.md#upgrade-an-existing-configuration),
then merge the relevant leaves into the existing configuration:

```json
{
  "schemaVersion": 2,
  "agents": {
    "pi": {
      "notifications": {
        "desktop": {
          "enabled": true,
          "sound": true,
          "volume": 0.5,
          "terminalBell": false,
          "clickToFocus": true
        },
        "webhook": {
          "enabled": false
        }
      }
    }
  }
}
```

`sound:false` disables the configured cue. Set `terminalBell:false` as well when
the terminal bell should stay quiet. `clickToFocus:false` disables the terminal
action. Per-status cues, custom files and audio device settings follow
the [shared sound configuration](CONFIGURATION.md#sound-options). The installed
managed runtime supplies its sound assets and native helper. An ambient
`CLAUDE_PLUGIN_ROOT` does not redirect the Pi observer to another installation.

Desktop/webhook settings also require the corresponding channel consent saved by
setup. Changing a shared flag cannot grant consent to an unselected channel.
Use the installer's channel-change command to change that choice.

## Return to the originating terminal

Keep Pi running in the terminal from which the task started. A notification click
uses the shared platform targeting path captured from that live Pi session.
Window, tab and pane precision depends on the terminal and multiplexer; see the
[terminal support table and permissions](CLICK_TO_FOCUS.md). A Windows Terminal
window is a window target, not a promise to select a particular tab.

macOS uses the managed signed notification helper and requires notification
permission. Exact window targeting can also need the documented Accessibility
and screen permissions. Linux requires a running desktop notification service
and the focus tools appropriate for its compositor. Windows requires an active
interactive desktop with notifications enabled. Muting sounds, Focus/Do Not
Disturb or a locked/unavailable desktop can change visible or audible delivery.

## Profile, inspection and removal

Pi's default user agent directory is `~/.pi/agent`. A nonempty
`PI_CODING_AGENT_DIR` selects the agent directory itself. Set it consistently
before setup and when starting Pi. The common installer records the physical
profile and owns one visible extension subdirectory; it preserves unrelated
extensions and settings. It refuses foreign files that mask Pi's child extension
discovery and reports their paths without rewriting them.

Use the printed common inspect/repair/remove commands. Missing owned files can
be repaired; edited or foreign files require a decision rather than silent
replacement. Removal revokes the observer and deletes only owned material. It
does not require the Pi CLI to remain installed. Restart or reload an existing Pi
session after removal to unload its in-memory factory.

## Verification

After setup and reload, start a normal Pi task in a local terminal, move to a
different window and wait for final completion. Check the banner and configured
cue, then click the notification and verify the originating terminal. A pending
extension input prompt should produce a Question alert; keep it open while
checking its return action. Inspect the separate webhook destination if enabled.
An accepted helper/API call alone does not establish a visible banner, audio
output or successful terminal activation.

The [native evidence guide](testing/pi-native-e2e.md) distinguishes actual Pi
process/loader checks, desktop observer prerequisites and candidate desktop
qualification. See [troubleshooting](troubleshooting.md) for shared delivery and
permission diagnostics.
