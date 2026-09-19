# HANDOFF: UAP installer SDK + notification wizard

**Как прочитать (не `/opt/cursor/artifacts`):** этот файл живёт в git, не в VM прошлого агента.

```
git fetch origin cursor/uap-installer-handoff-doc-6c84
git show origin/cursor/uap-installer-handoff-doc-6c84:docs/handoffs/uap-installer-sdk-and-wizard.md
```

GitHub: https://github.com/777genius/agent-notifications/blob/cursor/uap-installer-handoff-doc-6c84/docs/handoffs/uap-installer-sdk-and-wizard.md

Сырой текст: https://raw.githubusercontent.com/777genius/agent-notifications/cursor/uap-installer-handoff-doc-6c84/docs/handoffs/uap-installer-sdk-and-wizard.md

**Дата снимка:** 2026-09-16 05:57 UTC  
**Автор этого агента:** Cursor Grok 4.6 (cloud), run suffix `-6c84`  
**Статус цели:** **НЕ ЗАКРЫТА.** Не вызывать `UpdateGoal complete`. Не считать план выполненным.

Этот файл — передача другому агенту: весь текущий статус, ограничения, что сделано, что нельзя трогать, как продолжать. Можно открыть локально и вставить в новый чат как системный бриф.

---

## 0. Коротко для человека (RU)

- **Цель не доделана.** План `docs/plans/uap-installer-sdk-and-notification-wizard-plan.md` (ред. 10, 2026-09-14) требует UAP SDK P1–P3/P5 **в репозитории UAP**, плюс мастер P6, P7, native/reboot E2E, полный R3.
- **Работа шла почти только в `agent-notifications`.** В UAP (`universal-agent-plugins`) пуш **запрещён**: `Permission to 777genius/universal-agent-plugins.git denied to cursor[bot]` (HTTP 403). UAP только читался (клон `/tmp/uap`).
- Hosted пакет `install/uapinstaller/` в notifications — **временный публичный SDK**, пока UAP unpublished.
- P6 wizard PR **#183** HEAD **`ebfb3e1`**, все **9 named CI jobs зелёные**. Unblocked P6 на этом стеке уже в этом SHA.
- Оставшееся с этого агента **заблокировано**: UAP 403, NativeObserver unset, native/reboot `not_run`, Windows 0111, P5 TTY, group mixed rematerialize, unpublished SwitchGroup, полный R3.
- **Не пушить** уже зелёные P4 #182 / P4a #180 / P7 #185/#186/#187/#188/#189 поверх их текущего HEAD «на всякий случай».
- **Не мешать P7 Linux/Windows в wizard-стек.**
- **Не включать NativeObserver.** **Не публиковать SwitchGroup.** **Не выдумывать inspect JSON polish.**

---

## 1. Objective for the next agent (do not shrink)

Implement the UAP installer SDK and notification wizard **strictly** according to:

```
origin/docs/uap-installer-sdk-plan:docs/plans/uap-installer-sdk-and-notification-wizard-plan.md
```

Revision 10, 2026-09-14. Local copy used by this agent: `/tmp/uap-plan.md` (262566 bytes). The file is **not** on the wizard branch working tree.

Cover **every** phase, deliverable, acceptance criterion, and test from the plan; then verify e2e against current repo state.

**Do not mark the goal complete** until every numbered plan item has **current-state evidence**. Native/client/reboot E2E remain `not_run`. UAP P1–P3/P5 cannot be pushed without write access to UAP.

Plan is the source of truth. Presence of a bullet in the plan does **not** mean it already works.

---

## 2. Repositories

| Repo | Role | This agent could |
|---|---|---|
| `github.com/777genius/agent-notifications` | Workspace `/workspace`. Notifications, hosted SDK, wizard, P4/P4a/P6/P7 | **read + write + PR** |
| `github.com/777genius/universal-agent-plugins` | Real UAP installer engine. Clone `/tmp/uap` | **read only**. Push 403 |

UAP clone HEAD when last probed: local branch `cursor/uap-installer-p1-sdk-6c84` at `e02bf7ea3a2de6059132169b281862cb52df2912` (plan baseline). Earlier conversation also saw readable UAP HEAD `b072dc586b8bc1ba364fc944e34cec4a64b58f71`. Re-fetch before assuming.

**UAP push probe (2026-09-16):**

