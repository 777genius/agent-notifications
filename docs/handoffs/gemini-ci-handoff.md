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
that pending G5/desktop gates are complete.

Provider checks passed: four wrapper self-tests, six existing G5 self-tests,
Python compilation in memory, workflow YAML parsing and whitespace checks.
Commands used `python3 -B ... --self-test`; no npm/build/native execution was
performed. Native CI remains unrun.
