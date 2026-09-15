# uapinstaller external sample

Separate Go module. No `replace`, no `internal` imports, no raw Store/Kernel
wiring. Constructor proof:

```sh
GOWORK=off go get github.com/777genius/agent-notifications/install/uapinstaller@<commit>
GOWORK=off go run .
```

Prints `external import ok` and does not create `/uapinstaller-sample-state`.

Install → inspect → recover → no-op repeat → update → repair → remove requires
explicit absolute roots and a standard local package plus helper:

```sh
GOWORK=off go run . -state /abs/uap -package /abs/pkg -config /abs/config \
  -helper /abs/helper -client-exe /abs/client
```

Pass `-claude-config` (and optionally `-claude-exe`) to use published
`Request.Targets` for Claude+Codex together. That path needs a trusted Claude
Code CLI; a probe helper is enough for the Codex-only flags. Mixed PackageRoot
values stay unpublished for install and update. Group Repair of mixed live
revisions uses per-target `ClientTarget.PackageRoot` so each binding keeps its
recorded digest. Mixed Codex uninstall still needs Codex
`ExternalUninstalled`.
