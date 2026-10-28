# Pi native evidence

`pi-native-preflight.yml` runs the exact pull-request checkout on native Linux,
macOS and Windows. It exercises independent synthetic GUI, foreground, input and
audio observers before any candidate product is installed. Its receipt always
has `desktop_qualified: false`; `prerequisites_ready` establishes these observers,
not a Pi banner, notification permission, product sound or notification click.
Unavailable prerequisites exit nonzero with an `unqualified` receipt. Windows
Server cannot substitute for the required Windows Client desktop.

The Linux fixture owns its Xvfb display, Openbox, D-Bus session, dunst, two xterms
and PulseAudio null sink. It activates both windows, records a known PCM cue from
the sink monitor, and clicks a synthetic dunst notification using native input.
macOS uses actual session/permission observations, two disposable native windows,
native mouse input and ScreenCaptureKit audio capture. It never requests or grants
permissions. Windows observes the actual WTS/input desktop and Client SKU, creates
two disposable windows, verifies native input/foreground observation and records
a known cue through WASAPI loopback. No process/return-code observation is treated
as audio playback evidence.

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
