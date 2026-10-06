# Linux portal restart qualification (2026-10-05)

The owned X11 TEST stack reproduces an incorrect-close counterexample after notification daemon restart. This rejects its stale-notification owner-binding contract; it does not qualify a production Linux adapter. A separate recovery control proves one cold TEST callback for a newly submitted notification after restart.

## Counterexample

The finite sender A registers its TEST application and submits one notification. Its collected exit precedes an owned SIGKILL of dunst. GTK and the portal frontend retain their captured PID/start-time identities. A new owned dunst process has a different unique bus owner; finite sender B receives the same numeric notification ID, 2. B has a visible XRes-owned notification window.

A fresh finite sender, registered as application A, invokes the official portal RemoveNotification(A). GTK sends CloseNotification(2) to the current notification server. The new dunst owner sends NotificationClosed(2, reason 3) to the retained GTK owner, and B's window disappears while dunst remains alive. There are exactly two native Notify calls, one official Remove, no GUI click and no A/B callback or effect. The experiment passes only as `owned_restart_counterexample`; `restartOwnerBindingQualified=false`.

## Independent recovery control

A separate fresh TEST root repeats A submission, owned daemon crash and B submission, without Remove(A). One XTest click on the XRes-bound B window triggers the portal's genuine cold application activation. Callback PID 177 has a kernel birth lower bound of 1318758.38, later than collected B sender exit at 1318758.302175915. It receives the exact bounded B target, writes one exclusive effect and exits. No A service or effect appears. Counts are two Notify, zero Remove and one click. This qualifies only `owned_restart_recovery_control`, not safe stale removal.

## Source and evidence boundaries

Both successful scenarios capture runner SHA `c5cec5d7ef15439c04402ba462aa1ef0083cea1cbe8a5377b59865ac43eec1b7`, helper SHA `d12f1b4f70e5a6f2832d755e72efbf546966f5a8b537c36409260ab6ec9147b0` and unchanged Dockerfile SHA `06a5f0456d83f87a3e6e4b9553eec707c9de590dc1e4f87714eada0fc0dd1e9a`. Atomic exclusive helper publication preserves complete JSON visibility and duplicate rejection. Immutable A/B specifications, desktop entries and services remain unchanged. Both runs reap tracked processes, remove their owner-token-matched containers and verify exact ID/name absence.

An earlier run captured the reused ID and actual close signal but failed an incorrect mandatory method-reply barrier before window observation. Its failed result remains unchanged. GTK calls CloseNotification with callback=NULL; [GIO documents NO_REPLY_EXPECTED for that case](https://docs.gtk.org/gio/method.DBusConnection.call.html), and [return_value suppresses the reply accordingly](https://docs.gtk.org/gio/method.DBusMethodInvocation.return_value.html). The revised barrier records optional replies separately and requires the observable close effect for the negative outcome. An unanswered Close with a surviving B is unknown and forbids a click. No-Close observations have an explicit bounded scope and a final pre-click guard.

[Sanitized source-bound evidence](../evidence/navigation/2026-10-05/native-linux-portal-restart.json) retains both successful scopes and the failed prerequisite. The experiment uses Xvfb, GTK 1.15.1 and dunst 1.9.2; it establishes neither Wayland behavior nor human-visible chat routing or an installed client's identity. [Verified package metadata](navigation-linux-package-qualification-2026-10-05.md) separately records the actual public launcher contract. No production client was installed or activated.

## Reproduction

Use the existing source-provenance and archive arguments with exactly one of `--restart-owner-invalidation-test` or `--restart-recovery-control-test`. Each invocation creates a fresh owned root, nonce and container. Do not resend an unknown notification or repeat Remove within a scenario. A successful counterexample is evidence against the backend contract, not a passing product acceptance test.
