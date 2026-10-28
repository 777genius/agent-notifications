# Pi native evidence

`pi-native-preflight.yml` runs the exact pull-request checkout on native Linux,
macOS and Windows. It exercises independent synthetic GUI, foreground, input and
audio observers before any candidate product is installed. Its receipt always
has `desktop_qualified: false`; `prerequisites_ready` establishes these observers,
not a Pi banner, notification permission, product sound or notification click.
Unavailable prerequisites exit nonzero with an `unqualified` receipt. Windows
Server cannot substitute for the required Windows Client desktop.
The separate early Client lane uses the existing `windows-11-vs2026-arm` runner.
An already configured Client runner can be selected with
`CI_PI_WINDOWS_DESKTOP_RUNNER` or the workflow's `windows_client_runner` input.
Receipts record the host architecture. A later run of the shipped amd64 product
on ARM must explicitly record Windows x64 emulation rather than claim amd64
hardware. The desktop matrix uses Client only; the actual native process
contracts workflow separately covers Windows Server without granting desktop
qualification.

The Linux fixture owns its Xvfb display, Openbox, D-Bus session, dunst, two xterms
and PulseAudio null sink. It activates both windows, records a known PCM cue from
the sink monitor, and clicks a synthetic dunst notification using native input.
macOS uses actual session/permission observations, two disposable native windows,
native mouse input and ScreenCaptureKit audio capture. It never requests or grants
permissions. Windows observes the actual WTS/input desktop and Client SKU, creates
two disposable windows, verifies native input/foreground observation and records
a known cue through WASAPI loopback. No process/return-code observation is treated
as audio playback evidence. Its read-only audio inventory records both audio
service states, render endpoint counts across active/disabled/unplugged/missing
states and all three default render roles in that same session. The product's
pinned malgo/miniaudio WASAPI default uses `eConsole`, matching the capture probe;
an absent configured render route remains unqualified.

Run the evidence guards on any host:

```bash
python -B scripts/test_pi_native_preflight.py -v
```

Run a native probe with its exact checked-out commit and an explicit report path:

```bash
python -B scripts/pi-native-preflight.py --execute-test-fixtures \
  --platform linux --source-sha <40-character-HEAD> \
  --report /absolute/TEST-evidence/pi-preflight.json
```

The preflight retains bounded metadata only in the uploaded JSON. Synthetic
screenshots, audio and process diagnostics stay in its private TEST root. The
candidate desktop tests must separately validate the signed helper's permission,
show the real candidate completion and pending question, record their output,
click each real notification and identify the originating terminal. Only the
three-OS candidate aggregate can grant that acceptance.

## Candidate input inventory

Construct the candidate from one immutable source checkout and normal builders.
The input set includes source and managed runtime assets; the existing portable
ZIP alone does not contain the full desktop runtime. Capture both input hashes
and the installer-produced receipt/installed hashes.

| Input | Construction or authority | Required evidence |
| --- | --- | --- |
| Main executable | Native `go build -trimpath -ldflags="-s -w" ./cmd/claude-notifications`, matching the release workflow | SHA-256; actual `go version -m`; exact checkout; linked UAP versions/sums; no development replace |
| Companion utilities | Native release builds of `sound-preview`, `list-devices`, `list-sounds` | Exact-source executable hashes and installed inventory |
| Source package | Canonical source snapshot containing `plugin.json`, the Pi namespace source, configuration and sounds | Source commit/digest and normal UAP projection receipt |
| Portable ZIP | `cmd/build-portable-package`, using that final main executable | ZIP hash; normal builder layout; embedded executable hash |
| macOS native helper | Checksum-pinned, signature-verified `ClaudeNotifier.app`, through the existing managed native asset path | Archive and signed tree identity; installed native receipt; actual permission observation |
| Windows callback | `native/windows-callback/build.ps1 -EmbedAsset`, main built with `windows_callback_asset`, `verify-embedded.ps1` | Embedded asset verification against the built helper |
| Windows focus companion | Same-source native build using `-H=windowsgui`, as in the release workflow | Companion hash, installed path and real protocol activation from a notification click |
| Configured cue | Canonical sound file installed by the normal runtime handoff | Source/installed hashes and independent output recording; sound-off negative |
| Pi host | Official standalone v1.4.0 archive pinned by platform SHA-256 | Archive hash, physical executable and actual `--version` |

