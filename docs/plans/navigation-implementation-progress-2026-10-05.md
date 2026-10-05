# Notification navigation implementation evidence

Objective remains the complete implementation and E2E of the navigation adapters plan. This checkpoint is not completion.

## Current checkpoint

Base: `cccc0208358763c4a2650ed84e51145ec3d5a4f1`.

- Queue, discovery and signature verification durations captured at actual worker boundaries, then published by existing callback owner on its queue. Main queue return delay remains in total preflight time without inflating signature duration.
- Fixed diagnostic labels and numeric durations only; no target paths, thread IDs or payloads.
- Existing continuous clock reused by callback token/lifecycle defaults, so their deadline epoch includes system sleep. No protocol/action fields changed.
- Full static signature validation, selected application snapshot, bounded admission, cancellation and opener guarantees retained.

Checks on macOS:

```sh
NOTIFIER_TEST_OWNED_CHILD=1 NOTIFIER_TEST_LEADER_EXITS_FIRST=1 swift test --package-path swift-notifier
git diff --check
```

115 Swift tests passed, 0 failures, 0 skips. New tests distinguish queue=2s, discovery=3s, verification=4s from a separate 6s callback-queue delay, and exercise real background/main dispatch with queue-affinity assertions. Independent read-only review found no actionable defects; its successful-background coverage observation was addressed and reviewed again. These are component/integration checks, not installed notification E2E or proof of actual sleep/resume behavior.

## Full plan ledger

| Phase | State | Missing acceptance evidence |
| --- | --- | --- |
| P0 measurement | Instrumentation implemented and checked | Installed callback baseline, 50 running samples, cold samples, errors and helper-start timing |
| P1 contracts | Existing boundaries preserved; suspend clock shared | Remaining contracts depend on qualified transport requirements |
| P2 macOS transport | Public API research partially recorded in adjacent qualification note | Authenticated destination binding, resource guarantees, bounded TCC behavior, race fixture and actual client qualification |
| P3 macOS fast path | Not implemented | P2 qualification, implementation, before/after performance, adversarial and native E2E |
| P4 sources | No migration yet | Current-source capability audit and conditional integration tests |
| P5w Windows | Not qualified | Durable installed toast callback and supported selected-client activation |
| P5l Linux | Not qualified | Durable callback, destination identity and activation/restart tests |

The user authorized native testing on their Mac in this turn. Tests must still use synthetic chats/notifications; no agent/runtime actions in real user projects. Current permission does not establish Windows/Linux availability or any native test result.

## Hosted state

Host `workers-fsn1-01`, verified machine ID `d856d40da5ad4e23b4f67773e5942842`; runtime `2763cd951bdbb0bd4722836309da16f3f2f5fe95`. Dedicated source clone is clean at base SHA. Broker launch for `an-navigation-measurement-20261005` was rejected before a worker started. Read-only admission snapshot attributes the debt to stopped jobs `an-301302-identity-20261002` and `an-301302-identity-20261002-v2` sharing one workspace. No prior job, result or registry was deleted or manually rewritten. This does not demonstrate pool exhaustion; eligible accounts exist.

Next steps: safely resolve broker admission through supported controls, build native callback measurement harness using synthetic targets on the authorized Mac, and finish P2 qualification before enabling fast path. Keep full objective active until all applicable plan acceptance has evidence.
