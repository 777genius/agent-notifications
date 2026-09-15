# OpenCode integration reconnaissance

Reviewed bolzzzz/agent-notifications-go at 7d03bcfa65222cbeaf961893040dbae4e25a8a08.
No runtime code imported. This is a feasibility review, not tested support.

Useful pieces:
- `.opencode/plugins/notifications.ts`: thin TypeScript event adapter spawning the shared Go handle-hook command with JSON stdin.
- Last assistant text via session messages API, child-session classification, bounded child-process timeout, plugin logging.
- `internal/hooks/hooks_opencode_test.go`: event mapping fixtures to inspect when implementing.
- Fork also contains CodeBuddy transcript normalization and Cursor hook registration; these are separate scopes.

Confirmed concerns from source inspection:
- Permission handler listens only to permission.updated; current official docs list permission.asked and permission.replied.
- resolveBinary returns found:false for the PATH candidate; runHook immediately returns when found is false. The documented PATH fallback does not run.
- Both session.idle and session.status idle dispatch completion; prove exactly-once behavior per completed turn before shipping.
- Reading a historical last assistant message needs turn identity/cancel protection to avoid stale completion notifications.
- Child stdin has no asynchronous error listener; harden EPIPE handling and observe failed notification delivery.

Recommended bounded implementation:
1. Normalize supported OpenCode events to our existing product/hook contract, without renaming the binary or replacing shared settings.
2. Version the event adapter against supported OpenCode SDK; cover completion, question, permission and API error with typed fixtures.
3. Add explicit bootstrap product choice and global plugin registration with stable runtime location, preserving foreign config.
4. Test install/update/remove and real events in a new sandbox project, including duplicate idle events and unavailable binary.

Both repositories declare GPL version 3 or later. Preserve attribution and license notices for any imported code.

Sources:
- https://github.com/bolzzzz/agent-notifications-go/blob/7d03bcfa65222cbeaf961893040dbae4e25a8a08/.opencode/plugins/notifications.ts
- https://github.com/bolzzzz/agent-notifications-go/blob/7d03bcfa65222cbeaf961893040dbae4e25a8a08/LICENSE
- https://opencode.ai/docs/plugins/