The product fixture must invoke the normal setup/UAP engine. It may not copy a
product extension into Pi, invent a product installer or replace a native helper
with a recorder. Assert the actual installer active path is
`<physical-agent-dir>/extensions/<physicalArtifactID>` with a visible leaf, then
launch Pi without `-e`. Explicit `-e` is reserved for the retained-copy negative
loader test. Keep the old Pi process alive during update/removal to verify that
captured old revisions are denied; use a new or reloaded process to verify the
new installed projection.

## Public UAP Engine and real Pi loader

`scripts/pi-uap-loader-e2e.py` exercises the UAP repository's integration-test
driver, whose implementation calls the public Engine `Prepare`/`Apply` API. It
does not implement installation. The driver source is
`install/integrationctl/agentplugins/clients/pi/testdata/driver/main.go` in UAP.

```bash
python -B scripts/pi-uap-loader-e2e.py \
  --driver /absolute/TEST-tools/pi-uap-driver \
  --pi-executable /absolute/verified-pi/pi \
  --report /absolute/TEST-evidence/pi-uap-loader.json
```

The fixture asserts the actual visible artifact ID and runs that Engine-generated
bootstrap with real Pi. It checks exact-once automatic loading, retained visible
and explicit hidden negative copies, physical-path mismatch, idempotent install,
replacement, missing-owned repair and removal while a foreign extension remains
usable. Its synthetic package is independent of Notifications. The receipt marks
product notification qualification false; production setup/runtime tests remain
separate requirements.

## Pinned standalone and native event oracle

`scripts/testdata/pi-native/pins.json` freezes the official v1.4.0 standalone
archive bytes for each supported target. `scripts/acquire-pi-native.py` checks
size and SHA-256 before bounded extraction; archive and executable hashes have
separate names in its receipt. It never resolves a moving release or npm graph.

```bash
python -B scripts/acquire-pi-native.py --download --platform linux --arch amd64 \
  --output /absolute/TEST-tools/pi --report /absolute/TEST-evidence/pi-acquisition.json
python -B scripts/pi-native-contracts.py \
  --pi-executable /absolute/TEST-tools/pi/pi/pi \
  --report /absolute/TEST-evidence/pi-events.json
```

`pi-native-contracts.yml` runs real Pi JSON/RPC/print processes and its ordinary
auto-discovered foreign witness against a loopback HTTP provider. It verifies
success, retry, queued followup, error despite exit zero, abort without a boundary,
idle RPC UI and print completion. It records event counts and outcome metadata,
never transcripts. Every provider and child is collected. This is native Pi
contract evidence; product installation, TUI delivery, one-shot product shutdown
drain and desktop acceptance are separate candidate gates.

The same workflow also submits a real prompt in Pi's normal TUI and waits for the
actual provider reply and settled event before `/quit`. Unix uses an owned PTY;
Windows uses the narrow fixed-argument `pi_native_pty_windows.cpp` ConPTY bridge,
with the Pi process tree owned by a kill-on-close job. The receipt requires actual
`mode:tui`, one provider request, one settlement and collected shutdown. Startup
alone does not pass. A PTY receipt still grants no visible desktop, sound or
click-to-focus result.

## Actual installed Notifications fixtures

`scripts/pi-installed-process-e2e.py` uses the normal
`internal-install-runtime --consumer pi-runtime-preparation` writer, then
`setup-notifications wizard --action ... --agents pi`. It never pre-enables the
unrelated portable MCP policy. Pi's explicit channel choices and effective
configuration must suffice while that global policy remains disabled. The
fixture checks that the neutral preparation consumer is retired, that Pi is the
only remaining consumer, and that installed icon/sound hashes have real managed
receipts. It never copies an observer, locator, policy or sound into place.

Supply a complete normal release binary stage, two portable packages made by
`cmd/build-portable-package` for the update test, the canonical asset source and
the verified standalone Pi:

