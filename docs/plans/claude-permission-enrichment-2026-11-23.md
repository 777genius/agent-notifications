# Claude permission notification enrichment (2026-11-23)

## Intent
Improve #632 generic `Claude needs your permission` while retaining `Notification(permission_prompt)` as the canonical delayed/idle-aware notification, including sandbox network prompts. Avoid immediately notifying about all `PermissionRequest` calls or changing permission decisions.

## Scope
- Decode only approved, bounded local transcript metadata (not whole arbitrary JSONL or raw command text) when a generic permission prompt arrives.
- Prefer exact host message if already descriptive.
- Correlate by session + uniquely unresolved tool use (matching tool_use IDs to tool_result IDs), never by `last tool`.
- Only emit an explicitly qualified tool name on unique safe evidence; otherwise return current generic host message.
- Never render `tool_input`, commands, tokens, file paths, or raw transcript records. Preserve #662 interactive pairing and duplicate suppression.
- Limit file access, path authority, time/I/O, race and session mismatch; no additional subscription or daemon.

## Adversarial review
1. A unique unresolved tool is not guaranteed to be the permission requester; label any enrichment as context rather than exact approval target, and use generic on uncertainty.
2. A known pending sandbox network prompt may lack a tool_use record. Never suppress that Notification just because transcript lookup fails.
3. Concurrent tools, subagents, stale/unflushed JSONL, malicious oversized records and symlinked paths must degrade to the host message without breaking delivery.
4. Existing `pkg/jsonl.ParseFile` reads whole transcript and discards tool IDs; use a dedicated bounded read-only metadata projection, not a whole-transcript parse in a time-sensitive hook.
5. Existing hook notification has status/config/dedup semantics; unchanged except for safe body projection.

## Verification
- Unit red/green: one valid candidate, completed tool, multiple pending, stale/foreign session, invalid data, large lines, no transcript, specific host text, interactive echo.
- Wrapper blackbox `claude_sdk_e2e.py` plus Linux/macOS/Windows CI, native Claude observation when available.
- No claim about exact permission target without native proof.

## Estimates
~120–240 production LoC + 180–320 tests, complexity 6/10, reliability target 9/10, confidence 7/10.
