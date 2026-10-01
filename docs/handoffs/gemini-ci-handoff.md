# Gemini CI handoff

Owns only `.github/workflows/gemini-native-e2e.yml`,
`scripts/gemini-native-ci.py` and this handoff. No source, dependency, G0/G5,
commit, publication or release changes. Provider work is inspection and pure
syntax/self-tests; npm, Go builds, native CLI/PTY and HTTP run only on trusted
GitHub hosted runners. The explicit runner flag is an attestation, not a sandbox
bypass. Linux amd64/arm64, Windows amd64 and macOS arm64/Intel each have a 20-minute job;
every native session uses G0's 240-second watchdog.

The wrapper builds AN with CGO enabled, no workspace/replacements and
readonly public pinned modules. It checks actual `go version -m` linked pins,
CGO/platform and clean checkout commit, records executable SHA-256, and stages
the actual SDK/agentplugins directories from `go list -m -json`. npm installs
exact Gemini 0.62.0 with optional dependencies in a new marked TEST directory.
G0 validates its required node-pty 1.1.0 pin; the native bridge verifies the
actual loaded backend. Physical Python/Node/shell/CLI paths and hashes are
recorded. Windows supplies physical pwsh and SystemRoot explicitly.

G0 constructs the minimum environments for bootstrap, driver parents and fresh
native profiles. Worker credentials, sessions, proxy settings and npm config
are never inherited into those children. All labs/artifacts live outside the
repository, cwd and owner home with `.an-gemini-TEST`; only owned children and
their groups are handled by existing G0 cleanup. Uploads contain only the two
trusted evidence manifests, public npm lock/Go graph/build metadata and wrapper
status and both actual binaries, on success and failure. No terminal logs, payloads or environment dumps.

The integrated G0 driver, PTY bridge, G5 production driver and UI contract
are committed under `tests/integration`. The UI bytes come from actual native
0.62.0 trials and are copied unchanged. G5 uses the completed N2 parser,
including real readonly inspect after install and remove. No fake hooks,
synthetic production executable or injected hook payload qualifies delivery.

The wrapper also links an actual second binary from the same clean source and
ordinary public modules, using a separate real linker build ID. Both artifacts
are stripped to honor the installer's existing 32 MiB bound. Cached objects avoid
a second compilation. Both linked identities and distinct hashes
are recorded and the second artifact qualifies update within the same live
native session. Both actual binaries are uploaded for interactive qualification.
Interrupted recovery, continuation, resume/restart and nested agents retain
their explicitly unqualified status. Darwin targets use deployment target 12.0.

CI runs G0 recorder qualification and G5 production lifecycle/native delivery
separately, with webhook only and `--desktop` omitted. Headless desktop OS/API
and visual behavior are unverified. Interactive Mac desktop/API/visual runs
remain with the main orchestrator. The event producer is the real native CLI;
the model provider is deterministic real local HTTP. No Google model-service
qualification is claimed. Green CI means implemented scenarios passed, not
that GUI or live Google model-service gates are complete.

Historical provider checks passed: four wrapper self-tests, six then-existing G5 self-tests,
Python compilation in memory, workflow YAML parsing and whitespace checks.
Commands used `python3 -B ... --self-test`; no npm/build/native execution was
performed by that provider worker.

Trusted native CI passed all five platforms at AN
`cee81a77d8aef5369456cc24ad4e9be5184b3264`, run `36859247789`:
Windows amd64, macOS arm64/Intel and Linux amd64/arm64. Every job exercised
G0 and G5, all six native cases, production install/repeat/changed-binary update/
inspect/recovery/remove, foreign preservation and live-session revocation.
Native children exited zero without forced termination. The current pure suite
contains ten G5 checks and four wrapper checks. Interactive macOS production
and owner visual evidence are recorded separately in `gemini-production-harness.md`.

Earlier failures were TEST fixture defects: Windows binary reads required
`O_BINARY`; native `write_file` uses CRLF on Windows; the explicit Windows
child environment requires PATHEXT; private TEST ACLs must cover newly created
children; ConPTY bridge cleanup must follow the confirmed native child exit;
Intel startup requires `/usr/sbin` for the bundled architecture probe's `sysctl`.
The production deadline and privacy/ownership checks were preserved.

Final strengthened native run `36869251870` at
`e7204a400a66ee4f69dbbd772a6cdd08182f9183` also passed all five targets.
It rechecks denied/cancelled tool effects after recovery to reject late writes.
No production deadline, privacy or ownership contract was relaxed.

Latest candidate run `36874294985`, exact source
`1e0665c792b960835f301d8974fb248dba4687e8`, finished successfully on all five
targets at attempt 2. Only the failed Linux arm64 job was retried once; the
other four successful target results were retained. Its first attempt reached
all six native cases but failed `production_webhook_missing` before update and
remove. Cleanup verified revocation. The cause is not proven, and success on
retry does not establish that a production defect was fixed.

Both Linux arm64 artifacts remain available: original failure `11167664376`
and successful retry `11169107128`. Their bounded `G5-evidence.json` SHA-256
values are respectively
`59aa0d64b880306245f1ea1d20e04c566372840149c2075a9f1153812437cc8b` and
`9bd2206600af4132822ecc9d33f868444d7e890eb60fabfab14018d3b9e66493`.
The successful retry recorded nine final webhook deliveries, native exit zero
without forced termination, changed-artifact update and verified revocation.
Headless OS/API and visual gates retain the qualifications stated above.

Subsequent exact candidate `4601d24002dd5c43b43c07706035582c0e7d715f`
passed all five native G0/G5 targets on the first attempt in run `36879333230`.
Its only source changes from the preceding candidate are two test fixtures:
Windows ACL acceptance tests durable duplicate admission without timing cold
publication, and the aggregate installer Node watchdog is 60s. Production
code, the 250ms claim budget and strict first-claim/concurrency tests remain.
The earlier missing-webhook failure above remains part of the evidence.

Final Windows CI separates the entire `internal/geminievent` package from
other parallel packages while retaining `-race`, atomic coverage and every
strict first-claim/interprocess assertion. Remaining packages still run in
parallel, with both coverage profiles combined. This addresses cross-package
contention as a test risk; it does not prove Defender or a particular syscall
caused the earlier time-budget failures. Production code and limits are unchanged.

Final exact head `25b79bd7786818918512d700e12b3d9ee3a0bf77` passed all five
native G0/G5 targets on the first attempt in run `36884579017`. The final
artifacts are Linux amd64 `11175176260`, Linux arm64 `11174106547`, Windows
amd64 `11174807311`, macOS arm64 `11174244350` and Intel `11173608782`.
General source CI and merge status are tracked separately in the plan.
