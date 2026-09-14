# uapinstaller beta

Public process-local installer API for a standard local Agent Plugins package
(`plugin.json` + MCP/skills). This is the P1 subset: constructor, inspect,
prepare/apply for **install** and **remove**, and source-independent Recover.

Update, repair, and group operations are not published. Those requests fail
before mutation.

## Contract

```go
engine, err := uapinstaller.New(uapinstaller.Config{StateRoot: stateRoot, HelperExecutable: helper})
prepared, err := engine.Prepare(ctx, uapinstaller.Request{Operation: uapinstaller.OpInstall, ...})
defer prepared.Close()
result, err := engine.Apply(ctx, prepared, uapinstaller.Decision{Confirmed: true})
```

`New` validates paths and does not create directories, open a journal, or run a
helper. Directories are created on Recover or a confirmed Apply. Host seams may
replace args of one declared MCP server and observe a committed binding before
client activation. Optional `Config.Assess` is digest-bound: block and
unavailable never become allow. Nil assessor keeps the trusted-local MVP and
does not start a download scanner. Coarse `Config.Progress` phases are
observational. The engine does not import Notifications types and does not
query a live Claude/Codex identity by default.

Prepare copies `Request` and reports canonical `Plan.TreeDigest` with algorithm
`agentplugins-tree-sha256-v1`. That value is the packagedigest source identity,
not the installed packagesnapshot ArtifactDigest. An existing record whose
`TreeDigest` does not match the snapshot, including an old-bridge artifact
digest in that field, returns `ErrUpdateRequired` without rewriting state.
Remove Prepare verifies the managed artifact and persisted target before any
client deactivation. Last-client remove retains PLUGIN_DATA and reports
`data_retained`.

Codex artifact removal requires `Request.ExternalUninstalled`. Confirmed Apply
does not invent that attestation. A missing or relative helper is rejected
before the state file is written. Confirmed Apply returns `recovery_required`
when Inspect sees a pending journal or unfinished receipt; it does not recover
as a side effect of install/remove. Close during Apply returns `ErrHandleBusy`
without releasing the sealed snapshot. Install of a different TreeDigest for an
active binding returns `ErrUpdateRequired` before mutation.

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
`-client-exe` to run install → inspect → repeat → remove.

Long-term this package moves to
`github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/installer`.
