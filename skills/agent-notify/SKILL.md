---
name: agent-notify
description: Send an Agent Notifications desktop notification when the user requests one, attention is needed, or a meaningful milestone warrants an alert during ongoing work.
---

Use the Agent Notifications plugin's `notify` tool. Installation and notification permissions must already be configured. If availability is unclear, use its read-only `notification_status`; a configured route does not prove that the OS will display a banner.

Notify for an actionable question, a blocker requiring the user's help, or a useful milestone. Include the task name in a short title and make the body useful without exposing credentials or private source text on the lock screen. Do not send an alert for every internal step, poll, or notification result.

Choose `attention` for needed user input, `progress` for an intermediate milestone, and `info` for an informational result. Sending a notification does not mean the task is complete.

Send literal content and a stable, event-specific `request_id`. Reuse that ID and the same payload only when referring to the same notification request. A new event needs a new ID, even when its text is identical. Use a fresh high-entropy identifier such as a UUID for each new event; do not copy the example ID or use recurring labels like `review-choice-1`. When the client supplies no session ID (including informational Claude MCP calls), request IDs and the anonymous rate bucket are shared across those callers. A matching ID from another task can replay or conflict with its request; neither the process nor the working directory isolates it.

```json
{
  "title": "Notification plugin: review needed",
  "body": "The implementation is ready for your choice in this task.",
  "category": "attention",
  "request_id": "492456bb-3be4-440d-9dcd-bb3324e06c48",
  "navigation": "required"
}
```

Keep `navigation=required` when a click must return to this task. Use `best_effort` or `none` only when an informational alert without exact navigation satisfies the user's request. Never silently downgrade a required return to the task. When the user has explicitly configured setup with `enable --navigation none --allow-unknown-caller true --allow-caller-asserted false`, send suitable informational alerts with `navigation: "none"`; this needs no Codex app and promises no exact Claude terminal navigation. Default-on configure passes those consent flags explicitly; the parser does not imply them. Caller-asserted context still requires separate consent. These flags are stored under route but grant no navigation target and never admit known remote/headless callers. Do not infer locality or session identity from Claude tool metadata. Setup none does not change the tool default of required, and a failed required request must never trigger a silent downgrade. The client supplies the origin: do not put a chat ID, URL, executable, app path, or shell command in tool arguments, or infer a session from the working directory, active window, or MCP process environment.

Interpret the receipt rather than assuming that a successful tool call means delivery:

- `submitted`: the OS accepted the notification; display, click, and visible task selection are not confirmed.
- `suppressed`: respect the user's settings or rate policy. Do not loop or change IDs to bypass suppression.
- `rejected`: explain the actionable reason when relevant. Correcting a known no-effect request is different from retrying an uncertain delivery.
- `unknown`, a lost reply, or a write/connection failure: do not automatically resend, use another backend, or generate a new ID. The original notification may already exist. A new user-requested send may duplicate it; explain that uncertainty first. A returned tracking ID is not a replacement request ID.

Client approval still applies. Do not change notification permissions, approval rules, enablement, or installation to make a tool call succeed. A permission prompt or notification callback is not itself a reason to send another notification.

If MCP is unavailable, an already-configured session-scoped CLI integration may pass the same JSON through stdin to the installed `notify` command. Use only context supplied by that integration; do not fabricate a context file or claim that caller-asserted context is client-attested. If no qualified integration is available, report the missing setup instead of guessing a target.