```
remote: Permission to 777genius/universal-agent-plugins.git denied to cursor[bot].
fatal: unable to access 'https://github.com/777genius/universal-agent-plugins.git/': The requested URL returned error: 403
```

If the next agent **gains** UAP write access, P1–P3/P5 belong **in UAP**, consumed by notifications via **exact Go pin**, not `replace`, not Git stack across repos.

Notifications pin (do not float):

```
github.com/777genius/plugin-kit-ai/install/integrationctl v0.0.0-20260910100557-6af7f412cb4a
```

---

## 3. Preferred bases (do not guess)

- Cursor cloud preferred base is often `main`.
- **This plan requires Git base `feat/agent-notify-e2e` until #177 merges.**
- Child PRs target **immediate parent branch**, never merge back into #177.
- Do **not** fold wizard/SDK into #177.
- Independent Linux/Windows P7 also use earliest sufficient parent (`feat/agent-notify-e2e`), **not** the wizard stack.

#177 current (2026-09-16): OPEN, **`isDraft=false`**, head `89d8524`, base `main`.  
Plan §0.4 wanted it temporarily draft; that is **not** this agent’s job unless explicitly asked. Do not convert #177 to draft as a drive-by.

---

## 4. Stacked PRs (notifications) — current SHAs

All except #177 are **draft**. Do not undraft unless asked. `update_pr` with `draft: true` **fails**; omit `draft` on updates.

