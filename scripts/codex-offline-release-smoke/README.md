# Offline actual Codex release smoke

Reusable adaptation of the reviewed v1.48.5 observer. This harness qualifies one
source-bound ARM release binary and signed native input with actual Codex Stop
execution against a loopback model fixture. A completed `smoke-report.json` is
required; preparing files or passing unit tests does not qualify release assets.

## Validation and runner

Use native macOS ARM Node 24 or newer for actual smoke phases. Node's built-in
TypeScript stripping runs these `.mts` files; it does not replace typechecking.
The development compiler is pinned to TypeScript 7.0.2, with the reviewed Node
22.20.2 type definitions, by `package-lock.json`.

```sh
cd scripts/codex-offline-release-smoke
npm ci --ignore-scripts --no-audit --no-fund
npm run typecheck
npm test
node run-phase.mts --help
```

Tests only construct synthetic ZIPs and validate input objects. They reject
missing/duplicate/traversal/symlink/foreign ZIP members, local/central mismatch,
missing or unknown input keys, invalid custody identities, non-TEST roots,
relative paths and invalid ports. They never run a release asset or agent.

`schema-0.162/` is the minimal generated type closure exported from actual Codex
0.162.0; it is type-only and is never used to manufacture runtime trust. The
prepare preflight requires actual `codex-cli 0.162.0` and its app-server entry
point. A different installed version requires protocol review and a fresh TEST
qualification before changing this pin. Actual `hooks/list` later verifies the
protocol, trust, hash and TEST scope before the only turn.

## Input contract

Create a mode 0600 JSON file with exactly the keys in `Inputs` in `inputs.mts`.
No release version, candidate SHA, operator SHA or workspace path is hardcoded.

| Keys | Required value |
| --- | --- |
| `candidateSHA`, `operatorSHA` | Full lowercase 40-hex C and O identities |
| `version` | Exact stable source/artifact version, `MAJOR.MINOR.PATCH` |
| `testThread` | UUID of the freshly approved TEST context |
| `signingRun`, `signingAttempt` | Exact successful signing run and attempt, decimal strings |
| `draftBinarySHA256`, `portableZipSHA256`, `nativeZipSHA256` | Verified downloaded asset SHA256 values |
| `signingRunJSONSHA256`, `nativeCustodySHA256` | Actual full run JSON and native custody receipt SHA256 values |
| `root` | Brand-new `/private/tmp/TEST-an-codex-UUID`, lowercase UUID |
| `source` | Clean checkout with HEAD exactly C |
| `portableZip`, `draftBinary`, `nativeZip` | Absolute verified asset paths, with canonical filenames below |
| `signingRunJSON`, `nativeCustodyJSON` | Absolute paths to the matching original receipts |
| `codex` | Absolute actual installed executable, never a proxy/shim |
| `modelPort`, `webhookPort` | Distinct unused loopback ports, integers 1025..65535 |

Canonical assets: `agent-notify-portable-darwin-arm64.zip`,
`claude-notifications-darwin-arm64`, and `ClaudeNotifier.app.zip`.

The observer independently joins asset hashes, portable executable bytes,
native executable/sidecar receipts, C, the successful owner-dispatched signing
run/attempt, signing branch/workflow, notarization, publisher team and bundle ID.
It retains these repository-specific publisher/workflow contracts. O is supplied
as release-controller provenance; the external release gate must verify O and
review the harness. This smoke does not replace that gate or qualify a new C
using an older binary.

## Explicit operator stages

Only use a newly approved TEST context. Actual prepare runs the artifact and
native verification/setup, which can register its signed TEST helper. Before
execution, retain actual-user helper hash/inode/ownership and LaunchServices
baseline; compare afterward and preserve the TEST profile if registration may
still refer to it. Do not copy credentials or real user configuration. Do not
reconcile the user's LaunchServices registration as part of this smoke.

Set `NODE_ARM64`, `SMOKE` (absolute path to this `run-phase.mts`), and
`INPUTS_JSON` (absolute private inputs path):

```sh
"$NODE_ARM64" "$SMOKE" plan "$INPUTS_JSON"
"$NODE_ARM64" "$SMOKE" prepare "$INPUTS_JSON"
"$NODE_ARM64" "$SMOKE" server "$INPUTS_JSON"
```

`prepare` fails before any turn on incompatible Node/architecture, Codex version,
spctl availability, bad source/asset custody, wrong version or setup contract.
Child PATH explicitly includes `/usr/sbin` for spctl. All HOME/CODEX/Claude/XDG
and temp paths are private TEST paths; no provider or credential environment is
inherited. Wrappers come from `git archive C`; symlink copies use
`verbatimSymlinks`. The provenance intent and exact executable share a private
0700 stage. `setup-products prepare --products codex --codex-executable ...`
checks SourceCommit/Version/SHA of that same executable before idempotent setup.
Repeated setup must preserve fixture Claude files and hook definitions.

Keep exactly one owned `server` process live, save its process handle and wait
for `server-ready.json`. From a separate genuine controlling TTY:

```sh
"$NODE_ARM64" "$SMOKE" trust "$INPUTS_JSON"
```

Open `/hooks`, inspect and trust only the two private TEST Stop handlers
(notification wrapper and observation tap), then exit normally. `trust` is the
public alias for the retained `tui` observer phase. Never write trust state by
hand, invent a trust API, use bypass flags, inject Stop, or invoke `tap` manually.
Authorized UI automation may operate this real TTY. Never submit a model prompt
in the trust TUI.

```sh
"$NODE_ARM64" "$SMOKE" turn "$INPUTS_JSON"
# SIGTERM the exact owned server wrapper PID and wait for successful closure.
"$NODE_ARM64" "$SMOKE" verify "$INPUTS_JSON"
```

The server and turn each have an exclusive `wx` start marker. Never rerun an
uncertain actual turn or restart a fixture in the same root. Inspect retained
evidence and stop on uncertainty. A fresh run requires an explicit new TEST
context and decision, not an automatic retry loop. Retain the server handle and
join it even if another phase fails. The runner bounds server time to 10 minutes
and other phases to 5 minutes; these are failure bounds, not retry permission.

Verification requires one request, one actual thread/turn completion, zero
executable tools, no Authorization headers, exact nonce response, one native
Stop correlated by session/turn/cwd, one webhook, unchanged 3-second quiet tail,
unchanged staged harness/Codex/binary/hook hashes, and closure/death of every
recorded child/group and server. Qualification claims only offline actual CLI
Stop/webhook execution. Desktop delivery, cold callbacks, physical Intel,
live-provider quality, exhaustive filesystem audit and egress firewall proof
remain outside this report.
