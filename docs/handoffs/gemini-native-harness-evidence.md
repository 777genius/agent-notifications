> Historical worker handoff below. Current executed qualification is recorded in the final production supplement: all five native platforms passed. Static-worker limitations are retained as provenance, not current blockers.

# Gemini native harness handoff — an-next-g0

The worker implemented and statically checked the fixture. **No Gemini process, Google model service, AN consumer, desktop backend, or production installer was run. G0 and G5 remain unverified.** The trusted orchestrator must inspect and fingerprint these files before executing them outside the provider sandbox, with the guard unchanged.

Inputs: AN HEAD `571b303d80ff4a723a99f7bc698e732970df0888`; accepted contract `.research/accepted-plan.md` and R3 `.research/plan-r3.md`; exact public Gemini source reference `b460678f3db508407554afd604cc9d6635becb2a` (v0.62.0). Historical plan inputs at AN `7e8d868…` and UAP `8c8c5c5…` have not been rewritten. CONTRIBUTING.md was read; no applicable AN AGENTS.md was found. The historical `.research/uap-reference/` paths are absent in this worktree; this worker changes no UAP source. A single exclusive, task-specific `.git` lock creation returned EROFS (errno 30); no commit/history mutation or retry was attempted. Only the three owned files are added.

## Artifacts and red conditions

- `tests/integration/gemini_native_e2e.py`: Python stdlib provider/capture server, TEST isolation, hook recorder, bounded subprocess I/O, native scenarios, sanitized manifest. Exports `Fixture`, `Terminal`, `exercise`, `new_lab`, and `minimal_env` for the later built-artifact driver.
- `tests/integration/gemini_native_pty.cjs`: small Node bridge using only the explicitly installed CLI's `@lydell/node-pty@1.1.0` and its native platform addon. It refuses missing/mismatched modules or addons outside that installation. There is no global-module or Python-PTY fallback.
- This handoff, including actual worker checks and precise unresolved cases.

Red conditions were defined before implementation: missing actual CLI-emitted AfterAgent or ToolPermission, missing permission UI, nonneutral handler stdout, unsafe startup roots, incorrect own-file effect, output/request overflow, timeout, or unconfirmed child shutdown. Source inspection and future scenario results are separate evidence. A CLI/provider trial is not an installation or desktop pass.

The default command installs exactly two recorder hooks in new TEST settings: AfterAgent and Notification matched to ToolPermission. The fixture sends genuine GenAI candidate text/functionCall objects to the native GATEWAY provider, never hook stdin. The actual CLI decides permission, runs `write_file`, and invokes the hooks. The scenarios are two equal-text turns in one session, approve, deny, cancel at permission, and an ordinary recovery turn after cancellation. Approval must create exactly the own synthetic effect file; denial and cancellation must leave their files absent. Both a new native notification and a rendered permission-menu match are required before sending choice keys. The same session also catches accidental “always allow” approval through the next missing permission UI.

The recorder reads only actual hook stdin, validates trusted event/subtype/cwd/scalars, retains session/timestamp hashes and stop booleans, and prints `{}`. Optional `--probe-command` is a JSON object mapping each event to its **complete, explicit fixed argv** for an independent TEST SDK probe. No selector or product flag is invented/appended. It forwards the CLI-provided bytes once; captures only bounded stdout/stderr in memory; records exit/neutrality/hash metadata; and fails if stdout is anything other than whitespace-surrounded `{}` or exit is nonzero. Its executable must have a TEST-artifact marker ancestor and cannot be borrowed from HOME or the repository. This remains a native probe, not production installation/delivery evidence. Probe delegation is explicitly refused on Windows until its separate child-process-tree termination is qualified; the default recorder/ConPTY G0 path remains available there.

The fixture code is about 600 Python lines plus a small bridge. The additional lines bound stdin/output, enforce physical roots and explicit package/module identity, and confirm cleanup; they do not add a test framework, dependency, installer, or terminal emulator.

## Isolation and observed source facts

