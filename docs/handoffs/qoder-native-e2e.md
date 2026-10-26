# Native Qoder qualification

Use `scripts/qoder-native-e2e.mts` with a new `TEST-` directory. The harness
isolates HOME, QODER_CONFIG_DIR, cwd and Notifications configuration; it does not
read or copy authentication files. `turn` accepts a physical existing TEST root
so a person may first complete the official `qodercli login`. `resume-turn`
additionally requires `setup-qoder inspect` and native CLI listing to agree on
the existing managed Qoder installation before updating it.

```sh
node scripts/qoder-native-e2e.mts /absolute/qodercli /absolute/claude-notifications \
  /absolute/TEST-qoder-e2e resume-turn /absolute/ClaudeNotifier.app DeepSeek-Flash
```

Omit the model to use the account default. Use `-` for the native app argument
to qualify webhook delivery only. Only select model names actually advertised
by `qodercli --list-models` for the TEST account; the harness never changes the
account default. `lifecycle` uses a new profile without a model turn.

Each immutable JSON receipt binds CLI, Notifications executable and harness
SHA256 values and records native subprocess closure, timeout, bounded output,
native installation/update/removal and exact loopback webhook count. A real
turn must return the completion marker and produce exactly one structured
webhook with Qoder agent identity, task-complete status and the exact native
session ID. A webhook's normal notification body need not expose model text.
The harness never supplies a synthetic hook payload. Desktop delivery needs
independent OS observation; `desktopObserved` deliberately remains false in the
automated receipt even when the signed helper is installed and consent enabled.

For deterministic local inference, run the repo-owned fixture separately:

```sh
node scripts/qoder-native-e2e.mts provider-check
node scripts/qoder-native-e2e.mts provider 18791
```

It binds only `127.0.0.1`, serves `/v1/models` and OpenAI Chat Completions
(JSON and SSE), and rejects other endpoints. It logs only request path, count
and byte length. There is no command execution or upstream provider call.
In the authenticated TEST profile, use the official `/model` UI: Custom → Add
custom model → OpenAI-compatible → Chat Completions. Set Base URL to
`http://127.0.0.1:18791/v1`, Model ID to `TEST-qoder-local`, leave API Key empty
(the wizard explicitly allows unauthenticated endpoints), then Review and save.
Accept the default 128K TEST budget. Do not change the account default; pass
`TEST-qoder-local` as the harness's explicit model argument. The live 1.1.79
wizard accepted this configuration under Local endpoints. It reported zero API
time and zero tool calls when closed before the test turn.

This qualifies a genuine native Qoder turn, hooks and notification delivery with
a deterministic local model transport. It does not qualify Qoder cloud inference
availability or a third-party model's behavior.

## Qualified native result, 2026-10-26

The corrected candidate passed a real native turn using the officially configured
local model, signed desktop helper, managed channel consent and loopback webhook.
Its immutable receipt is
`/private/tmp/TEST-qoder-native-e2e-20261026/evidence-1793037340064-b9275554-318c-4b15-b968-79eaabbe6ade.json`.

- Candidate executable SHA256:
  `b21092292720759319b27f70b98b736b2dcc638e070229a4e37673eabed4442a`.
- Executed harness SHA256:
  `f39623e669605b6fa304ce5999e05b44cc8982612acdf9eaca2cb58909fb908d`.
- Native session `ed9c01d8-aa42-438d-ad0d-40802f4e5a92` returned
  `TEST_QODER_DONE` and sent exactly one structured notification with
  `agent_source=qoder`, `status=task_complete`, `notification_type=task_complete`
  and that exact session ID.
- Managed update and removal passed; native CLI final listing was `[]`.
  All ten native/helper subprocesses closed and their PIDs were absent.

A separate fresh corrected lifecycle passed installation, native disable/enable
(actual native settings saves after our installer), managed update and removal.
There was no native lock-name collision and the final native listing was `[]`.
All eleven subprocesses closed and their PIDs were absent. Receipt:
`/private/tmp/TEST-qoder-managed-final-lifecycle-20261026/evidence-1793037402208-4d397315-54ba-4aca-aa1e-719cb56dde68.json`.
This used the same candidate executable and final harness SHA256
`13dcd1dfaae09d3d42d4fe8598339a4b0f8c73413e46f8c695b61930271c8b8e`.
Strict TypeScript checking and the provider self-check passed. The loopback
provider and native TEST processes were stopped; the authenticated TEST profile
is retained for downloaded-release qualification without copying its credentials.

