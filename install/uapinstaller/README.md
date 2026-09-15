# uapinstaller beta

Public process-local installer API for a standard local Agent Plugins package
(`plugin.json` + MCP/skills). Constructor, inspect, Recover, and prepare/apply
for **install**, **update**, **repair**, and **remove** are published. One
client uses `Request.ClientID`. Claude+Codex together uses `Request.Targets`
with the same operation verb. `install-group` as an operation name is invalid.
Two PackageRoot values in one install or update group stay unpublished.
Repair of mixed live revisions uses per-target PackageRoot so each binding
keeps its exact recorded digest. Group Repair with one PackageRoot across
mixed live revisions is `ErrUpdateRequired` before effects.

Update and repair of a missing owned binding return `ErrNotInstalled` before
mutation. Repair rematerializes a missing target of a live binding.
Repair of one live binding uses that binding's recorded package, not the
installation's latest Source.TreeDigest, so an older sibling can be repaired
after a subset update. Repair of that same binding with a different digest
stays `ErrUpdateRequired`. `InspectedBinding.TreeDigest` is that binding's
recorded package digest; mixed live revisions are not collapsed to
`InspectedInstallation.TreeDigest`. Apply `ClientResult.TreeDigest` is that
same per-binding digest, including unchanged mixed group Repair.
`OnCommittedBinding` receives the same per-binding digest, not the first
group envelope. `ProjectArgs` BindingFacts use the envelope digest of the
client being staged.

## Contract

```go
engine, err := uapinstaller.New(uapinstaller.Config{StateRoot: stateRoot, HelperExecutable: helper})
prepared, err := engine.Prepare(ctx, uapinstaller.Request{Operation: uapinstaller.OpInstall, ...})
defer prepared.Close()
result, err := engine.Apply(ctx, prepared, uapinstaller.Decision{Confirmed: true})
```

`New` validates paths and does not create directories, open a journal, or run a
helper. ClientConfigRoot is an explicit request path; CODEX_HOME,
CLAUDE_CONFIG_DIR, and HOME are not read as profile defaults. Directories are
created on Recover or a confirmed Apply. Host seams may
replace args of one declared MCP server and observe a committed binding before
client activation. Optional `Config.Assess` is digest-bound: block and
unavailable never become allow. Mixed group Repair assesses each distinct
snapshot digest once; blocking any snapshot refuses Prepare without mutation.
Same-root group Repair assesses that snapshot once. `New`, Inspect, Discover,
and Recover do not invoke Assess. Nil assessor keeps the trusted-local MVP
and does not start a download scanner. Coarse `Config.Progress` phases are
observational. The engine does not import Notifications types and does not
query a live Claude/Codex identity by default.

Prepare copies `Request` and reports canonical `Plan.TreeDigest` with algorithm
`agentplugins-tree-sha256-v1`. That value is the packagedigest source identity,
not the installed packagesnapshot ArtifactDigest. Scratch `TempRoot` must not
overlap the package source, including case, symlink, and Unicode NFC/NFD aliases
when the filesystem presents them as the same directory. After a terminal
remove, a later install of the missing client uses the explicit profile in the
new request; sibling bindings and PLUGIN_DATA stay. An existing record whose
`TreeDigest` does not match the snapshot, including an old-bridge artifact
digest in that field, returns `ErrUpdateRequired` without rewriting state.
Remove Prepare verifies the managed artifact and persisted target before any
client deactivation. Last-client remove retains PLUGIN_DATA and reports
`data_retained`. A later remove of that retained empty installation returns
`already_absent` without creating a journal, including when a sibling client is
still installed. `LocalPackageTreeDigest` reports that same canonical digest
without writing state. Retained metadata Update of a different digest is
`SwitchRetained`; a later Add is a separate Install. Plan of that Update is
metadata-only. SwitchRetained Progress is prepare/preflight/commit/complete
and does not report stage, activate, or verify. Cancelled SwitchRetained
reports no Progress. RequiredComponents do not apply to SwitchRetained;
completeness is checked on the following Install. A blocking Assess refuses
SwitchRetained without rewriting retained source. A different plugin.json
name is `package_identity` before mutation. A source already bound to
another installation is `source_collision`. Successful SwitchRetained
reports `data_retained` and a `data_compatibility` next action. An
ambiguous Save of that metadata is retried; a remaining Save error is
not `unchanged` even if Inspect already shows the desired digest. Plan of Install onto
retained r1 with package r2 is `ErrUpdateRequired` and shows both phases
before confirmation. Plan includes the helper protocol
version and SHA-256 of the helper bytes; UAP managedstdio stores the same
digest. Result.NextActions cover recover, update, reprepare, and activate after
a managed commit whose client activation did not finish. Cancel before the first
durable effect returns cancelled and writes no state. A host callback or context
cancel after that commit returns incomplete with the binding retained; it is not
a bool and does not roll the managed package back. Group retry reconciles each
pending committed binding before the no-change shortcut and before client
VerifyOnly; Inspect does not invoke that callback. Client.Materialization and
Client.Activation stay separate fields.

Confirmed Apply re-reads live target/ownership before mutation and returns
`plan_changed` instead of applying a stale confirmation. Discover reports
supported Claude/Codex user-scope metadata, executable presence, and current
bindings without creating state or executing found files. Inspect after a group
install reports both live bindings and TreeDigest without mutating state or
running a helper; Recover of that clean observation is `already_recovered`.
Recover of a stale observation whose journals vanished still requires leftover
swap artifacts to be absent; leftover `.agentplugins-staging-*` or
`.agentplugins-backup-*` beside the recorded target is `incomplete_recovery`,
not success.
Inspect and Result expose per-client materialization/activation/authentication/verification and
required components. The external sample's flagged path runs install → inspect
→ recover → repeat → update → repair → remove. Passing `-claude-config`
uses `Request.Targets` for the same verbs on Claude+Codex together.

Codex artifact removal requires `Request.ExternalUninstalled`. Confirmed Apply
does not invent that attestation. A missing or relative helper is rejected
before the state file is written. Confirmed Apply returns `recovery_required`
when Inspect sees a pending journal or unfinished receipt; it does not recover
as a side effect of install/remove. Close during Apply returns `ErrHandleBusy`
without releasing the sealed snapshot. Install of a different TreeDigest for an
active binding returns `ErrUpdateRequired` before mutation. Update of one live
client CompatibilityChecks remaining live siblings; missing sibling profile
data returns `ErrCompatibilityUnavailable` before mutation.

## External sample

`example/` is a separate Go module. It does not use `replace` or import
`internal`. From that directory:

```sh
GOWORK=off go get github.com/777genius/agent-notifications/install/uapinstaller@<commit>
GOWORK=off go run .
```

Or clone `example/`, run `GOWORK=off go get` of the same package path, then
`GOWORK=off go run .`. A successful import prints `external import ok` without
creating state. Pass `-package`, `-state`, `-config`, `-helper`, and
`-client-exe` to run install → inspect → recover → repeat → update → repair → remove.
Inspect prints `inspect-bindings` for the live clients. Add `-claude-config` for the published Claude+Codex group path.

Long-term this package moves to
`github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/installer`.