```bash
python -B scripts/pi-installed-process-e2e.py \
  --candidate /absolute/TEST-inputs/bin/claude-notifications-linux-amd64 \
  --runtime-stage /absolute/TEST-inputs/bin \
  --replacement-runtime-stage /absolute/TEST-inputs/replacement-stage \
  --package /absolute/TEST-inputs/package-A.zip \
  --updated-package /absolute/TEST-inputs/package-B.zip \
  --assets-root /absolute/checked-out-source \
  --pi-executable /absolute/verified-pi/pi \
  --report /absolute/TEST-evidence/installed-process.json
```

There is deliberately no `--pi-agent-dir`: actual `PI_CODING_AGENT_DIR` discovery
must select the TEST profile. Real Pi then auto-loads the installed observer.
Cases include JSON/print success, retry/followup final deduplication, error,
abort, a question kept pending until its webhook arrives, normal one-shot quit
with an already admitted held HTTP response, and a hung endpoint within the
original deadline. Configuration and consent off are separate negatives. A live
Pi A is retained through a real package update; its terminal event must finish
and its process/children be collected without a webhook. A fresh observer must
work after update and missing-directory repair. A current live Pi B is retained
through removal and must also finish without a webhook. A newly launched Pi
after removal must still complete its native provider request and foreign
witness, with no notification effect.

The separate executable replacement case retains a loaded Pi A while the normal
managed runtime writer installs a second full bundle built from the same source
with a distinct linker version. The unchanged-package common wizard refreshes
the owned runtime receipt. The fixture compares the real executable SHA with
both owned and derived metadata, keeps the package projection unchanged, and
checks all sibling consumer records, policy bytes and foreign profile files.
The old observer must finish with no webhook; a fresh Pi must deliver exactly
once. The TEST retainer and preparation consumers are retired through the
normal writer. No test calls `pi-event` or treats a mocked callback as native
evidence.

`scripts/pi-installed-desktop-linux.py` shares the same common setup and receipt
readers. It reuses the owned X11/PulseAudio session, runs the actual Pi TUI in
Xterm and holds the loopback provider until a separate window is foreground.
Completion and a still-pending blocking UI question each require the real dunst
banner, a retained screenshot, independently captured output PCM and a physical
mouse click that returns foreground and keyboard focus to the originating Xterm.
Native history content is read after the controlled dismissal of that same
banner. Sound-off requires zero PCM energy; click-off and a closed originating
window must leave the distractor foreground; desktop-off must show no banner or
audio. Every case inherits a foreign TEST `CLAUDE_PLUGIN_ROOT`, while helpers and
cues must still come from receipt-owned runtime assets. The private product
daemon and all terminals/providers/captures are collected.

Use the same arguments as above without `--updated-package` or
`--replacement-runtime-stage` for the Linux desktop script. Its receipt records
`native_product_observations_passed`, exact
input/fixture hashes and whether the source checkout was dirty. Its
`desktop_qualified` flag stays false: final acceptance still needs an exact,
clean, pinned candidate and the macOS/Windows Client native receipts. A working
local GOWORK investigation does not qualify the final linked module graph.

### macOS installed-product handoff

Reuse `tests/integration/pi_installed_fixture.py` for the actual loopback webhook
observer, supported schema 2 configuration (`preset: "custom"`), public setup
argv, binding/runtime readers and asset-receipt checks. Reuse
`tests/integration/pi_native_fixture.py` for the controlled model provider and
foreign native witness. `Provider(..., hold_reply=True)` allows backgrounding
the real Terminal window before final completion;
`TEST_PI_DEFER_DIALOG=1` defers the foreign test command's blocking input by one
second, allowing the question to open while Pi is backgrounded.

The existing native primitive is
`tests/integration/pi_native_preflight_darwin.swift`: its ScreenCaptureKit
capture, Accessibility/input permission checks, native mouse event injection
and foreground observation are reusable. The product observer must record
external product audio rather than play the preflight's synthetic `NSSound` cue.
Run Pi in a real disposable Terminal window and identify its native TTY/window;
a detached PTY alone cannot establish Terminal click-to-focus. Observe the real
Notification Center banner and physically click it while recording the returned
foreground window/TTY and pending question. Do not replace the notification
helper or fabricate its callback.