The preceding real turn at 19:54:07 also delivered one webhook and submitted the
desktop notification via the signed helper. The human independently confirmed
the banner to the root agent; record this separately as
`humanDesktopObserved=true`. The automated receipt correctly retains
`desktopObserved=false`, since it does not inspect the OS UI.

That preceding receipt
`evidence-1793037223405-74d62f17-52f9-4ed5-ab4b-5c35b9ce3020.json` reported a
harness assertion failure: Markdown cleaning changes `TEST_QODER_DONE` in the
notification body to `TESTQODERDONE`. The product delivered correctly; the
corrected harness checks structured agent/status/session identity and checks the
model completion marker independently. The earlier receipt remains unchanged.

The official 1.1.79 macOS arm64 CLI archive was downloaded from the URL and
SHA256 in its published manifest, independently matching
`b489a8341ea3589b3ecfa6c1105341203007a4412adcd88fa6a5d9564137027b`.
Sources: [installation](https://docs.qoder.com/cli/installation),
[CLI reference](https://docs.qoder.com/cli/cli-reference),
[native plugins](https://docs.qoder.com/cli/plugins-reference),
[hook exec/env contract](https://docs.qoder.com/cli/hooks).

The first TEST real turn on 2026-10-26 authenticated successfully and native
managed installation exposed all six hooks. Inference did not complete:
Qoder returned HTTP 403, code 10605, `RESOURCE_LIMIT`, `isQueued=true`,
`serviceAvailable=false`, retry-after 30 seconds for default model `qfmodel`.
At the harness's 120-second bound it exited 143; its PID was absent afterwards.
No Stop event or notification was observed. This is a provider-capacity failure,
not successful notification E2E or evidence of a hook deadlock. The failure
receipt remains `/private/tmp/TEST-qoder-native-e2e-20261026/evidence.json`.
An independent managed update subsequently passed. The account advertised
`Qwen3.8-Flash` and `DeepSeek-Flash` for a separately authorized bounded attempt.
The explicit DeepSeek-Flash attempt used `dfmodel` and encountered the same
capacity response; its final-candidate receipt is
`/private/tmp/TEST-qoder-native-e2e-20261026/evidence-1793036647234-464300ef-b3f8-4890-be95-748fcefb486f.json`.
Subsequent official `/model` UI separately reported ProTrial, Available Credits
0 and “Credits exhausted”; this is direct UI evidence beyond the earlier
resource-limit response. Neither failed attempt produced a Stop event.

The real TUI also detected an installer lock-name collision: a regular
`settings.json.lock` made Qoder's directory-lock cleanup fail with ENOTDIR while
saving TEST folder trust. The exact zero-byte TEST artifact was preserved as
`settings.json.lock-installer-conflict-evidence`. This requires the corrected
installer's separate lock namespace; moving a disposable reproduction artifact
does not qualify an unfixed production binary.

The hardened harness then passed managed install/update/remove in a separate
new TEST profile, without inference: all eight native/helper subprocesses closed,
their PIDs were absent and the final native registry was empty. Its immutable
receipt is
`/private/tmp/TEST-qoder-managed-lifecycle-20261026/evidence-1793036570798-79129808-428a-43a3-b04d-e6a29626deaf.json`.
That baseline binary SHA256 was
`d4a9123ba848af2399a5fe684b59ff931054bcfd3ddf12678225b818148d6219`;
this lifecycle result is separate from final candidate and real-turn qualification.

The v1.49.8 helper ZIP matched the published checksum
`e0fda3710fe8c4d0e2c11a8f397af4078bf7a161d6f4ae132c83bf8f9b17a669`.
`codesign --verify --deep --strict` passed, Developer ID team `86399583GS`,
with a stapled notarization ticket. GitHub attestation lookup returned 404;
there is no attestation-success claim.