| Exact source copy / seam | Harness behavior and limit |
|---|---|
| `content-generator.ts:87–88,174–175,200–204` | Local `GOOGLE_GEMINI_BASE_URL` selects GATEWAY; only `GEMINI_API_KEY=an-gemini-test-not-a-secret` is supplied to bypass the preceding stored-key lookup. This is not genuine authentication or proof of every other startup path. |
| `paths.ts:23`, `storage.ts` | `HOME`, `USERPROFILE`, `GEMINI_CLI_HOME` all equal `<TEST>/profile`; `.gemini/settings.json` is below that home parent. XDG and temporary paths are all inside the new lab. |
| `settings.ts:107–121,566+` | System settings/defaults/trusted-folders paths point to owned files. Both `<TEST>/profile/.gemini/.env` and `<TEST>/profile/.env` are empty barriers. `advanced.ignoreLocalEnv` is additional protection; no unsupported `--ignore-env` argument is used. Cwd equals effective home, protecting the trusted and untrusted dotenv branches. |
| `settings-schema.ts:1443`, context settings | Empty memory boundary markers disable ancestor traversal; own empty GEMINI.md and an empty TEST Git repository also form context barriers. No external include directories or inherited MCP/skills are enabled. |
| `config.ts:1111+`, `settings-schema.ts:2548+` | TEST policy explicitly uses separate top-level `hooksConfig.enabled/disabled/notifications`, not legacy reserved event fields. Existing owner settings are never opened or enabled. |
| `settings-schema.ts`, `config.ts` | Auto-update and its notification, telemetry/prompt telemetry, usage statistics, terminal notifications and skills are disabled only in owned TEST config. Only `write_file` is in the core-tool allowlist; bypass tools are empty; default approval is forced, yolo/always-allow are disabled. `--skip-trust` is the source-supported **new TEST workspace** session choice, not a provider-guard change. |
| `env-var-resolver.ts` | Settings interpolate `$NAME`, `${NAME}`, `${NAME:-default}` before the shell. Variable-like paths, percent signs, backticks, double quotes and control lines are explicitly unsupported. Spaces/non-ASCII and shell-quoted single quotes remain representable. This TEST renderer is not the production UAP planner. |
| `shell-utils.ts:658+`, `hook-runner.ts:341+` | Native Unix hooks use Bash; Windows uses explicitly supplied pwsh via ComSpec, with profiles disabled by the native runner. Runtime Bash/PowerShell version expansion reaches the recorder and is validated/recorded; actual platform evidence is generated only on a real run. Windows PowerShell 5.1 fallback is deliberately unqualified. |
| `hook-event-handler.ts:138+,157+,371+`, native types | Event/subtype, session, ISO timestamp and stop flag are native fields. Unknown stdin fields, prompt/response/tool/message/details/transcript never enter evidence. Missing required facts fail; no parent/root/turn/request IDs are fabricated. |

The lab must be a previously nonexistent physical absolute path with a `TEST` or `TEST-*`/`TEST_*` component, outside HOME/current repository/current cwd and any existing repository ancestor. Its private marker is created by the harness. The separate CLI installation root must also be physical, marked as TEST, and outside HOME/repository. All launch environments are constructed explicitly; proxies, custom headers, Node options and credential variables are not inherited. Probe children receive that same explicit TEST environment. No credential, dotenv outside the lab, keychain, owner-installed CLI, or process-environment dump is used.

HTTP binds only `127.0.0.1` on an ephemeral port, has no outbound client, caps bodies at 1 MiB and requests at 128, and only serves v1/v1beta model generate/stream/count routes plus optional `/capture`. Unknown endpoints fail. Streaming uses bounded SSE `data:` frames with actual candidate content/parts. Nonstream generation currently returns fixed text without consuming the interactive stream script; its exact ancillary uses still need native qualification. The server owns one thread and closes its active socket/listener on shutdown. The PTY keeps a 64 KiB in-memory matching window and an 8 MiB total output limit; the bridge emits only classifications/module hashes, never terminal text.

There are bounded per-case waits and an absolute native-child watchdog. Cleanup targets only the spawned subprocess/group or owned ConPTY handle. Forced termination is a failed scenario. There is no unrelated process search, sweep, or kill. POSIX probe processes receive a fresh process group and bounded input/output/termination. Backend platform packages and cleanup behavior still require actual host execution.

## Source-supported fake-response seam decision

