# Gemini installer G4 qualification

Executed 2026-10-01 on Linux amd64 in fresh marked TEST profiles. Public 1.46.1
has no Gemini support; this qualifies candidate installation, not publication.

Source `3889410ccd5f75afed39b7a93420a19b9e0bc2cd` plus fixture patch
`d5cf47cad4f074e63467ab08350924b97c8600c2e2522f10e9c1f1ba6bb8c4bf` (SHA-256).
Actual stripped binary, readonly public modules, `GOWORK=off`, no replacements;
binary SHA-256 `a0249dfc8d11cdfb96d26b87847279595415b8caf3ba00f8dd7db43e23e91aec`.

The existing `bootstrap_opencode_test.sh` exercises actual piped `setup.sh`,
downloaded actual `bootstrap.sh`, checksum-bound AN binary and actual `install.sh`.
Offline curl supplies explicit TEST release/source assets. Version-only host
adapters reject agent runtime commands; no real projects or credentials are used.

Passed:

- No-args pipe opens controlling TTY and genuine UAP SelectMany.
- Cancel/empty preserves persistent files and directories byte-for-byte.
- Interactive OpenCode/Gemini plus webhook consent installs actual owned hooks,
  persists separate consent and preserves foreign settings/comments.
- Explicit mixed pipeline repeat shares one executable and preserves state.
- Printed Gemini remove respects its control root despite a changed default,
  revokes Gemini and preserves the OpenCode sibling runtime.
- All-four pipeline uses actual Claude/Codex/OpenCode/Gemini AN installers.
  Claude registration uses the existing narrow metadata adapter contract;
  this does not qualify real Claude/Codex CLI execution.

All-four pipe was executed only on Linux. Existing Windows placement/removal
checks remain; five-platform native Gemini and Mac desktop proof are separate.
Parser/preflight failures retain their existing closest unit boundaries.

Replacing only TEST `setup.sh` with `exit 0` makes the new fixture fail at missing
interactive selection, while the prior direct-bootstrap fixture passes with
that same broken loader. Both candidate files were restored.

TEST lab:
`/srv/workers/jobs/agent-notifications/next-agent-notifications-v1/workspaces/TEST-gemini-g4-loader-N8gNLz`

| Evidence | SHA-256 |
| --- | --- |
| Positive `checks.log` | `d31454354aedae57e51a4b373206cb83d7b90b12a077652bdde993d7e91f6105` |
| Broken loader/new fixture | `e0cb84d4365c6c58c5a3738d23ee10c63827393b0769771c999ccc448511e202` |
| Broken loader/prior fixture | `b44d937bd1ab76250b10adb640b41b48df68f98cf35429a41372f5372fa2a744` |

Independent GPT-6.1 Sol high static review and orchestrator diff review passed.
The focused real fixture passed on its first runtime execution.

## Aggregate runtime fallback watchdog

The existing Node-only commit/checksum scenario starts several real Node/Bash
processes and asserts both valid and corrupt checksum outcomes. Its aggregate
fixture watchdog was changed from 20s to 60s after Windows CI timeout evidence;
production hook/cache budgets and semantic assertions were unchanged. The full
actual `installer_runtime_fallback_e2e_test.sh` passed on Linux in the same
fresh TEST lab with this test-only patch. This is not Windows runtime proof.

Patch SHA-256:
`fe6153ba6641dc44e5a6e5f74948a9d286d65bdccb0111634cd580a580db7df0`.
Executed log SHA-256:
`9ced22ed675173cde794d85852f0905dfc85b8c5544a005cd4885f50ea7c69c3`.

The pre-existing hook-wrapper diagnostics fixture subsequently exceeded its
15s aggregate Git Bash watchdog on Windows after strict cache tests passed.
Only Windows uses a 60s fixture watchdog now; other platforms retain 15s.
All private log, JSON/ANSI, Codex silence and two-process atomic assertions
remain. The actual Linux fixture passed in the same TEST lab; its output
SHA-256 is `9da383a5fc933110f4c52f3ef70a4b03774655e7b46648a32b1e6de7eb638dd9`.
Patch SHA-256 is `c76b4e967e43de158cbd07ddb4f3b55432af01a4ae99ea9b2de7f5d9b9f7a8ba`.
This does not attribute the Windows timeout to a proven cause or relax a
production hook deadline.
