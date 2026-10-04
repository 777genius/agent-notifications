# Windows observation-cache diagnostics

Observation claims fail closed. The 250ms budget covers the locked read/publication
transaction after root and private temporary-file preflight; the original caller
context covers both phases. Complete claim duration can exceed 250ms. A
`cache_unavailable` result does not establish whether the cause was a deadline,
private-file validation or a Windows I/O error. It also does not prove that an
attempted bit was not published. Never retry an uncertain claim automatically.

The public error message and notification receipts remain `cache_unavailable`.
Internally, declare `var failure *observation.ClaimFailure` and call
`errors.As(err, &failure)` to inspect bounded details:

- `Phase`: validation, path/root preparation, temp preparation, lock, clock, read/decode,
  record/encode, publication or the check after publication.
- `Class` and `OSCode`: sanitized failure category and numeric OS error.
  Windows sharing violation (32) and lock violation (33) are distinct.
- `TotalElapsed`, `StageElapsed` and `BudgetElapsed`: elapsed durations.
  Root/path and private temporary-file preflight precede the claim timer, so total duration can
  exceed budget duration. `BudgetState` records the timer independently of the
  operation's error; an I/O error and an expired deadline can coexist.
- `MayHavePublished`: conservative uncertainty once publication starts.
  A successful replacing rename can be followed by a close/deadline failure.

The error retains no underlying error, path, native marker, boot identity or
cache document. `Unwrap` is deliberately unavailable. With
`AGENT_NOTIFICATIONS_OBSERVATION_DIAGNOSTICS=1`, failures emit these fixed
labels/numbers to stderr after releasing the cache lock. Default execution is
silent; stdout is unchanged.

Windows CI records the original Gemini qualification attempt and the separate
consent/no-retry repetition with Go runtime trace and sanitized stderr diagnostics. If qualification fails, the
`windows-observation-failure-go-*` artifact contains its trace, checked-out SHA
and Go version. Tests keep their real first claims, process concurrency,
security assertions and deadline; no failed attempt is rerun as success.

Inspect the failure in the job log, then use `go tool trace` from a compatible
Go version on the artifact. Fixed `observation.claim/*` regions locate the
claim stage, and `observation.publish/*` regions separate root validation,
creation, write, target validation, rename and close. Trace failure events
carry the same bounded fields. Trace regions include deferred cleanup, so use
`StageElapsed` when measuring work before the failure was detected. Child test
processes have their own runtime and are not included in the parent's trace.

A blocked Windows syscall in a Go trace does not identify its external cause.
Do not attribute a stall to antivirus or another filter driver without further
OS evidence. A passing run also cannot identify an earlier unrecorded failure.

## Attributed creation stalls

The original failures from main run [37152371344](https://github.com/777genius/agent-notifications/actions/runs/37152371344)
and PR run [37163324811](https://github.com/777genius/agent-notifications/actions/runs/37163324811)
spent 288-791ms in `NtCreateFile` while creating the private publication temp.
All eight captured failed claims then published before the final deadline check
denied delivery. The responsible filesystem/filter driver remains unidentified.

Publication now receives the existing claim context and checks it immediately
before the replacing rename. An already-expired preparation preserves the old
document and removes its temp rather than persisting an undelivered attempt.
The final deadline check remains necessary: a synchronous rename or close can
itself outlast the budget, and an uncertain committed attempt cannot be retried.
This guard does not interrupt blocked Windows I/O or guarantee availability.

The paired [access-mask experiment](https://github.com/777genius/agent-notifications/actions/runs/37189195501)
failed for both the original and narrowed-rights variant on Go 1.25. The paired
[temporary-attribute experiment](https://github.com/777genius/agent-notifications/actions/runs/37190645407)
passed both variants on both Go versions. Neither establishes an availability
cure; production creation rights and attributes remain unchanged.

## Preflight and transaction boundary

Each claim allocates one private empty temporary file before starting its 250ms
transaction context. The caller context is checked before and after allocation;
it is never refreshed. Preflight does not inspect or change attempt history.
Locking, current document/clock decisions, writing, target validation, replacing
rename and checked close remain in the transaction. The pre-rename and final
deadline checks still deny expired or uncertain publication.

Unused preparations are deleted and closed on duplicate, lock, read, decode or
cancellation exits. A duplicate now also requires a writable private cache root:
creation failure returns `cache_unavailable` before duplicate detection. This is
a conservative admission failure, never permission to deliver or retry.

This boundary change addresses the attributed temporary creation stalls without
claiming they are faster. Other synchronous I/O can still exhaust the transaction
or caller deadline. No first-valid assertion, fixed window or private ACL is relaxed.
