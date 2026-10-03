# Windows observation-cache diagnostics

Observation claims keep the 250ms production budget and fail closed. A
`cache_unavailable` result does not establish whether the cause was a deadline,
private-file validation or a Windows I/O error. It also does not prove that an
attempted bit was not published. Never retry an uncertain claim automatically.

The public error message and notification receipts remain `cache_unavailable`.
Internally, declare `var failure *observation.ClaimFailure` and call
`errors.As(err, &failure)` to inspect bounded details:

- `Phase`: validation, path/root preparation, lock, clock, read/decode,
  record/encode, publication or the check after publication.
- `Class` and `OSCode`: sanitized failure category and numeric OS error.
  Windows sharing violation (32) and lock violation (33) are distinct.
- `TotalElapsed`, `StageElapsed` and `BudgetElapsed`: elapsed durations.
  Root/path preflight precedes the existing claim timer, so total duration can
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
