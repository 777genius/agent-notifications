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
client activation. The engine does not import Notifications types and does not
query a live Claude/Codex identity by default.

Codex artifact removal requires `Request.ExternalUninstalled`. Confirmed Apply
does not invent that attestation.

Long-term this package moves to
`github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/installer`.
