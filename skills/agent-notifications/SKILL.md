---
name: agent-notifications
description: Send an Agent Notifications desktop notification when the user requests one, attention is needed, or a meaningful milestone warrants an alert during ongoing work.
---

Use the Agent Notifications plugin's `notify` tool. Installation and notification permissions must already be configured. When using MCP and `notification_status` is available, check that read-only tool for this chat before choosing navigation. Its navigation eligibility reflects configuration and caller context, not proof that the OS will display a banner or open the chat.

Leave routine task/turn completion to automatic lifecycle hooks. Do not call `notify` merely to say done or summarize the final result just before the final response, or relabel that same completion as `progress` or `attention`.
A normal "notify me when done" request does not ask for an extra alert on top of the completion hook.

Send a completion alert only when completion hooks are known disabled or unavailable (for example, an explicit MCP-only installation), or the user explicitly requests an additional separate alert.
Unknown hook state is not evidence of absence; `notification_status` does not report hook state.

In Codex Desktop, leave native questions (`request_user_input`, `request_user_input_async`) and approval/permission prompts to the app's notification flow. Do not call `notify` for the same event before opening the prompt, while waiting for the response, or after it closes. This also covers permission to call `notify` itself.

Do not relabel the same native prompt as a blocker, milestone, `progress`, or `info` to send an extra alert. A general instruction to notify when attention is needed does not request a duplicate; only an explicit request for an additional separate alert does.

Apply this rule without checking whether a banner appeared. Unknown native notification settings or delivery are not evidence that a fallback alert is needed; `notification_status` does not report them. Separate in-progress milestones and blockers without a native question/approval prompt may still use `notify`.

Notify during ongoing work when an actionable question, a blocker requiring the user's help, or a useful milestone warrants it, including an unresolved blocker when work pauses. If a known hook covers the same question or approval event, do not duplicate it. Continue the task after the MCP call when possible. Include the task name in a short title and make the body useful without exposing credentials or private source text on the lock screen. Do not send an alert for every internal step, poll, or notification result.

Choose `attention` for needed user input, `progress` for an intermediate milestone, and `info` for an informational result. Sending a notification does not mean the task is complete.

Send literal content and a stable, event-specific `request_id`. Reuse that ID and the same payload only when referring to the same notification request. A new event needs a new ID, even when its text is identical. Use a fresh high-entropy identifier such as a UUID for each new event; do not copy the example ID or use recurring labels like `review-choice-1`. When the client supplies no session ID (including informational Claude MCP calls), request IDs and the anonymous rate bucket are shared across those callers. A matching ID from another task can replay or conflict with its request; neither the process nor the working directory isolates it.

```json
{
  "title": "Installer task: input needed",
  "body": "The Windows setup is ready for a test profile. Which profile should I use?",
  "category": "attention",
  "request_id": "492456bb-3be4-440d-9dcd-bb3324e06c48",
  "navigation": "required"
}
```

When `notification_status.navigation.capability` is `eligible`, prefer `navigation: "required"` for task-related alerts, including informational results. Their category does not disable click navigation. The example above uses this mode, which is also the tool default. Eligibility is checked again when sending; it is not a delivery or click guarantee.

Use `navigation: "none"` when an alert intentionally needs no return to the chat, or navigation is unavailable and an information-only alert still satisfies the request (for example, an MCP-only setup without a desktop route). `best_effort` permits a click target when possible without requiring one. Neither mode guarantees a return to this task. If returning to the chat is required, explain unavailable navigation instead of silently downgrading it, including after a failed `required` call. An older server omitting the navigation status field does not prove navigation is disabled.

The user must have configured the route and consent for `none`; default-on configure passes `--navigation none --allow-unknown-caller true --allow-caller-asserted false` explicitly. The parser does not imply that consent. Caller-asserted context requires separate consent. These flags grant no navigation target and never admit known remote/headless callers. Do not infer locality or session identity from Claude tool metadata. The client supplies the origin: do not put a chat ID, URL, executable, app path, or shell command in tool arguments, or infer a session from the working directory, active window, or MCP process environment.

Interpret the receipt rather than assuming that a successful tool call means delivery:

- `submitted`: the OS accepted the notification; display, click, and visible task selection are not confirmed.
- `suppressed`: respect the user's settings or rate policy. Do not loop or change IDs to bypass suppression.
- `rejected`: explain the actionable reason when relevant. Correcting a known no-effect request is different from retrying an uncertain delivery.
- `unknown`, a lost reply, or a write/connection failure: do not automatically resend, use another backend, or generate a new ID. The original notification may already exist. A new user-requested send may duplicate it; explain that uncertainty first. A returned tracking ID is not a replacement request ID.

Client approval still applies. Do not change notification permissions, approval rules, enablement, or installation to make a tool call succeed. A permission prompt or notification callback is not itself a reason to send another notification.

If MCP is unavailable, an already-configured session-scoped CLI integration may pass the same JSON through stdin to the installed `notify` command. This CLI has no `notification_status` operation. Use the integration's declared route and the user's intent: prefer `required` for task-related alerts, and use `none` or `best_effort` only when returning to the chat is optional. Missing status never justifies silently downgrading a required return. Use only context supplied by that integration; do not fabricate a context file or claim that caller-asserted context is client-attested. If no qualified integration is available, report the missing setup instead of guessing a target.
