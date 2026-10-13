# Retained Windows callback foundation (checkpoint A)

This is the classic unpackaged Windows SDK/C++WinRT implementation. It has no
Windows App SDK/NuGet, cgo, background service, runtime downloads or PATH helpers.
The Go adapter is intentionally unavailable, including asset-tagged releases.
Presence of the helper and a successful build do not establish route readiness.

`build.ps1 -OutputDirectory <job-owned-directory> -EmbedAsset` compiles x64
`helper.exe` and inert `contracts.exe`, retaining MSBuild logs and source/digest
metadata. `-EmbedAsset` copies the exact compiled bytes and emits their SHA-256
into build-tagged generated Go source. Production release inputs must run this
before Go compilation with `-tags windows_callback_asset`. Generated binaries
and Go asset source are ignored and never checked in as trusted placeholders.
Untagged, non-Windows and non-amd64 builds expose an unavailable asset. Native
helper is compile-only here; contracts.exe never installs, registers or launches.

Read WIRE.md before extending producers or installers. Checkpoint B must create
one complete owner-protected generation transaction, publish exact
`"<physical-root>\helper.exe" --callback` LocalServer32, shortcut/AUMID/CLSID and
binding last, and qualify installed read-only readiness + opt-in Show + genuine
cold Shell callback with trusted release bytes. Registry, shortcut, permission
observation and native toast submission are intentionally absent from A.

Callback mode accepts only `--callback -Embedding`. The generation comes from
its physically held executable. Every callback uses a fresh random attempt and
entry-time absolute boot deadline clipped to the process lease minus collection
reserve. Record references are reusable for later deliberate clicks; there is
no cross-restart/redelivery exactly-once claim. Two nonqueued slots include
validation and actual pending operation lifetime; busy requests are rejected
before admission. S_OK acknowledges handled admission only.

Query and launch deadlines are captured before entry and never restarted.
Expiration publishes unknown while retaining the pending API until actual
completion. No Cancel result, timeout, process absence or error grants replay
or a fallback launch. A hard own-incarnation lease starts before guards/COM
publication and bounds blocked calls. Termination at that lease preserves
unknown intent/records and does not certify global async quiescence. Local
thread completion is recorded separately; accepted still means handoff only.
All intent/publication boundaries require checked write, flush and close.
An accepted .result is a candidate: missing/partial/late/uncertain collection
remains unknown under the authoritative WIRE.md contract. An entered SDK
creation with unproved completion retains the same worker/slot through the
existing hard lease, even if result publication also throws. Clock readback and
filesystem publication are not atomic across a crash.
SDK FullName checks plus exact PFN are not atomic against package updates.

Protected owner+SYSTEM DACL and no-reparse held physical ancestor/file handles
protect custody against other principals/redirection. These are not genuine-click
cryptographic attestation: the same principal may inject COM or alter owned
files. Installer B must exclusively create immutable records and verify release
bytes; user-supplied hashes do not establish official vendor provenance.

Focused required validation: native x64 SDK compile, inert independent golden
Go/C++ codec checks, concurrent callback/deadline/unknown/drain regressions, and
Go tagged/untagged asset custody checks. No TEST OOBE, UIA, deployment or proof
files are dependencies of this helper. No shipping PASS before checkpoint B.