The supplied native binary stage must include the authenticated signed
`ClaudeNotifier.app` plus its external `.managed-runtime.json` attestation,
using the existing signing/acquisition and managed runtime writer. The writer
verifies/stages it and the installed receipt supplies its durable native path;
do not redirect it through ambient `CLAUDE_PLUGIN_ROOT`. The candidate main and
utilities, canonical source package, sound hashes, Pi acquisition and clean
merged module identity follow the input inventory above. Permission observed in
synthetic preflight is separate from the candidate helper's own notification
permission and visible delivery.

### Windows installed-product handoff

`Pi installed Windows product` separates an ordinary Windows Server amd64
build/process job from its Windows Client desktop job. The Server job builds
the exact embedded callback, main, utilities and focus companion and runs the
real installed Pi lifecycle suite. Its artifact cannot qualify desktop
delivery. The Client job first compiles
`tests/integration/pi_installed_desktop_windows.cpp` with the native MSVC image
compiler and parses the exact PowerShell Terminal child launcher. These checks
run before the render/input prerequisites, so an unavailable audio device does
not hide compilation or launcher errors.

On a configured Client desktop, `scripts/pi-installed-desktop-windows.py` runs
the installed Pi in two real, separately identified Windows Terminal windows.
It preserves Terminal's actual session identity, observes the native Shell
toast through UI Automation, records a screenshot and physically clicks its
visible point. Foreground and keyboard focus must return to the origin window.
External WASAPI loopback records the product cue; Media Foundation only decodes
the configured reference asset for comparison and never renders a replacement
cue. A pending question, sound-off, click-off, desktop-off and closed-origin
case use the same six-case contract as the other desktop lanes.

The workflow's `windows_client_runner` input, or
`CI_PI_WINDOWS_DESKTOP_RUNNER` repository variable, selects an existing Client
runner. Its default is the existing `windows-11-vs2026-arm` route. Shipped amd64
Pi and product binaries may run under Windows' x64 emulation on an ARM64 host;
the receipt records that distinction. The Client consumes the same run's exact
Server-built candidate and process evidence. It requires a genuine configured
render endpoint, active interactive desktop, Shell notifications and working
foreground/input observations. It does not install a driver or alter endpoint
defaults to make the test pass.

## Clean candidate and final three-OS gate

`scripts/pi-candidate-build.py` runs the ordinary four release build commands and
portable-package builder from one clean source checkout, with `GOWORK=off`. It
refuses local module replacements, missing linked module sums, modified VCS
identity, a mismatched native build host and a checkout changed by the build.
Windows additionally builds/verifies the embedded callback and focus companion;
macOS receives the already authenticated signed helper/attestation directory.
`build.json` records the exact source, module manifests/graph, actual Go build
information, staged inventory, portable A/B hashes and canonical asset hashes.
The separately inventoried `replacement-stage` uses the same release build
commands and linked source with a distinct version identity; its executable
must run and have a different SHA. Both stages are inputs to the process suite.
No release is published.

The installed Linux and macOS workflows retain these build inputs alongside
`acquisition.json`, `process.json` and their actual native desktop receipt. The
reusable workflows accept `source_sha` so callers can bind every job to the same
immutable source. Standalone PR jobs use the pull request head commit.

Run `Pi three-OS product qualification` with the exact source and the Linux,
macOS and Windows Client **product** run IDs. It collects only the corresponding
`pi-installed-<lane>-<SHA>` artifacts, then runs
`scripts/pi-qualification-aggregate.py`. The aggregate requires matching clean
source/module/asset identities, immutable Pi acquisition, all actual installed
process/lifecycle cases and all six real native desktop cases on each OS. It
also requires verified native Mac helper evidence and a Windows Client render
route, embedded callback, focus companion and truthful x64-emulation disclosure
when relevant. No callback/API submission or synthetic prerequisite receipt can
replace a product desktop receipt.

Missing run IDs/artifacts, skipped/unqualified cases, stale source, development
replacements, absent audio, uncollected actors or a question no longer pending
all leave `desktop_qualified: false` and a failing aggregate with per-OS reasons.
This gate must pass before claiming full native qualification or merging the
integration. The existing hosted Windows Client image has been observed with
running audio services but zero render endpoints in every state; its synthetic
foreground/input observers also failed. It cannot supply Windows sound evidence
until an actual configured Client render route is available. Installing a new
driver or substituting generated audio is not part of these fixtures.
