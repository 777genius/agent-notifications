# Portable launcher independent review (2026-09-11)

Reviewer: independent security-auditor on `/private/tmp/notification-agent-notify-e2e`.
Scope: `cmd/claude-notifications/agent_notify_portable.go` and tests;
`internal/agentnotify/portable/{locator.go,files_unix.go,files_other.go}` and tests.
Not installed Desktop E2E. No files were edited by the reviewer.

**Verdict: pass-with-residuals**

No traced attack path in the reviewed files violates the launcher contract.
Identity is ledger-bound and fail-closed. Residuals are about what tests never
run: real Codex/Claude activation, Darwin path aliases, production Linux without
the test clock seam.

## 1. Actionable findings

None that meet entry → traced path → impact → exploit.

The only production hardening called out is inode identity for `os.Executable()`
(`agent_notify_portable.go` primary pin) so Darwin `/tmp` vs `/private/tmp`
cannot false-fail. That is correctness, not an impersonation bypass: a copy at
another path already fails the string check.

## 2. Contract checks that held

- Selector `^agent-notify-[0-9a-f]{64}\.json$` only; filename is SHA-256 of
  canonical Binding JSON; `Acquire` re-derives `Filename()`.
- Owner must be `existing-installer`; primary is
  `Join(Ledger.RuntimeRoot, Primary)`, not a locator-supplied executable.
- `AcquireSetupLease` then child revalidation; lock released before stdio.
- Child env replaced with `PLUGIN_DATA` + `PLUGIN_ROOT` only.
- Missing/foreign/stale/recovery/symlink/FIFO/mode failures fail closed.
- Launch does not reconstruct locators or write stdout except child MCP.

## 3. Residuals (not a contract fail in these files)

1. No actual client activation (Codex Desktop / Claude Code).
2. macOS `/tmp`→`/private/tmp` and `/var`→`/private/var` aliases: tests
   `EvalSymlinks` first; aliased `PLUGIN_DATA` can fail closed on `openat`.
3. `os.Executable()` string pin is Darwin-untested (addressed after this review).
4. Production Linux MCP still needs a boot clock; tests inject `portableTestClock`.
5. Window after parent `Release` and before child `Acquire` is intentional.
6. Authority is the kernel consumer, not the locator file, after lock drop.
7. Direct `portable-primary` does not sanitize env (only `portable-launch` does).
8. Intermediate directories are not required to be unwritable.
9. No Windows launch stdout test.
10. No native delivery / Desktop click-back in this slice.