The supplied `config.ts:474+`, `content-generator.ts:216+` and `fake-content-generator.ts` prove official `--fake-responses` and `--fake-responses-non-strict` JSONL seams. `generateContentStream` consumes arrays of real GenAI candidate responses with prototype restoration; this would still exercise the native agent/tool/UI/hook loop, unlike BeforeModel text translation or injected hook stdin. The supplied translator does not justify using BeforeModel functionCalls.

HTTP remains the implemented default, so there is **no delta to the accepted HTTP plan**. Cancellation and denial can change how many model followups are consumed; a loaded fake-response queue cannot be re-armed at each observed scenario. Exact auxiliary method ordering/content and these decision branches have not been observed. Switching to the official fake seam can be evaluated after those calls are qualified, without claiming a live model service. A G5 webhook capture still needs the existing HTTP fixture.

## Trusted orchestrator run instructions

First inspect/hash the two executable source files and audit the remaining exact-package startup/UI/tool call sites below. Install exact `@google/gemini-cli@0.62.0` and its pinned prebuilt PTY packages in a separate owned TEST installation outside this provider sandbox. Resolve the package's bin bundle to its **physical absolute file**, and use an explicit physical Node executable. Do not invoke an npm/user/global shim. Create `.an-gemini-TEST` in that installation root to attest its disposable TEST ownership; the session lab itself must not exist yet.

Supply a reviewed `<CLI-TEST-install>/ui-contract.json` with exactly these fields:

| Field | Required value |
|---|---|
| `permission_pattern` | Regex for the actual 0.62.0 rendered permission menu, including the approval and rejection choices; maximum 512 characters. Python and JS regex syntax must both accept it. Avoid broad banner-only matches. |
| `approve` | Exact allow-once menu navigation/Enter sequence from that source/UI. Only number/up/down navigation followed by `\r` is accepted. |
| `deny` | Exact reject menu navigation/Enter sequence, same key restrictions. |
| `cancel` | Exact supported cancel key, JSON `"\u001b"` or `"\u0003"`. The subsequent recovery turn must work; exiting the process fails. |
| `source_sha256` | SHA-256 of the inspected exact-version UI source artifact. The hash is provenance, not an automatic proof that a regex describes the correct menu. |

No default menu labels/indexes are fabricated: the needed dialog/stream call-site copies were not supplied, and GitHub public-source fetch attempts were blocked by the provider's domain allowlist. Do not bypass that restriction. The trusted orchestrator can inspect its exact installed/source package before making this small contract.

Run, outside the provider sandbox, using your actual physical paths:

```sh
python3 /absolute/checked-harness/gemini_native_e2e.py \
  --gemini-executable /absolute/TEST-cli/node_modules/@google/gemini-cli/dist/index.js \
  --node-executable /absolute/node \
  --cli-install-root /absolute/TEST-cli \
  --lab-root /absolute/existing-parent/TEST-gemini-g0-new \
  --hook-shell /physical/path/to/bash \
  --ui-contract /absolute/TEST-cli/ui-contract.json
```

Stage the bridge adjacent to the Python file if moving the checked harness. The example bundle path must be checked against the actual exact package's bin mapping. No command in this document was used to launch Gemini in the worker. On Windows, the same Python/Node files require an explicit physical pwsh.exe with `--hook-shell` and an explicit OS directory with `--system-root`; put both TEST labs outside the inherited home and use actual canonical absolute paths. Native Windows availability is not claimed from this code.

The run writes `<TEST>/evidence.json`; stdout contains only its sanitized location/status and red classifications. The manifest records exact package/version, installed package tree/bundle/package JSON hashes, harness/bridge/Node/PTY hashes, event classifications/countable observations, actual shell version, UI choices/effects, own child exit and unchanged direct-TEST-settings state. It never embeds raw prompts, responses, tool arguments, terminal logs, credentials, inherited paths or unknown hook fields. The synthetic fixture files/settings are confined to the private TEST lab, not evidence captures. Keep that lab for inspection; cleanup belongs to its owner, with no sweeping deletion in this worker.

## G5 reuse and remaining gates