| PR | Scope | Branch | Base | HEAD (short) | Push more? |
|---|---|---|---|---|---|
| [#177](https://github.com/777genius/agent-notifications/pull/177) | P0 / R0 foundation | `feat/agent-notify-e2e` | `main` | `89d8524` | only its own R0 defects |
| [#180](https://github.com/777genius/agent-notifications/pull/180) | P4a handoff guard | `cursor/uap-installer-p4a-handoff-6c84` | `feat/agent-notify-e2e` | `e29dbd1` | **NO** (already green) |
| [#182](https://github.com/777genius/agent-notifications/pull/182) | P4 hosted SDK + portable pin | `cursor/uap-installer-sdk-adapter-6c84` | `cursor/uap-installer-p4a-handoff-6c84` | `3914f92` | **NO** (already green) |
| [#183](https://github.com/777genius/agent-notifications/pull/183) | **P6 wizard** | `cursor/uap-installer-wizard-6c84` | `cursor/uap-installer-sdk-adapter-6c84` | **`ebfb3e1`** | only if new unblocked P6 or confirmed CI-fail fix |
| [#185](https://github.com/777genius/agent-notifications/pull/185) | R0 capability-aware MCP configure | `cursor/uap-installer-r0-configure-6c84` | `feat/agent-notify-e2e` | `cd6c672` | **NO** (do not mix into wizard) |
| [#186](https://github.com/777genius/agent-notifications/pull/186) | P7 Linux clock/delivery/none-setup | `cursor/uap-installer-linux-clock-6c84` | `feat/agent-notify-e2e` | `0c4c170` | **NO** |
| [#187](https://github.com/777genius/agent-notifications/pull/187) | P7 Windows clock/toast/none-setup | `cursor/uap-installer-windows-notify-6c84` | `feat/agent-notify-e2e` | `05d0c6d` | **NO** |
| [#188](https://github.com/777genius/agent-notifications/pull/188) | P7 Linux MCP configure on none-setup | `cursor/uap-installer-linux-configure-6c84` | `cursor/uap-installer-linux-clock-6c84` | `1cc5ad1` | **NO** |
| [#189](https://github.com/777genius/agent-notifications/pull/189) | P7 Windows MCP configure on none-setup | `cursor/uap-installer-windows-configure-6c84` | `cursor/uap-installer-windows-notify-6c84` | `c4d4d6f` | **NO** |

Wizard working tree at snapshot: clean, tracking origin, HEAD full SHA:

```
ebfb3e171cf828806b894254e98c12d2305de4fd
```

#183 CI on that SHA — **all 9 named jobs pass** (CodeRabbit skip is extra, not a named job):

| Job | Result |
|---|---|
| Lint | pass 25s |
| Swift notifier tests | pass 38s |
| Ubuntu 1.25 | pass 13m25s |
| Ubuntu 1.26 | pass 14m49s |
| Windows 1.25 | pass 12m11s |
| Windows 1.26 | pass 12m8s |
| macOS 1.25 | pass 11m44s |
| macOS 1.26 | pass 12m7s |

Ubuntu run: `35019920107`  
Windows run: `35019920090`  
macOS run: `35019920141`

A GitHub “success” with **checks=1** is CodeRabbit skip on draft — **not** full CI green. Require **all 9 named jobs**. Job annotations `Process completed with exit code 1` on otherwise-✓ jobs are **not** failures. Trust `gh pr checks` named-job conclusions.

---

## 5. Git / PR / CI protocol (must follow)

### Branches

- Create with: `git checkout -b cursor/<descriptive-name>-6c84`
- Prefix `cursor/`, suffix `-6c84`, **lowercase only**
- Already on wizard: `cursor/uap-installer-wizard-6c84`
- Call `SetActiveBranch` + `git checkout` if the environment flipped you to `cursor/uap-installer-p4a-handoff-6c84` (this happens constantly)

### Commits / push

- `git add` then `git commit` then **`git push -u origin <branch-name>`**
- Push retries: up to 4× exponential backoff 4s/8s/16s/32s
- Co-author on commits:

```
Co-authored-by: Илия <iliyazelenkog@gmail.com>
```

### PRs

- **`gh` is READ-ONLY.** Do **not** create/update PRs with `gh`/`origin`/raw HTTP.
- Use **`ManagePullRequest`**: `create_pr` / `update_pr` with `branch_name` and `base_branch`.
- Wizard #183 `update_pr` base: **`cursor/uap-installer-sdk-adapter-6c84`**
- PRs default draft. **`update_pr` with `draft: true` fails** — omit `draft`.
- Do not convert #183 back to draft via `draft:true`.
- After every push: `update_pr` (omit title/body unless they are wrong), then re-subscribe CI.

### CI wait (wizard)

**Do not push wizard while current remote HEAD CI is in flight.** Commit locally; push only when that head is **terminal green** (or fold a **confirmed-failure** fix into current-head if CI failed).

After each wizard push:

1. Re-subscribe CI with **identical** args:
   - repo: `777genius/agent-notifications`
   - branch: `cursor/uap-installer-wizard-6c84`
   - subscription id historically: `sub_d49d8e2d-3412-4717-8115-9ea89cc646eb`
2. Set a **900s once** backup timer `wizard-ci-<shortSHA>`
3. Ignore **own** synchronize events from cursor[bot]
4. Codecov informational
5. CodeRabbit “draft not reviewed”: no reply
6. Ignore delayed CI failures **and delayed greens** on **superseded** SHAs
7. Repeated greens may not notify; failures always deliver. CodeRabbit-only success can suppress a later all-green notification — keep the 900s timer.

### Superseded SHAs (ignore delayed CI/timer)

`8864aa2` `d8e0f8e` `e0c315d` `46fd27f` `24ef17a` `ddfb12a` `d8bd93a` `7703fd4` `c198f7b` `dd7c17f` `c027785` `dccd4fe` `35af2aa` `423b93f` `7f00522` `d3aa5b1` `87d98a7` `fa294bf` `65cd08e` `44bc33e` `8a59096` `1af8d34` `8dd9722` `53f75a9` `ee88854` `93f9316` `61d404b` `cd98598` `62dc24f` `dd7346a` `de76fc7` `7f90a15` `c21f47c` `e3e0989` `3a6e0ab` `a214b0d` `2fdeaee` `ec200de` `a77c333` `611d790`

Current live wizard HEAD at handoff: **`ebfb3e1`**. If a newer push exists, treat `ebfb3e1` as superseded too.

### Toolchain

```
GOTOOLCHAIN=local
PATH="/home/ubuntu/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.0.linux-amd64/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
GOWORK=off
```

- `gofmt -w` **changed Go files only**. **Never pass `.md` to gofmt.**
- On unix-only test files (`//go:build linux || darwin`), **never** write `runtime.GOOS == "windows"` (SA4032).
- Ubuntu/macOS `go test -race` package timeout **20m**.
- Windows E2E job `timeout-minutes: 10`.
- Do **not** add more CLI e2e.
- Do **not** add more heavy wizard fixtures without watching the 20m `-race` timeout.
- `bin/install_e2e_test.sh` `run_with_timeout` must use GNU `timeout --foreground` **or** a child-PID watchdog. Without `--foreground`, Git Bash SIGTERM’d the test script (Windows exit **3840** = 15<<8). Folded in `2fdeaee`.

---

## 6. Hard NEVER list (violations already happened or were explicitly forbidden)

Do **not**:

1. Enable **NativeObserver** (even as a host-injectable production-nil seam) unless a later human explicitly allows it. Default UAP observer queries Claude/Codex executables and **blocks mutation** when identity is indeterminate. Production `install/uapinstaller/engine.go` `lifecycle()` leaves it unset on purpose.
2. Publish **SwitchGroup** as a public hosted method.
3. Use **SwitchRetained** for live source-switch (retained-only).
4. Mix **TreeDigest** into **PackageSHA256**.
5. Add **ClientProfile** to `portable.Binding`.
6. Offer `request-permission` / `test-notification` after retained **metadata-only** Update.
7. Use **RemoveGroup** for mixed Claude+Codex uninstall that must hold Claude until Codex `--external-uninstalled`.
8. Leave `NextAction.Agents` empty when `others` is empty in two-phase add `updateRequired`.
9. Add `data_compatibility` as a **third** NextAction on two-phase `update_required` (len must stay **2**).
10. Re-untag `wizard_test.go` for Windows until UAP **0111** is fixed (`//go:build linux || darwin` stays).
11. Guess HOME/cwd for MCP config. Default **only** from already-selected **explicit profile roots** when the file already exists as a **regular file** (`Lstat` IsRegular).  
    **Do not** use `${CODEX_HOME:-$HOME/.codex}` for wizard MCP/profile forwarding.
12. Mix P7 Linux/Windows into the wizard stack.
13. Invent more inspect JSON/text polish (DataRoots, skill rows, extra fields). Fidelity: do not substitute smaller inspect work for remaining blocked plan items.
14. Wire SDK **Progress** through portablesetup on the wizard branch (that mixes P4 into P6).
15. Push green P4/P4a/P7 PRs.
16. Fold wizard/SDK into #177.
17. Add more CLI e2e.
18. `gofmt` markdown.
19. Convert #183 to draft via `update_pr draft:true`.

---

## 7. Plan phases vs current evidence

### P0 / #177 / R0

Foundation exists on `feat/agent-notify-e2e`. Remaining R0 from the plan (truthful configure incomplete, native proof, reboot) is **not** fully evidenced. Separate PRs #185 (capability-aware configure) and P7 OS branches exist — **do not merge them into wizard**. Native/client/reboot E2E: **`not_run`**.

### P1–P3 UAP public SDK

**Not published.** Hosted `install/uapinstaller/` in notifications is the temporary public surface:

- Config, New, Prepare/Apply, Inspect, Recover
- SwitchRetained, CompatibilityChecks, TreeDigest
- NativeObserver **unset**
- Config has Assess/Progress/ClientExecutables but **no NativeObserver field**

Cannot land real UAP P1–P3 without UAP write access.

### P4a #180

Handoff reservation + writer floor 2. HEAD `e29dbd1`, all green. **Do not push.**

### P4 #182

Hosted SDK + portable setup pin. HEAD `3914f92`, all 9 green. **Do not push.** Do not wire Progress through portablesetup from wizard.

### P5 UAP reusable TTY

**Blocked.** Wizard uses local `LinePrompt` in `internal/agentnotify/setupwizard/prompt.go`:

```go
// LinePrompt is a local adapter until UAP publishes the reusable P5 terminal UI.
```

P6.1 “thin TTY adapter P5 forms the same request” is **not** done as UAP pin. Do not pretend LinePrompt *is* P5.

### P6 #183 wizard — unblocked work LANDED at `ebfb3e1`

Recent commits on wizard (newest first):

```
ebfb3e1 fix(wizard): put reserved installation id on incomplete retry argv
82b9384 test(wizard): omit installation id when reinstalling unique retained data
2154117 feat(wizard): report installation id on inspect and uninstall
611d790 feat(wizard): report data_retained after last binding uninstall
a77c333 test(wizard): lock CLI inspect JSON and text MCP paths
c1f7104 feat(wizard): print discovered MCP path on inspect text output
1b2b943 feat(wizard): emit camelCase JSON for inspect targets
3ac769f test(wizard): inspect reports Claude and Codex discovered MCP paths
ec200de test(wizard): inspect reports discovered MCP ConfigPath
...
```

Key P6 semantics already implemented:

- **§5.5.1** Install is not upsert; Update is not Add.
- Mixed live Install of r2 onto Claude r2 / Codex r1 = **Update of behind sibling only**.
- Install of both when one is live on older revision and the other is absent = **two-phase Update then Add**.
- Same-bytes Install of matching live sibling = **Ready**, not `update_required`.
- After mixed siblings converge, `canGroupNotify` Install restored.
- `liveUpdateAgents` / `unboundSelectedAgents` / `updateRequired(..., liveUpdate, unbound)`:
  - non-empty `liveUpdate` → Update those; if `unbound` also non-empty → also Add (hybrid)
  - empty `liveUpdate` → classic two-phase
- `GuardSecondClient` skipped when agent is already live.
- `canGroupNotify` Install: both unbound → true; both live → `sameLiveRepairRevision` only; mixed live/absent → false.
- Sequential Repair via `liveManagedTargetsPresent` = rematerialize-after-delete **workaround** because NativeObserver is unset. Group mixed rematerialize still not the UAP NativeObserver path (Codex-only recovering in upstream UAP).
- **§9.2** noninteractive mutation requires explicit `--action`/`--agents` and `--yes` or matching pending intent. Omitted `--agents` with `--yes` → `invalid`/`agents_required` exit 2.
- **P6.5 MCP handoff:** explicit `--mcp-config`/`--claude-mcp-config` still win; otherwise a regular file already present in selected profile (`$CodexHome/config.toml`, `$ClaudeConfig/.claude.json`) via Lstat IsRegular only. Bootstrap forwards already-selected `CODEX_HOME` as `--codex-home` when flag omitted (symmetric with `CLAUDE_CONFIG_DIR` → `--claude-config`); **never invents `--mcp-config`**. Confirm-plan prints `codex-mcp=` / `claude-mcp=` from `discoveryConfigPath`. After `evaluate`, `bindDiscoveredMCP` copies resolved paths onto `req.MCPConfig` so Plan retry argv includes `--mcp-config`. `IntentTarget.MCPConfig` recorded for agent-notify targets; resume restores it; different explicit flag → `pending_intent_conflict`. Hooks-only targets do **not** record MCPConfig. Host snapshot / bootstrap first argv still omit `--mcp-config`.
- Inspect `--json` TargetResult camelCase matching Result/ReadinessFact/NextAction. Inspect reports `ConfigPath` on `direct-mcp` targets. Text inspect prints `mcp=` / `profile=` / `digest=` when set. **Do not invent more inspect fields.**
- Failed MCP → `incomplete` + `activate` retry when materialization persisted; pre-commit failure → `portable_install_failed` with no NextActions.
- **§5.7 / §7.1 data_retained:** after last live binding removed, PLUGIN_DATA remains (`PurgeData=false`). `Result.DataRetained` on last-binding uninstall, already-absent of retained-empty, and inspect of retained installation. Absent inspect rows stay absent; flag is **not** a license to run.
- Unique retained reinstall: `ReserveIdentity` with empty InstallationID and exactly one installation reuses that ID (TTY after last uninstall sees no live bindings and **omits** `--installation-id`). Ambiguous (>1) still requires explicit ID (`TestWizardAmbiguousInstallationsConflictWithoutID`).
- Incomplete retry argv copies reserved `Result.InstallationID` onto `RetryCommand` (`attachCommand`) so a later run does not allocate a different installation. `attachCommand` skips completed/cancelled/unchanged.
- Inspect of a unique installation copies InstallationID onto Result; empty inspect omits it.
- Text CLI: `writeSetupWizardResult` prints `installation-id=` then `data_retained=true` when set.

### P7 OS

Independent of wizard. Linux already wires `TrustedBootIDPath` / `DefaultClock()` on `cursor/uap-installer-linux-configure-6c84`. Wizard-branch production `journal.PlatformClock{}` **without** BootIDPath is **expected**. Do not mix.

### R2 / R3

R2 wizard application flow is largely in #183 but **P5 UI pin** and native proof are missing.  
R3 (full cross-platform MVP + native/reboot matrix + owner publish permission) is **not** achieved.

---

## 8. Key files (wizard / hosted SDK)

| Path | Why |
|---|---|
| `internal/agentnotify/setupwizard/wizard.go` | evaluate, `updateRequired`, `liveUpdateAgents`, `unboundSelectedAgents`, `canGroupNotify`, `GuardSecondClient`, `liveManagedTargetsPresent`, discovery, `bindDiscoveredMCP`, `Result.DataRetained`, `markInspectedIdentity`, uninstall identity |
| `internal/agentnotify/setupwizard/prompt.go` | `LinePrompt` (until P5), `confirmPlan` mcp lines, `attachCommand` copies InstallationID |
| `internal/agentnotify/setupwizard/wizard_test.go` | unix-only; mixed live, two-phase, retained reinstall, MCP handoff, data_retained |
| `internal/agentnotify/setupwizard/next_actions_test.go` | JSON `dataRetained`, empty inspect has neither DataRetained nor InstallationID |
| `internal/agentnotify/setupwizard/prompt_test.go` | `TestAttachCommandCopiesReservedInstallationID` |
| `cmd/claude-notifications/setup_wizard.go` | text result `installation-id=` / `data_retained=true` |
| `cmd/claude-notifications/setup_wizard_cli_contract_test.go` | `TestSetupWizardReportsDataRetained` |
| `cmd/claude-notifications/notification_bootstrap_test.go` | CODEX_HOME forwarded, no invented mcp-config |
| `bin/bootstrap.sh` | `wizard_codex_home` from `--codex-home` else `$CODEX_HOME` if set |
| `bin/install_e2e_test.sh` | `timeout --foreground` / child-PID watchdog |
| `internal/agentnotify/portablesetup/intent.go` | `IntentTarget.MCPConfig` |
| `.github/workflows/ci-windows.yml` | E2E `timeout-minutes: 10` |
| `install/uapinstaller/engine.go` | NativeObserver comment stays unset |
| `install/uapinstaller/switch_retained.go` | retained-only; live switch would require unpublished SwitchGroup |
| `install/uapinstaller/engine_test.go` | Windows skip: UAP `managedstdio.NewSource` requires `Perm()&0111` |

Important Result + attachCommand shape:

```go
type Result struct {
	Action         string          `json:"action"`
	Outcome        string          `json:"outcome"`
	Reason         string          `json:"reason,omitempty"`
	InstallationID string          `json:"installationID,omitempty"`
	Generation     uint64          `json:"generation,omitempty"`
	Command        []string        `json:"command,omitempty"`
	Targets        []TargetResult  `json:"targets,omitempty"`
	Readiness      []ReadinessFact `json:"readiness,omitempty"`
	NextActions    []NextAction    `json:"nextActions,omitempty"`
	// DataRetained is true after the last live binding is removed while
	// PLUGIN_DATA remains. Absent inspect rows are not a license to run.
	DataRetained bool `json:"dataRetained,omitempty"`
}

func attachCommand(req Request, out Result) Result {
	switch out.Outcome {
	case "completed", "cancelled", "unchanged":
		return out
	}
	if req.InstallationID == "" && out.InstallationID != "" {
		req.InstallationID = out.InstallationID
	}
	if len(out.Command) == 0 {
		out.Command = RetryCommand(req)
	}
	return out
}
```

---

## 9. Known CI war stories (do not regress)

| SHA | What happened |
|---|---|
| `de76fc7` | Windows 1.25 hung in `install.sh` E2E (30m job timeout) → `abb2eee` bounded E2E to 10m |
| `cd98598` | Ubuntu `-race` timed out → 20m package timeout |
| `a214b0d` | Windows 1.26 failed `test_force_preserves_symlinks`: `run_with_timeout 5 bash install.sh --force` vs `http://127.0.0.1:1`; GNU timeout without `--foreground` SIGTERM’d Git Bash script, exit 3840 |
| `2fdeaee` | Folded `timeout --foreground` + watchdog; Windows 1.26 then PASS 9m24s |
| `611d790` | Windows 1.26 check-run stayed `pending` after all steps including Complete job succeeded; all 3 github-actions **suites** were `success`. Agent treated suites+steps as green and pushed `ebfb3e1`. Later `gh pr checks` and GitHub event confirmed all 9 named jobs pass. |

`gh pr checks` exit 8 while jobs pending is expected, not a test failure.

---

## 10. What the next agent should actually do

**If UAP write access is still 403:** you cannot finish the plan. Do **not** invent inspect polish to look busy. Do **not** enable NativeObserver. Do **not** publish SwitchGroup. Do **not** mix P7 into wizard. Do **not** push green P4/P4a/P7. Leave the goal open.

**If UAP write access is granted (this is the real remaining work):**

1. In **UAP**, implement/publish P1–P3 per plan §11 (public installer API, lifecycle, seams/groups). Sample install→inspect→no-op→safe remove. No Notifications imports.
2. Fix Windows **0111** in `managedstdio.NewSource` (Go Windows FileMode has no execute bits on regular files). Then notifications can untag unix-only wizard tests **only after** that fix is pinned.
3. Extract **P5** reusable terminal UI; pin it into notifications; replace `LinePrompt` with the public types (same SetupRequest, no business logic in renderer).
4. Decide NativeObserver with a **human**: enabling the default observer will query Claude/Codex binaries and can block CI. Group mixed rematerialize-after-delete currently needs it in upstream `usecase/group.go`. Sequential Repair workaround must remain until a safe observer exists.
5. Do **not** publish a general source-switch API. SwitchRetained stays retained-only. SwitchGroup stays unpublished unless the plan’s P3 “existing group facade” is the actual UAP method **inside UAP**, not a new hosted wrapper.
6. After UAP commits exist: notifications consumes **exact** module version, `GOWORK=off`, no `replace`. Draft notifications PR may use an exact unpublished pin for development; merge needs an accepted UAP checkpoint.
7. Native/client/reboot E2E on real Claude+Codex, three OSes, per plan §15. Fake fixtures are not native proof. Do not claim reboot without a reboot/login machine.
8. R3 only after R1a/R1b/R2 + matrix + **owner publish permission**.

**If you must continue P6 in notifications only:** remaining unblocked P6 that is not inspect polish is **already landed** at `ebfb3e1`. Re-audit against `/tmp/uap-plan.md` before adding code. Extra unix fixtures can blow the 20m `-race` budget.

---

## 11. How to start a new session (copy into the new agent)

```
Workspace: github.com/777genius/agent-notifications
Plan: origin/docs/uap-installer-sdk-plan:docs/plans/uap-installer-sdk-and-notification-wizard-plan.md
      (local /tmp/uap-plan.md; fetch that docs branch if missing)

Current wizard: branch cursor/uap-installer-wizard-6c84
PR https://github.com/777genius/agent-notifications/pull/183
HEAD ebfb3e171cf828806b894254e98c12d2305de4fd
base_branch cursor/uap-installer-sdk-adapter-6c84
All 9 named CI jobs green. CodeRabbit skip ≠ green.

UAP: github.com/777genius/universal-agent-plugins
Push currently 403 for cursor[bot]. Clone /tmp/uap if present.
Pin: github.com/777genius/plugin-kit-ai/install/integrationctl v0.0.0-20260910100557-6af7f412cb4a

Do not CreateGoal. Do not mark goal complete until every plan item has current-state evidence.
Read this handoff fully. Follow the NEVER list. Switch to cursor/uap-installer-wizard-6c84 + SetActiveBranch before wizard edits.
gh is read-only; use ManagePullRequest. Always git push -u origin <branch>.
Branch names: cursor/<descriptive-name>-6c84
Co-authored-by: Илия <iliyazelenkog@gmail.com>
```

Also tell the new agent: **ignore delayed CI for superseded SHAs listed in §5**; **do not push over in-flight wizard CI**; **do not mix P7**.

---

## 12. Environment notes from this cloud run

- `SetActiveBranch` often reports `cursor/uap-installer-p4a-handoff-6c84` even when you are on wizard. Always `git checkout cursor/uap-installer-wizard-6c84` before edits.
- A `cd /tmp/uap` in a persistent shell will make later `gh pr view 183` hit **UAP’s** PR 183, not notifications. Always `cd /workspace` first.
- Goal-continue pings fired every ~20–60s while waiting on CI; remaining work was blocked — do not invent polish in response.
- Artifacts dir: `/opt/cursor/artifacts` → `/cursor/stores/self/artifacts`

---

## 13. Completion audit reminder

Before anyone marks this goal complete, prove **each** of:

- P1–P3 published UAP API + sample (not only hosted wrapper)
- P5 public TTY imported by wizard
- P4/P4a/P6 stacked PRs with exact pin
- §5.5.1 table (already tested in wizard; still needs UAP-side guard for external SDK consumers)
- §5.7 data_retained (wizard Result done; UAP Inspect must keep matching)
- NativeObserver policy decided and evidenced
- Windows 0111 fixed or still honestly skipped
- Native Claude+Codex session E2E + reboot matrix §15
- R0 truthful configure
- R3 owner permission

Green #183 CI is **not** plan completion.

---

*End of handoff. Snapshot 2026-09-16T05:57Z. Wizard HEAD ebfb3e1. UAP push 403.*