There is no `setup-gemini`, GeminiNotification route or Gemini-event parser in the inspected AN base's `cmd/internal/install` Go source. This worker does not invent its production flags or add a fake installer/consumer. The CLI command therefore always labels `G5` as unverified and settings lifecycle as `TEST_direct_settings_only`, even if its native scenarios pass.

The later production driver imports the same `Fixture`/`Terminal`/`exercise`. It creates `new_lab`, uses `minimal_env`, starts `Fixture(lab, capture_validator)`, and invokes the **actual implemented** production installer/candidate with its explicit TEST control/runtime/native roots and `fixture.url + '/capture'`. Install recorder hooks as foreign TEST observers before production installation if needed; preserve the actual production-owned entries, without replacing their commands by probes. The observer callback must report actual CLI-emitted observations; the capture validator must check the product's fixed copy/agent source against an independent contract, reject unknown sensitive content and return only `task_complete` or `permission_request`. No fixture API calls the AN consumer. Reuse the scenario driver, then run production repeat/update/remove and assert foreign TEST preservation and owned lifecycle through real receipts/config state. Production-handler stdout neutrality, delivery consent and built source/module/helper identities need independent actual evidence; the G0 recorder's `{}` does not prove them.

| Still unresolved | Required evidence; current classification |
|---|---|
| Remaining startup/auth/context/policy paths | Audit exact installed CLI call sites, including trusted-folders implementation, policy discovery and auxiliary authentication initialization. Given source seams and redirects alone do not prove every startup read or absence of outbound CLI networking. **Do not native-launch until those reads are confined to TEST/package/system runtime sources.** |
| UI labels, navigation, cancel behavior | Exact dialog source and actual native rendered menu/choices on each host. No keyboard indexes or Windows parity pass is assumed. |
| HTTP/SSE/count and nonstream auxiliary calls | Actual 0.62.0 request methods/paths and response expectations, especially nonstream structured/background calls. The fixture fails unsupported endpoints, overflow and missing UI/events rather than asserting compatibility. |
| `write_file` registration/schema/permission policy | Exact registry/core-filter/call-site inspection and own-effect observations. The named wire call is the proposed TEST tool; a missing actual UI is red. |
| Event semantics | `AfterAgent` is event-level completion, not task success. Equal-turn false stop flags are asserted; native true values may be recorded but no blocking hook is installed. `stop_hook_active` denotes continuation/recursion, not retry or event identity. Cancel absence is only a four-second bounded observation, followed by a real recovery turn. Blocking continuation, pre-response cancellation, retry, restart/resume and nested/subagent flow are unqualified; no terminal-false or main/root discriminator is fabricated. |
| Installer/SDK/delivery | G5 must use actual production installation/binary/SDK and delivery, not the probe route. Native install/repeat/update/remove and foreign preservation remain pending. |
| Platforms/desktop | No Mac/Linux/Windows native, OS API or visible-banner execution occurred. Even a future successful PTY trial cannot prove desktop submission or visual display; those manifest fields stay unverified. |

## Checks actually executed in the worker

Python AST parsing and `node --check` passed. Eight ordinary pure harness checks passed: candidate/tool wire shape and confinement without running the tool; subsequent text; endpoint/agent-loop/request limits; count response; rejection of unknown capture classification; refusal of existing/repository lab roots before writes; physical/symlink/variable-path refusal; minimal environment exclusions; and equal scripted turns remaining separate requests. These checks used stdlib and disposable scratch files only, without HTTP sockets, native CLI/PTY children, hook stdin injection or model execution. They do not prove native behavior. No Go toolchain, dependency installation, production tests, agent delegation, commits or releases were used.

Repeat syntax checks without creating repository bytecode:

```sh
python3 -c "import ast,pathlib; ast.parse(pathlib.Path('tests/integration/gemini_native_e2e.py').read_text())"
node --check tests/integration/gemini_native_pty.cjs
```

The final patch/new-file whitespace check and current owned-file status are recorded by the worker before handoff. Native observations remain for the trusted orchestrator; none are fabricated here.

## Trusted native qualification supplement, 2026-10-01

The earlier sections describe the worker's static handoff. The trusted orchestrator subsequently inspected the exact upstream UI/auth/policy sources and executed the installed `@google/gemini-cli@0.62.0` package in newly created TEST profiles.

- macOS arm64: all six scenarios passed, one native session; four AfterAgent and three ToolPermission observations. Equal-text turns had separate timestamps. Approve created the own TEST file; deny and Escape did not. Recovery completed. Child exited with code zero and no forced termination.
- macOS arm64, independent SDK probe: the same native scenarios passed while forwarding actual hook stdin to a separately built public-only UAP SDK consumer at source `2d2a26bf372aae286cd2f0dcbe40c47aa6d23b57`. Both handlers returned neutral `{}` and exit zero. Probe SHA-256: `9751dc1ee0f3ed500dbed1be7b03e672629b620198112f134f84a5897a8fca0f`.
- Linux amd64: the same native CLI scenarios passed using Node 24.21.0 and the exact installed CLI package. Desktop API and visual display remain unverified.

Native trials required these narrowly scoped fixture corrections:

1. Gateway auth uses the source-supported `security.auth.useExternal=true`; `GEMINI_FORCE_FILE_STORAGE=true` prevents native keychain fallback. A synthetic nonsecret key is still supplied.
2. Explicit `gemini-2.5-flash` avoids default-model routing calls requiring additional structured model responses. The successful fixture serves seven actual streaming model requests, with no live remote model claim. Ripgrep is disabled in the TEST profile.
3. `tools.core` is also a policy allowlist. `tools.confirmationRequired=["write_file"]` is required to exercise the genuine permission UI while keeping the single-tool allowlist.
4. Text and Return are sent separately because a single burst is interpreted by the native input box as a paste. Menu navigation is likewise sent as individual keys.
5. Both deny and Escape cancel use the native Cancel outcome. No AfterAgent was emitted during their bounded four-second observation; completion is not synthesized. The subsequent ordinary recovery turn produced AfterAgent.

UI provenance: exact `ToolConfirmationMessage.tsx` at upstream commit `b460678f3db508407554afd604cc9d6635becb2a`, with `RadioButtonSelect`/`useSelectionList` navigation inspected. The fixed TEST settings disable session/permanent approval, leaving Allow once, external editor and No/suggest changes. Actual rendered menu detection was required before any choice.

These are `native_cli/provider_substitute` results. Production setup, AN delivery, desktop API and a visible Gemini banner are **not** proved by the SDK probe. G5 remains pending until the production candidate is installed and exercised.


## Production qualification supplement, 2026-10-01

The trusted production G0/G5 run passed on macOS arm64/Intel, Linux amd64/arm64
and Windows amd64 at `cee81a77d8aef5369456cc24ad4e9be5184b3264`, GitHub run
`36859247789`. This supersedes earlier static-worker and partial-platform statuses.
Desktop GUI remains unverified on Windows, Linux and macOS Intel.

On the owner's Mac, the final public graph was exercised with actual CI binary
source `884e2b6f522010f0cac015d89a9933071e6d85d9`, driver source
`91b0c64732567962ac1a017e501b6643004ce90d`, public SDK revision `2d2a26bf372a`
and public agentplugins revision `11a842f55d19`. The binary's actual Go build
metadata confirms those modules without replacements. Candidate SHA-256:
`af603968ab97fabd10593e5fdc72830719115b60b1c20a6224abfe930d5de162`;
changed update candidate: `b560c7e38f16c1e604385b62a497331fbcf59569269b416549cdcb8b7dbd7d7f`.

Actual install/repeat/update/inspect/remove, six native scenarios and foreign-hook
preservation passed. Cached commands after normal removal produced no new webhook;
a damaged receipt revoked consent while retaining foreign hooks. Both owned native
sessions exited zero without forced termination. The existing signed macOS helper
returned ten correlated submitted receipts with the fixed copy checked, seven
completion and three permission alerts. Desktop submission was also observed while
webhooks were disabled. The owner separately confirmed seeing both fixed alert
texts in the earlier native trial. That visual record retains its earlier source
binding; it is not relabeled as a fresh attestation for this final-graph run.

Evidence is stored in the owned disposable `TEST-g5-final-11` lab, with hashes and
qualification linkage. This is actual CLI plus loopback-provider evidence, not a
live Google model-service trial or GUI qualification for other operating systems.
